#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <stdint.h>

#include <arpa/inet.h>

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
	if (!config->downstream_min) {
		fprintf(stderr, "Downstream min not set - Configuring to 2048 kbps\n");
		config->downstream_min = 2048;
	}

	if (!config->upstream_min) {
		fprintf(stderr, "Upstream min not set - Configuring to 512 kbps\n");
		config->upstream_min = 512;
	}

	if (config->downstream_max && config->downstream_min > config->downstream_max) {
		fprintf(stderr, "Downstream min cannot be greater than max\n");
		return -1;
	}
	if (config->upstream_max && config->upstream_min > config->upstream_max) {
		fprintf(stderr, "Upstream min cannot be greater than max\n");
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
		fprintf(stderr, "No script configured to apply rate limit\n");
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
		perror("popen");
	} else {
		ret = pclose(fp);
	}

	for (int i = 0; i < sizeof(envs) / sizeof(envs[0]); i++) {
		unsetenv(envs[i].envname);
	}
	if (ret < 0) {
		perror("pclose");
		return -1;
	} else if (WEXITSTATUS(ret) != 0) {
		fprintf(stderr, "apply_rate.sh failed with exit code %d\n", WEXITSTATUS(ret));
		return -1;
	}
	return 0;
}

int ssr_handle_received_packet(struct ssr_state *state, struct ssr_packet_v1 *packet)
{
	// Extract Rate information and apply
	uint32_t downstream_current = ntohl(packet->downstream_current);
	uint32_t upstream_current = ntohl(packet->upstream_current);

	state->downstream_configured = ntohl(packet->downstream_configured);
	state->upstream_configured = ntohl(packet->upstream_configured);

	printf("Received rate limit update: downstream %u kbps, upstream %u kbps\n", downstream_current, upstream_current);

	// Check if this is within configured limits
	if (state->config.downstream_min) {
		if (downstream_current < state->config.downstream_min) {
			downstream_current = state->config.downstream_min;
		} else if (downstream_current > state->config.downstream_max) {
			downstream_current = state->config.downstream_max;
		}
	}

	if (state->config.upstream_min) {
		if (upstream_current < state->config.upstream_min) {
			upstream_current = state->config.upstream_min;
		} else if (upstream_current > state->config.upstream_max) {
			upstream_current = state->config.upstream_max;
		}
	}

	state->downstream_current = downstream_current;
	state->upstream_current = upstream_current;

	fprintf(stderr, "Applying rate limit: downstream %u kbps, upstream %u kbps\n", downstream_current, upstream_current);

	return ssr_apply_rate_limit(state, downstream_current, upstream_current);
}

int ssr_packet_build(struct ssr_state *state, struct ssr_packet_v1 *packet)
{
	memset(packet, 0, sizeof(*packet));
	packet->version = SSR_PROTOCOL_VERSION;
	packet->sequence_number = htonl(state->communication_sequence_number++);

	/* Rates */
	packet->downstream_current = htonl(state->downstream_current);
	packet->downstream_configured = htonl(state->downstream_configured);
	packet->downstream_min = htonl(state->config.downstream_min);
	packet->downstream_max = htonl(state->config.downstream_max);
	packet->upstream_current = htonl(state->upstream_current);
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
	state.config.ratelimit_ifname = "wlp1s0";

	static struct option longopts[] = {
		{"interface", required_argument, NULL, 'i'},
		{"script", required_argument, NULL, 'p'},
		{"target", required_argument, NULL, 't'},
		{"subtarget", required_argument, NULL, 's'},
		{"downstream-min", required_argument, NULL, 'a'},
		{"downstream-max", required_argument, NULL, 'b'},
		{"upstream-min", required_argument, NULL, 'c'},
		{"upstream-max", required_argument, NULL, 'd'},
		{NULL, 0, NULL, 0}
	};

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
		default:
			fprintf(stderr, "Usage: %s --interface IFNAME --downstream-min N --downstream-max N --upstream-min N --upstream-max N --target TARGET --subtarget SUBTARGET\n", argv[0]);
			return 1;
		}
	}

	if (ssr_validate_config(&state.config) < 0) {
		return 1;
	}

	if (ssr_communication_init(&state) < 0) {
		return 1;
	}

	while (1) {
		struct ssr_packet_v1 packet;

		memset(&packet, 0, sizeof(packet));
		ssr_update_system_state(&state);
		printf("Load: %u, %u, %u | Sent: %lu pkts, %lu kbytes | Recv: %lu pkts, %lu kbytes\n",
		       state.system_state.load1, state.system_state.load5, state.system_state.load15,
		       state.system_state.pkts_sent, state.system_state.kbytes_sent,
		       state.system_state.pkts_recv, state.system_state.kbytes_recv);
		sleep(1);
		ssr_packet_build(&state, &packet);
		ssr_communication_send(&state, &packet	);
		if (ssr_communication_receive(&state, &packet) == 0) {
			ssr_handle_received_packet(&state, &packet);
		}
	}
	return 0;
}
