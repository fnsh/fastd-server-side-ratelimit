
#ifndef SERVER_SIDE_RATELIMIT_H
#define SERVER_SIDE_RATELIMIT_H

#include <stdint.h>
#include "packet.h"

#define SSR_PROTOCOL_VERSION 1

struct ssr_config {
	char *ratelimit_ifname;
	char *script;
	uint32_t downstream_min;
	uint32_t downstream_max;
	uint32_t upstream_min;
	uint32_t upstream_max;
};

struct ssr_state {
	struct ssr_config config;

	struct {
		uint64_t pkts_sent;
		uint64_t kbytes_sent;

		uint64_t pkts_recv;
		uint64_t kbytes_recv;

		uint8_t load1;
		uint8_t load5;
		uint8_t load15;

		uint8_t cpu_count;
		char target[32];
		char subtarget[32];
	} system_state;

	int communication_socket;
	uint32_t communication_sequence_number;

	uint32_t downstream_current;
	uint32_t downstream_configured;
	uint32_t upstream_current;
	uint32_t upstream_configured;
};

int ssr_communication_send(struct ssr_state *state, struct ssr_packet_v1 *packet);
int ssr_communication_receive(struct ssr_state *state, struct ssr_packet_v1 *packet);
int ssr_communication_init(struct ssr_state *state);


#endif