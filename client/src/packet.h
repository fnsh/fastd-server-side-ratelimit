#ifndef SSR_PACKET_H
#define SSR_PACKET_H

#include <stdint.h>

#define SSR_GCF_SERVER_DISABLE_DOWNSTREAM_SHAPING 0x00000001
#define SSR_GCF_SERVER_DISABLE_UPSTREAM_SHAPING   0x00000002
#define SSR_GCF_CLIENT_DISABLE_DOWNSTREAM_SHAPING 0x00000004
#define SSR_GCF_CLIENT_DISABLE_UPSTREAM_SHAPING   0x00000008
struct ssr_packet_v1_machine_information {
	uint8_t target[32];
	uint8_t subtarget[32];
	uint8_t cpu_core_count;
	uint8_t model_name[32];
	uint8_t reserved[27];
} __attribute__((packed));

struct ssr_packet_v1 {
	uint8_t version;
	uint32_t sequence_number;

	struct ssr_packet_v1_machine_information machine_information;

	uint32_t global_configuration_flags;

	uint8_t load1;
	uint8_t load5;
	uint8_t load15;
	uint8_t load_reserved;

	uint64_t pkts_sent;
	uint64_t kbs_sent;
	uint64_t pkts_recv;
	uint64_t kbs_recv;

	uint32_t downstream_target;
	uint32_t downstream_configured;
	uint32_t downstream_min;
	uint32_t downstream_max;

	uint32_t upstream_target;
	uint32_t upstream_configured;
	uint32_t upstream_min;
	uint32_t upstream_max;

	uint8_t reserved[55];
} __attribute__((packed));

#endif