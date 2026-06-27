#!/usr/bin/env bash
# Runs on a producer box: continuous Zipfian producers against one backend, until
# killed (the coordinator starts this with nohup and pkills it between shard counts).
#
#   producers.sh <postgres|valkey> <conn> [producers]
#
# conn is a comma-separated DSN list (postgres) or addr list (valkey) — the SAME
# list the matching worker box uses, so producers and worker route workspaces to the
# same shards.
set -euo pipefail
backend="${1:?usage: producers.sh <postgres|valkey> <conn> [producers]}"
conn="${2:?missing conn}"
producers="${3:-256}"
case "$backend" in
  postgres) connvar="PLQ_POSTGRES_DSN" ;;
  valkey)   connvar="PLQ_VALKEY_ADDR" ;;
  *) echo "unknown backend: $backend" >&2; exit 1 ;;
esac
exec env PLQ_BACKEND="$backend" "$connvar=$conn" PLQ_PRODUCERS="$producers" \
  "./plq-$backend" loadgen
