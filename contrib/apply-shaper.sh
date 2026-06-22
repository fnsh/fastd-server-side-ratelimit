#!/bin/sh

set -eu

usage() {
	cat >&2 <<'EOF'
Usage: apply-shaper.sh

Expected environment variables:
  FSSRL_ROLE
   - "server" or "client"
  FSSRL_TARGET_IF
   - Interface to apply shaper on (e.g. "eth0")
  FSSRL_DOWNSTREAM_RATE
   - Downstream rate in kbit/s (e.g. "10000")
  FSSRL_UPSTREAM_RATE
   - Upstream rate in kbit/s (e.g. "5000")
EOF
	return 1
}

require_var() {
	var_name="$1"
	value="$(eval "printf '%s' \"\${$var_name-}\"")"
	if [ -z "$value" ]; then
		printf 'apply-shaper.sh: missing required environment variable %s\n' "$var_name" >&2
		usage
		return 1
	fi
	printf '%s' "$value"
}

sanitize_ifname() {
	case "$1" in
		''|*[!A-Za-z0-9_.:-]* )
			printf 'apply-shaper.sh: invalid interface name %s\n' "$1" >&2
			exit 1
			;;
	esac
	printf '%s' "$1"
}

sanitize_rate() {
	rate="$1"
	case "$rate" in
		''|*[!0-9]* )
			printf 'apply-shaper.sh: invalid rate %s\n' "$rate" >&2
			exit 1
			;;
	esac
	printf '%s' "$rate"
}

run_tc() {
	tc "$@"
}


ROLE="$(require_var FSSRL_ROLE)"
if [ "$ROLE" != "server" ] && [ "$ROLE" != "client" ]; then
	printf 'apply-shaper.sh: invalid role %s\n' "$ROLE" >&2
	usage
	exit 1
fi

TARGET_IF="$(require_var FSSRL_TARGET_IF)"
TARGET_IF="$(sanitize_ifname "$TARGET_IF")"
DOWNSTREAM_RATE="$(require_var FSSRL_DOWNSTREAM_RATE)"
DOWNSTREAM_RATE="$(sanitize_rate "$DOWNSTREAM_RATE")"
UPSTREAM_RATE="$(require_var FSSRL_UPSTREAM_RATE)"
UPSTREAM_RATE="$(sanitize_rate "$UPSTREAM_RATE")"

if [ "$ROLE" = "server" ]; then
	# For server role, we shape the downstream traffic (towards clients)
	# and use the upstream rate as the bandwidth limit for the shaper.
	run_tc qdisc replace dev "$TARGET_IF" root cake bandwidth "${DOWNSTREAM_RATE}kbit" besteffort ack-filter
else
	# For client role, we shape the upstream traffic (towards server)
	# and use the downstream rate as the bandwidth limit for the shaper.
	run_tc qdisc replace dev "$TARGET_IF" root cake bandwidth "${UPSTREAM_RATE}kbit" besteffort ack-filter
fi

exit 0
