#!/usr/bin/env bash
# Runs on a worker box: the isolated-worker sub-sweep for ONE shard count. Loops
# process × workers, each a `loadrun` with PLQ_PRODUCERS=0 (measure the worker pool
# against the external loadgen) and PLQ_RESET=false (don't wipe the producers' queue).
# Appends rows to results/sweep.csv tagged with the series label.
#
#   subsweep.sh <postgres|valkey> <label> <conn> [workers] [procs] [dur] [warmup]
#
#   backend  postgres|valkey         — selects the binary + conn env var
#   label    series key in the CSV   — e.g. postgres, postgres-tuned, valkey
#   conn     DSN/addr list for this shard count (shards = its comma-count)
#
# The queue must already be primed by producers.sh on this conn before calling.
set -euo pipefail
backend="${1:?usage: subsweep.sh <backend> <label> <conn> ...}"
label="${2:?missing label}"
conn="${3:?missing conn}"
workers="${4:-1 10 30 100 300 1000}"
procs="${5:-zero 2ms 20ms 200ms}"
dur="${6:-20s}"
warm="${7:-8s}"
case "$backend" in
  postgres) connvar="PLQ_POSTGRES_DSN" ;;
  valkey)   connvar="PLQ_VALKEY_ADDR" ;;
  *) echo "unknown backend: $backend" >&2; exit 1 ;;
esac
shards=$(echo "$conn" | tr ',' '\n' | grep -c .)
mkdir -p results
for p in $procs; do
  if [ "$p" = zero ]; then pm=(PLQ_PROCESS=zero); else pm=(PLQ_PROCESS=cost PLQ_PROCESS_BASE="$p"); fi
  for w in $workers; do
    echo ">>> $label shards=$shards workers=$w process=$p"
    env PLQ_BACKEND="$label" "$connvar=$conn" PLQ_WORKERS="$w" "${pm[@]}" \
      PLQ_PRODUCERS=0 PLQ_RESET=false PLQ_DURATION="$dur" PLQ_WARMUP="$warm" \
      PLQ_RESULTS=./results "./plq-$backend" loadrun
  done
done
