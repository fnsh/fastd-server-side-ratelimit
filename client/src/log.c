#include "log.h"

#include <stdarg.h>
#include <stdbool.h>
#include <stdio.h>
#include <string.h>
#include <strings.h>

static enum ssr_log_destination current_destination = SSR_LOG_DEST_STDERR;
static int current_level = LOG_INFO;
static bool syslog_open = false;

struct ssr_log_level_name {
	int level;
	const char *name;
};

static const struct ssr_log_level_name level_names[] = {
	{LOG_EMERG, "emerg"},
	{LOG_ALERT, "alert"},
	{LOG_CRIT, "crit"},
	{LOG_ERR, "err"},
	{LOG_WARNING, "warning"},
	{LOG_NOTICE, "notice"},
	{LOG_INFO, "info"},
	{LOG_DEBUG, "debug"},
};

static void ssr_log_v(int level, int errnum, const char *format, va_list ap)
{
	if (level > current_level) {
		return;
	}

	if (current_destination == SSR_LOG_DEST_SYSLOG) {
		char buffer[512];
		vsnprintf(buffer, sizeof(buffer), format, ap);
		if (errnum != 0) {
			syslog(level, "%s: %s", buffer, strerror(errnum));
		} else {
			syslog(level, "%s", buffer);
		}
		return;
	}

	fprintf(stderr, "%s: ", ssr_log_level_name(level));
	vfprintf(stderr, format, ap);
	if (errnum != 0) {
		fprintf(stderr, ": %s", strerror(errnum));
	}
	fputc('\n', stderr);
}

void ssr_log_init(enum ssr_log_destination destination, int level)
{
	current_destination = destination;
	current_level = level;

	if (current_destination == SSR_LOG_DEST_SYSLOG && !syslog_open) {
		openlog("fssrl-client", LOG_PID, LOG_DAEMON);
		syslog_open = true;
	}
}

void ssr_log_close(void)
{
	if (syslog_open) {
		closelog();
		syslog_open = false;
	}
}

void ssr_log_set_level(int level)
{
	current_level = level;
}

int ssr_log_parse_level(const char *level_name, int *level)
{
	if (level_name == NULL || level == NULL) {
		return -1;
	}

	for (size_t i = 0; i < sizeof(level_names) / sizeof(level_names[0]); i++) {
		if (strcasecmp(level_name, level_names[i].name) == 0) {
			*level = level_names[i].level;
			return 0;
		}
	}

	if (strcasecmp(level_name, "error") == 0) {
		*level = LOG_ERR;
		return 0;
	}
	if (strcasecmp(level_name, "warning") == 0 || strcasecmp(level_name, "warn") == 0) {
		*level = LOG_WARNING;
		return 0;
	}
	if (strcasecmp(level_name, "critical") == 0) {
		*level = LOG_CRIT;
		return 0;
	}
	if (strcasecmp(level_name, "emergency") == 0) {
		*level = LOG_EMERG;
		return 0;
	}

	return -1;
}

const char *ssr_log_level_name(int level)
{
	for (size_t i = 0; i < sizeof(level_names) / sizeof(level_names[0]); i++) {
		if (level_names[i].level == level) {
			return level_names[i].name;
		}
	}

	return "unknown";
}

void ssr_log(int level, const char *format, ...)
{
	va_list ap;

	va_start(ap, format);
	ssr_log_v(level, 0, format, ap);
	va_end(ap);
}

void ssr_log_errno(int level, int errnum, const char *format, ...)
{
	va_list ap;

	va_start(ap, format);
	ssr_log_v(level, errnum, format, ap);
	va_end(ap);
}
