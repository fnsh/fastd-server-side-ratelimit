#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <stdint.h>
#include <sys/wait.h>
#include <errno.h>

#include <arpa/inet.h>
#include <net/if.h>

#include "log.h"
#include "server-side-ratelimit.h"
#include <getopt.h>
#include <endian.h>

int ssr_system_load_get(uint8_t *load1, uint8_t *load5, uint8_t *load15)
{
	FILE *fp = fopen("/proc/loadavg", "r");
	if (!fp) {
		perror("fopen");
		return -1;
	}

	float load1_f, load5_f, load15_f;
	if (fscanf(fp, "%f %f %f", &load1_f, &load5_f, &load15_f) != 3) {
		perror("fscanf");
		fclose(fp);
		return -1;
	}
	fclose(fp);

	*load1 = (uint8_t)(load1_f * 10);
	*load5 = (uint8_t)(load5_f * 10);
	*load15 = (uint8_t)(load15_f * 10);

	return 0;
}

int ssr_system_interface_procfs_get(const char *ifname, const char *field, uint64_t *value)
{
	char path[256];
	snprintf(path, sizeof(path), "/sys/class/net/%s/statistics/%s", ifname, field);
	FILE *fp = fopen(path, "r");
	if (!fp) {
		perror("fopen");
		return -1;
	}
	if (fscanf(fp, "%lu", value) != 1) {
		perror("fscanf");
		fclose(fp);
		return -1;
	}
	fclose(fp);
	return 0;
}

int ssr_system_traffic_get(const char *ifname, uint64_t *pkts_sent, uint64_t *kbytes_sent, uint64_t *pkts_recv, uint64_t *kbytes_recv)
{
	if (ssr_system_interface_procfs_get(ifname, "tx_packets", pkts_sent) < 0) {
		return -1;
	}
	if (ssr_system_interface_procfs_get(ifname, "tx_bytes", kbytes_sent) < 0) {
		return -1;
	}
	if (ssr_system_interface_procfs_get(ifname, "rx_packets", pkts_recv) < 0) {
		return -1;
	}
	if (ssr_system_interface_procfs_get(ifname, "rx_bytes", kbytes_recv) < 0) {
		return -1;
	}

	return 0;
}

int ssr_update_system_state(struct ssr_state *state)
{
	uint8_t load1, load5, load15;
	if (ssr_system_load_get(&load1, &load5, &load15) < 0) {
		return -1;
	}

	state->system_state.load1 = load1;
	state->system_state.load5 = load5;
	state->system_state.load15 = load15;

	/* Read number of CPU cores */
	state->system_state.cpu_count = sysconf(_SC_NPROCESSORS_ONLN);

	/* Read number of bytes and packets xferred */
	if (ssr_system_traffic_get(state->config.ratelimit_ifname, &state->system_state.pkts_sent, &state->system_state.kbytes_sent, &state->system_state.pkts_recv, &state->system_state.kbytes_recv) < 0) {
		return -1;
	}

	return 0;
}

int ssr_validate_config(struct ssr_config *config)
{
	if (config->downstream_max && config->downstream_min > config->downstream_max) {
		ssr_log(LOG_ERR, "Downstream min cannot be greater than max");
		return -1;
	}
	if (config->upstream_max && config->upstream_min > config->upstream_max) {
		ssr_log(LOG_ERR, "Upstream min cannot be greater than max");
		return -1;
	}

	if (config->interval_seconds <= 0) {
		ssr_log(LOG_ERR, "Interval seconds must be greater than 0");
		return -1;
	}

	return 0;
}

