#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <errno.h>
#include <net/if.h>
#include <fcntl.h>
#include <errno.h>

#include "server-side-ratelimit.h"

int ssr_communication_socket_create(void)
{
	int fd;

	fd = socket(AF_INET6, SOCK_DGRAM, 0);
	if (fd < 0) {
		perror("socket");
		return -1;
	}

	/* Allow reuse of address */
	int reuse = 1;
	if (setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &reuse, sizeof(reuse)) < 0) {
		perror("setsockopt SO_REUSEADDR");
		close(fd);
		return -1;
	}


	/* Non-blocking */
	if (fcntl(fd, F_SETFL, O_NONBLOCK) < 0) {
		perror("fcntl");
		close(fd);
		return -1;
	}

	return fd;
}

int ssr_communication_bind(struct ssr_state *state)
{
	struct sockaddr_in6 addr;
	memset(&addr, 0, sizeof(addr));
	addr.sin6_family = AF_INET6;
	addr.sin6_addr = in6addr_any;
	inet_pton(AF_INET6, "fe80::f421:d:2", &addr.sin6_addr);
	addr.sin6_port = htons(42454);
	addr.sin6_scope_id = if_nametoindex(state->config.ratelimit_ifname);

	if (bind(state->communication_socket, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
		perror("bind");
		close(state->communication_socket);
		return -1;
	}

	return 0;
}

int ssr_communication_send(struct ssr_state *state, struct ssr_packet_v1 *packet)
{
	struct sockaddr_in6 addr;
	memset(&addr, 0, sizeof(addr));
	addr.sin6_family = AF_INET6;
	inet_pton(AF_INET6, "fe80::f421:d:1", &addr.sin6_addr);
	addr.sin6_port = htons(42453);
	addr.sin6_scope_id = if_nametoindex(state->config.ratelimit_ifname);

	ssize_t sent_bytes = sendto(state->communication_socket, packet, sizeof(*packet), 0,
	                             (struct sockaddr *)&addr, sizeof(addr));
	if (sent_bytes < 0) {
		perror("sendto");
		return -1;
	} else if ((size_t)sent_bytes != sizeof(*packet)) {
		fprintf(stderr, "Partial packet sent\n");
		return -1;
	}

	return 0;
}

int ssr_communication_receive(struct ssr_state *state, struct ssr_packet_v1 *packet)
{
	ssize_t recv_bytes = recvfrom(state->communication_socket, packet, sizeof(*packet), MSG_DONTWAIT, NULL, NULL);
	if (recv_bytes < 0) {
		if (errno == EAGAIN || errno == EWOULDBLOCK) {
			/* No data available, not an error */
			return -1;
		}
		fprintf(stderr, "recv failed: %s\n", strerror(errno));
		return -1;
	} else if ((size_t)recv_bytes != sizeof(*packet)) {
		fprintf(stderr, "Partial packet received: %ld bytes\n", recv_bytes);
		return -1;
	}

	return 0;
}

int ssr_communication_init(struct ssr_state *state)
{
	state->communication_socket = ssr_communication_socket_create();
	if (state->communication_socket < 0) {
		return -1;
	}

	/* Bind to interface */
	if (ssr_communication_bind(state) < 0) {
		return -1;
	}

	return 0;
}