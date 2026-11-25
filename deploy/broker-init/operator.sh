#!/bin/sh
# Gives one operator their own RabbitMQ and Redis logins, or takes them away.
#
#   operator.sh add <name>      password on standard input; run again to change it
#   operator.sh revoke <name>
#
# The name must equal the person's Postgres login: Studio refuses to requeue or
# purge with any other broker login, so the three names are what tie an action to
# a person.
#
# RABBITMQCTL and REDIS_CLI are the commands to run. They must pass their arguments
# on unchanged (a local binary, sudo, a container exec). ssh does not: its remote
# shell would interpret the permission patterns, so run this file on the broker host.
#
# RABBITMQ_VHOST is the virtual host (default /). A login has one: add starts over,
# so permissions on another virtual host are gone afterwards.
set -eu

# Character ranges follow the locale, and some locales fold case.
LC_ALL=C
export LC_ALL

RABBITMQCTL=${RABBITMQCTL:-rabbitmqctl}
REDIS_CLI=${REDIS_CLI:-redis-cli}
RABBITMQ_VHOST=${RABBITMQ_VHOST:-/}

usage() {
	echo "usage: operator.sh add|revoke <name>   (op_<name>, or stratum_operator_dev locally)" >&2
	exit 2
}

[ $# -eq 2 ] || usage
action=$1
name=$2

# Only operator names: revoking the app's own user would stop the app.
case $name in
*[!a-z0-9_]*) usage ;;
op_?* | stratum_operator_dev) ;;
*) usage ;;
esac

case " $RABBITMQCTL $REDIS_CLI " in
*" ssh "* | *"/ssh "*)
	echo "operator.sh: ssh would not pass the arguments on unchanged; run this file on the broker host" >&2
	exit 2
	;;
esac

# drop_rabbitmq_user removes the login if it exists, which also closes its
# connections. Not being able to list the users is a failure, never an absence.
drop_rabbitmq_user() {
	# shellcheck disable=SC2086
	users=$($RABBITMQCTL -q list_users </dev/null) || return 1
	if printf '%s\n' "$users" | cut -f1 | grep -qx "$name"; then
		# shellcheck disable=SC2086
		$RABBITMQCTL -q delete_user "$name" </dev/null || return 1
	fi
}

# redis-cli -e exits non-zero when the server refuses the command. Deleting a user
# also closes its connections.
drop_redis_user() {
	# shellcheck disable=SC2086
	$REDIS_CLI -e ACL DELUSER "$name" </dev/null >/dev/null
}

# Without an ACL file the users live only until Redis restarts. Any other reason the
# file cannot be written is a failure: a revoked login would be back after a restart.
save_acl() {
	# shellcheck disable=SC2086
	if saved=$($REDIS_CLI -e ACL SAVE </dev/null 2>&1); then
		return 0
	fi
	case $saved in
	*"not configured to use an ACL file"*)
		echo "note: Redis has no aclfile, so this change lasts until it restarts" >&2
		;;
	*)
		echo "operator.sh: ACL SAVE failed: $saved" >&2
		return 1
		;;
	esac
}

# shellcheck disable=SC2086
case $action in
add)
	password=$(cat)
	[ -n "$password" ] || { echo "operator.sh: empty password" >&2; exit 2; }

	# rabbitmqctl does not read a password from a pipe, and its command line can be
	# read by any local user and lands in sudo's log, so it gets a salted hash. The
	# format is RabbitMQ's own: base64(salt + sha256(salt + password)), 4-byte salt.
	salt=$(mktemp)
	trap 'rm -f "$salt"' EXIT
	openssl rand 4 >"$salt"
	hash=$({
		cat "$salt"
		{ cat "$salt"; printf '%s' "$password"; } | openssl dgst -sha256 -binary
	} | openssl base64 -A)
	# A pipeline hides a failed digest, and a hash without one locks the person out.
	[ "${#hash}" -eq 48 ] || { echo "operator.sh: could not hash the password" >&2; exit 1; }
	finished=
	trap '[ -n "$finished" ] || echo "operator.sh: $name may be only partly set up; fix the error above and run add again" >&2; rm -f "$salt"' EXIT
	drop_rabbitmq_user
	$RABBITMQCTL -q add_user "$name" "$hash" --pre-hashed-password </dev/null >/dev/null
	# read covers dead-letter queues only, which is what browsing, requeueing and
	# purging need. write is what republishing needs, and it lets the login publish
	# anything to a module's exchange; the broker does not record it. The dead-letter
	# exchanges are included because a message with no x-death header goes back there.
	$RABBITMQCTL -q set_permissions -p "$RABBITMQ_VHOST" "$name" '^$' '\.events(\.dlx)?$' '\.dlq$' </dev/null
	# management is the least privileged tag that may use the HTTP API.
	$RABBITMQCTL -q set_user_tags "$name" management </dev/null

	# No key access: keys hold session revocations and per-user cache entries. The
	# password goes through standard input, as redis-cli's last argument.
	drop_redis_user
	printf '>%s' "$password" | $REDIS_CLI -e -x ACL SETUSER "$name" reset on resetkeys resetchannels \
		-@all +ping +info +dbsize >/dev/null
	save_acl
	finished=yes
	echo "operator $name: RabbitMQ and Redis logins ready"
	;;
revoke)
	# Each store is tried even when the other fails, so an outage of one never leaves
	# the login in both.
	failed=
	drop_rabbitmq_user || failed="$failed RabbitMQ"
	{ drop_redis_user && save_acl; } || failed="$failed Redis"
	if [ -n "$failed" ]; then
		echo "operator.sh: $name is NOT revoked in:$failed" >&2
		exit 1
	fi
	echo "operator $name: RabbitMQ and Redis logins removed"
	;;
*)
	usage
	;;
esac