struct ssr_script_env {
	char envname[32];
	char *value;
};
int ssr_apply_rate_limit(struct ssr_state *state, uint32_t downstream_rate, uint32_t upstream_rate)
{
	// Prepare Environment variables for script
	char downstream_str[16];
	char upstream_str[16];
	struct ssr_script_env envs[] = {
		[0] = {"FSSRL_ROLE", "client"},
		[1] = {"FSSRL_TARGET_IF", state->config.ratelimit_ifname},
		[2] = {"FSSRL_DOWNSTREAM_RATE", downstream_str},
		[3] = {"FSSRL_UPSTREAM_RATE", upstream_str}
	};

	if (state->config.script == NULL) {
		ssr_log(LOG_ERR, "No script configured to apply rate limit");
		return -1;
	}

	snprintf(downstream_str, 16, "%u", downstream_rate);
	snprintf(upstream_str, 16, "%u", upstream_rate);

	for (int i = 0; i < sizeof(envs) / sizeof(envs[0]); i++) {
		setenv(envs[i].envname, envs[i].value, 1);
	}
	
	FILE *fp = popen(state->config.script, "r");
	int ret = -1;
	if (fp == NULL) {
		ssr_log_errno(LOG_ERR, errno, "popen");
	} else {
		ret = pclose(fp);
	}

	for (int i = 0; i < sizeof(envs) / sizeof(envs[0]); i++) {
		unsetenv(envs[i].envname);
	}
	if (ret < 0) {
		ssr_log_errno(LOG_ERR, errno, "pclose");
		return -1;
	} else if (WEXITSTATUS(ret) != 0) {
		ssr_log(LOG_ERR, "apply_rate.sh failed with exit code %d", WEXITSTATUS(ret));
		return -1;
	}

	state->rate_update_force = 0;
	return 0;
}

int ssr_handle_received_packet(struct ssr_state *state, struct ssr_packet_v1 *packet)
{
	// Extract Rate information and apply
	uint32_t downstream_target = ntohl(packet->downstream_target);
	uint32_t upstream_target = ntohl(packet->upstream_target);

	uint32_t downstream_configured_new = downstream_target;
	uint32_t upstream_configured_new = upstream_target;

	int update = 0;

	state->downstream_target = downstream_target;
	state->upstream_target = upstream_target;

	if (packet->downstream_configured != state->last_server_packet.downstream_configured ||
	    packet->upstream_configured != state->last_server_packet.upstream_configured) {
		ssr_log(LOG_INFO, "Server has updated configured rates: downstream %u kbps, upstream %u kbps", ntohl(packet->downstream_configured), ntohl(packet->upstream_configured));
	}

	if (packet->downstream_target != state->last_server_packet.downstream_target ||
	    packet->upstream_target != state->last_server_packet.upstream_target) {
		ssr_log(LOG_INFO, "Server has updated target rates: downstream %u kbps, upstream %u kbps", ntohl(packet->downstream_target), ntohl(packet->upstream_target));
	}

	if (packet->downstream_min != state->last_server_packet.downstream_min ||
	    packet->downstream_max != state->last_server_packet.downstream_max) {
		ssr_log(LOG_INFO, "Server has updated downstream limits: min %u kbps, max %u kbps", ntohl(packet->downstream_min), ntohl(packet->downstream_max));
	}

	if (packet->upstream_min != state->last_server_packet.upstream_min ||
	    packet->upstream_max != state->last_server_packet.upstream_max) {
		ssr_log(LOG_INFO, "Server has updated upstream limits: min %u kbps, max %u kbps", ntohl(packet->upstream_min), ntohl(packet->upstream_max));
	}

	// Check if this is within configured limits
	if (state->config.downstream_min) {
		if (downstream_configured_new < state->config.downstream_min) {
			downstream_configured_new = state->config.downstream_min;
		} else if (downstream_configured_new > state->config.downstream_max) {
			downstream_configured_new = state->config.downstream_max;
		}
	}

	if (state->config.upstream_min) {
		if (upstream_configured_new < state->config.upstream_min) {
			upstream_configured_new = state->config.upstream_min;
		} else if (upstream_configured_new > state->config.upstream_max) {
			upstream_configured_new = state->config.upstream_max;
		}
	}

	if (state->downstream_configured != downstream_configured_new) {
		ssr_log(LOG_INFO, "Updating downstream rate limit from %u kbps to %u kbps", state->downstream_configured, downstream_configured_new);
		update = 1;
	}

	if (state->upstream_configured != upstream_configured_new) {
		ssr_log(LOG_INFO, "Updating upstream rate limit from %u kbps to %u kbps", state->upstream_configured, upstream_configured_new);
		update = 1;
	}

	state->downstream_configured = downstream_configured_new;
	state->upstream_configured = upstream_configured_new;

	/* Save the last received server packet */
	memcpy(&state->last_server_packet, packet, sizeof(*packet));

	if (update || state->rate_update_force) {
		ssr_log(LOG_INFO, "Applying rate limit: downstream %u kbps, upstream %u kbps", downstream_target, upstream_target);
		return ssr_apply_rate_limit(state, downstream_configured_new, upstream_configured_new);
	}

	return 0;
}

