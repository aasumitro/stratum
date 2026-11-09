#!/bin/sh
# Proves a dump can actually be restored, by restoring it into a scratch database next to the live
# one and checking the result. The live database is only read. The checks show integrity (migration
# version, schema, seeded rows), not row-for-row equality.
#
# Usage: restore-verify.sh <dump file>
set -u

FILE=${1:-}
COMPOSE=${COMPOSE:-podman compose}
COMPOSE_FILE=${COMPOSE_FILE:-deploy/docker-compose.local.yml}
SCRATCH=stratum_restore_verify
LIVE=stratum

if [ ! -f "$FILE" ]; then
	echo "no such dump file: $FILE" >&2
	exit 1
fi

in_postgres() {
	# Word splitting on COMPOSE is intended: it holds a command and its subcommand.
	# shellcheck disable=SC2086
	$COMPOSE -f "$COMPOSE_FILE" exec -T postgres "$@"
}

sql() {
	database=$1
	shift
	# A check against a failed restore errors by design, and reports its own FAIL line.
	in_postgres psql -v ON_ERROR_STOP=1 -U stratum -d "$database" -Atc "$1" 2>/dev/null
}

drop_scratch() {
	sql "$LIVE" "DROP DATABASE IF EXISTS $SCRATCH" >/dev/null
}
trap drop_scratch EXIT

failed=0
report() {
	if [ "$2" = "pass" ]; then
		echo "ok   $1"
	else
		echo "FAIL $1"
		failed=1
	fi
}

drop_scratch
if ! sql "$LIVE" "CREATE DATABASE $SCRATCH" >/dev/null; then
	echo "could not create the scratch database" >&2
	echo "RESULT: FAIL"
	exit 1
fi

# With no --exit-on-error, pg_restore carries on past an error and still exits non-zero at the end,
# so a partial restore fails this check.
if in_postgres pg_restore --no-owner -U stratum -d "$SCRATCH" <"$FILE"; then
	report "pg_restore exits 0" pass
else
	report "pg_restore exits 0" fail
fi

restored_version=$(sql "$SCRATCH" "SELECT version FROM schema_migrations")
live_version=$(sql "$LIVE" "SELECT version FROM schema_migrations")
if [ -n "$restored_version" ] && [ "$restored_version" = "$live_version" ]; then
	report "schema_migrations.version matches the live database ($live_version)" pass
else
	report "schema_migrations.version matches the live database (restored '$restored_version', live '$live_version')" fail
fi

for table in ref.countries ref.currencies billing.plans; do
	rows=$(sql "$SCRATCH" "SELECT count(*) FROM $table")
	if [ -n "$rows" ] && [ "$rows" -ge 1 ]; then
		report "$table has rows ($rows)" pass
	else
		report "$table has rows" fail
	fi
done

if [ "$failed" -eq 0 ]; then
	echo "RESULT: PASS"
	exit 0
fi
echo "RESULT: FAIL"
exit 1
