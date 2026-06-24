#ifndef SERVER_SIDE_RATELIMIT_LOG_H
#define SERVER_SIDE_RATELIMIT_LOG_H

#include <syslog.h>

enum ssr_log_destination {
	SSR_LOG_DEST_STDERR = 0,
	SSR_LOG_DEST_SYSLOG = 1,
};

void ssr_log_init(enum ssr_log_destination destination, int level);
void ssr_log_close(void);
void ssr_log_set_level(int level);
int ssr_log_parse_level(const char *level_name, int *level);
const char *ssr_log_level_name(int level);
void ssr_log(int level, const char *format, ...);
void ssr_log_errno(int level, int errnum, const char *format, ...);

#endif