int ssr_packet_build(struct ssr_state *state, struct ssr_packet_v1 *packet)
{
	memset(packet, 0, sizeof(*packet));
	packet->version = SSR_PROTOCOL_VERSION;
	packet->sequence_number = htonl(state->communication_sequence_number++);

	/* Rates */
	packet->downstream_target = htonl(state->downstream_target);
	packet->downstream_configured = htonl(state->downstream_configured);
	packet->downstream_min = htonl(state->config.downstream_min);
	packet->downstream_max = htonl(state->config.downstream_max);
	packet->upstream_target = htonl(state->upstream_target);
	packet->upstream_configured = htonl(state->upstream_configured);
	packet->upstream_min = htonl(state->config.upstream_min);
	packet->upstream_max = htonl(state->config.upstream_max);

	/* System state */
	packet->load1 = state->system_state.load1;
	packet->load5 = state->system_state.load5;
	packet->load15 = state->system_state.load15;
	
	/* Machine information */
	packet->machine_information.cpu_core_count = state->system_state.cpu_count;
	strncpy(packet->machine_information.target, state->system_state.target, sizeof(packet->machine_information.target) - 1);
	packet->machine_information.target[sizeof(packet->machine_information.target) - 1] = '\0';
	strncpy(packet->machine_information.subtarget, state->system_state.subtarget, sizeof(packet->machine_information.subtarget) - 1);
	packet->machine_information.subtarget[sizeof(packet->machine_information.subtarget) - 1] = '\0';

	/* Network statistics */
	packet->pkts_sent = htobe64(state->system_state.pkts_sent);
	packet->kbs_sent = htobe64(state->system_state.kbytes_sent);
	packet->pkts_recv = htobe64(state->system_state.pkts_recv);
	packet->kbs_recv = htobe64(state->system_state.kbytes_recv);
	return 0;
}

int main(int argc, char *argv[])
{
	struct ssr_state state;

	/* defaults */
	memset(&state, 0, sizeof(state));
	state.communication_socket = -1;

	static struct option longopts[] = {
		{"interface", required_argument, NULL, 'i'},
		{"disable-shaping", required_argument, NULL, 'f'},
		{"script", required_argument, NULL, 'p'},
		{"target", required_argument, NULL, 't'},
		{"subtarget", required_argument, NULL, 's'},
		{"downstream-min", required_argument, NULL, 'a'},
		{"downstream-max", required_argument, NULL, 'b'},
		{"upstream-min", required_argument, NULL, 'c'},
		{"upstream-max", required_argument, NULL, 'd'},
		{"interval", required_argument, NULL, 'n'},
		{"log-syslog", no_argument, NULL, 'l'},
		{"log-level", required_argument, NULL, 'v'},
		{NULL, 0, NULL, 0}
	};
	int log_destination = SSR_LOG_DEST_STDERR;
	int log_level = LOG_INFO;
	int ret = 0;

	int opt;
	while ((opt = getopt_long(argc, argv, "", longopts, NULL)) != -1) {
		switch (opt) {
		case 'i':
			state.config.ratelimit_ifname = optarg;
			break;
		case 'a':
			state.config.downstream_min = strtoul(optarg, NULL, 10);
			break;
		case 'b':
			state.config.downstream_max = strtoul(optarg, NULL, 10);
			break;
		case 'c':
			state.config.upstream_min = strtoul(optarg, NULL, 10);
			break;
		case 'd':
			state.config.upstream_max = strtoul(optarg, NULL, 10);
			break;
		case 'f':
			if (strcmp(optarg, "downstream-local") == 0) {
				state.config.disable_shaping.local.downstream = true;
			} else if (strcmp(optarg, "upstream-local") == 0) {
				state.config.disable_shaping.local.upstream = true;
			} else if (strcmp(optarg, "downstream-remote") == 0) {
				state.config.disable_shaping.remote.downstream = true;
			} else if (strcmp(optarg, "upstream-remote") == 0) {
				state.config.disable_shaping.remote.upstream = true;
			} else {
				fprintf(stderr, "Unknown disable-shaping option: %s\n", optarg);
				return 1;
			}
			break;
		case 'n':
			state.config.interval_seconds = strtoul(optarg, NULL, 10);
			break;
		case 't':
			strncpy(state.system_state.target, optarg, sizeof(state.system_state.target) - 1);
			state.system_state.target[sizeof(state.system_state.target) - 1] = '\0';
			break;
		case 's':
			strncpy(state.system_state.subtarget, optarg, sizeof(state.system_state.subtarget) - 1);
			state.system_state.subtarget[sizeof(state.system_state.subtarget) - 1] = '\0';
			break;
		case 'p':
			state.config.script = optarg;
			break;
		case 'l':
			log_destination = SSR_LOG_DEST_SYSLOG;
			break;
		case 'v':
			if (ssr_log_parse_level(optarg, &log_level) < 0) {
				fprintf(stderr, "Unknown log level: %s\n", optarg);
				return 1;
			}
			break;
		default:
			fprintf(stderr, "Usage: %s --interface IFNAME --downstream-min N --downstream-max N --upstream-min N --upstream-max N --target TARGET --subtarget SUBTARGET [--log-syslog] [--log-level LEVEL]\n", argv[0]);
			return 1;
		}
	}

	ssr_log_init(log_destination, log_level);

	if (ssr_validate_config(&state.config) < 0) {
		ret = 1;
		goto out;
	}

	while (1) {
		struct ssr_packet_v1 packet;
		int scope_id = if_nametoindex(state.config.ratelimit_ifname);

		/* Need to validate Network is set up correctly.
		 * The interface might not be there yet or have disappeared.
		 *
		 * For this reason, check if the interface is present.
		 */

		if (scope_id != state.communication_scope_id) {
			ssr_log(LOG_INFO, "Interface %s scope_id changed from %d to %d", state.config.ratelimit_ifname, state.communication_scope_id, scope_id);
			state.communication_scope_id = scope_id;
			state.communication_ok = 0;
			ssr_communication_close(&state);
		}

		if (!state.communication_ok){
			ret = ssr_communication_init(&state);
			if (ret < 0) {
				if (state.communication_ok) {
					ssr_log(LOG_ERR, "Communication init failed: %d", ret);
					state.communication_ok = 0;
				}
				ssr_communication_close(&state);
				sleep(5);
				continue;
			} else {
				ssr_log(LOG_INFO, "Communication init successful");
				state.communication_ok = 1;
				state.rate_update_force = 1;
			}
		}

		if (state.communication_last_send_time + state.config.interval_seconds < time(NULL)) {
			state.communication_last_send_time = time(NULL);

			memset(&packet, 0, sizeof(packet));
			if (ssr_update_system_state(&state) < 0) {
				ssr_log(LOG_ERR, "Failed to update system state");
				continue;
			}
			ssr_log(LOG_DEBUG, "Load: %u, %u, %u | Sent: %lu pkts, %lu kbytes | Recv: %lu pkts, %lu kbytes",
				state.system_state.load1, state.system_state.load5, state.system_state.load15,
				state.system_state.pkts_sent, state.system_state.kbytes_sent,
				state.system_state.pkts_recv, state.system_state.kbytes_recv);
			ssr_packet_build(&state, &packet);
			ssr_communication_send(&state, &packet);
		}
		
		if (ssr_communication_receive(&state, &packet) == 0) {
			ssr_handle_received_packet(&state, &packet);
		}

		sleep(1);
	}
out:
	ssr_log_close();
	return ret;
}
