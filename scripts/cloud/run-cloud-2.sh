#!/usr/bin/env bash
# Coordinator for the run-cloud-2 head-to-head (runs on the laptop). Assumes the
# boxes are already provisioned (`make cloud-up` with TF_VAR_pg_count=8
# TF_VAR_valkey_count=8) and AWS_PROFILE is set. Builds both binaries, distributes
# them + the per-box scripts, runs the three tracks in parallel (PG-sharded ‖ Valkey
# ‖ PG-tuned), runs the durability tail, then merges every worker's sweep.csv into
# results/run-cloud-2/ and graphs (faceted by process model).
#
#   AWS_PROFILE=praxis scripts/cloud/run-cloud-2.sh
#
# GATED upstream: the spend is the `make cloud-up` that precedes this. This script
# only drives the already-running boxes; `make cloud-down` tears them down after.
set -euo pipefail

TFDIR=deploy/terraform
SSH="ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=15 -o ServerAliveInterval=30 -o ServerAliveCountMax=4"
USER=ec2-user
OUT=results/run-cloud-2
mkdir -p "$OUT"

tf() { terraform -chdir="$TFDIR" output -raw "$1"; }
tflist() { terraform -chdir="$TFDIR" output -json "$1" | tr -d '[]" ' | tr ',' '\n' | grep .; }

echo "=== reading terraform outputs ==="
mapfile -t WORKERS < <(tflist worker_runner_ips)     # [0]=pg-sharded [1]=pg-tuned [2]=valkey
mapfile -t PRODUCERS < <(tflist producer_runner_ips) # [0,1]=pg-sharded [2]=pg-tuned [3,4,5]=valkey
PG1=$(tf pg_addrs_1);  PG2=$(tf pg_addrs_2);  PG4=$(tf pg_addrs_4);  PG8=$(tf pg_addrs_8)
PGT=$(tf pg_tuned_dsn)
V1=$(tf valkey_addrs_1); V2=$(tf valkey_addrs_2); V4=$(tf valkey_addrs_4); V8=$(tf valkey_addrs_8)
mapfile -t PG_PRIV  < <(tflist pg_private_ips)
mapfile -t VAL_PRIV < <(tflist valkey_private_ips)
PGT_PRIV=$(tf pg_tuned_private_ip)

W_PGSH=${WORKERS[0]}; W_PGT=${WORKERS[1]}; W_VAL=${WORKERS[2]}
# Producer assignment adapts to the pool size: 6 (full run) → pg-sharded 2 / tuned 1 /
# valkey 3; 4 (quota-constrained run) → pg-sharded 1 / tuned 1 / valkey 2 (valkey is
# the fast one, so it gets the spare producer either way).
if [ "${#PRODUCERS[@]}" -ge 6 ]; then
  P_PGSH="${PRODUCERS[0]},${PRODUCERS[1]}"; P_PGT="${PRODUCERS[2]}"; P_VAL="${PRODUCERS[3]},${PRODUCERS[4]},${PRODUCERS[5]}"
else
  P_PGSH="${PRODUCERS[0]}"; P_PGT="${PRODUCERS[1]}"; P_VAL="${PRODUCERS[2]},${PRODUCERS[3]}"
fi

echo "=== building static linux binaries ==="
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags postgres -o /tmp/plq-postgres ./cmd/plq
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags valkey   -o /tmp/plq-valkey   ./cmd/plq

ship() { # ship <ip> <backend-binary>
  scp -q -o StrictHostKeyChecking=accept-new \
    "/tmp/plq-$2" scripts/cloud/subsweep.sh scripts/cloud/producers.sh "$USER@$1:" 2>/dev/null
  $SSH "$USER@$1" "chmod +x plq-$2 subsweep.sh producers.sh"
}
wait_ssh() { # block until SSH answers on each box (fresh instances boot ~30s)
  for ip in "$@"; do
    for i in $(seq 1 40); do $SSH "$USER@$ip" true 2>/dev/null && break; sleep 5; done
  done
}
wait_ports() { # wait_ports <worker_ip> <host:port...> — all open, polled from the worker box
  local wip=$1; shift
  $SSH "$USER@$wip" "for i in \$(seq 1 72); do ok=1; for hp in $*; do h=\${hp%:*}; p=\${hp#*:}; (exec 3<>/dev/tcp/\$h/\$p) 2>/dev/null || ok=0; done; [ \$ok = 1 ] && exit 0; sleep 5; done; exit 1"
}

echo "=== waiting for runner SSH ==="
wait_ssh "${WORKERS[@]}" "${PRODUCERS[@]}"

echo "=== distributing binaries + scripts ==="
for ip in "$W_PGSH" "$W_PGT" "${PRODUCERS[0]}" "${PRODUCERS[1]}" "${PRODUCERS[2]}"; do ship "$ip" postgres; done
for ip in "$W_VAL" "${PRODUCERS[3]}" "${PRODUCERS[4]}" "${PRODUCERS[5]}"; do ship "$ip" valkey; done

echo "=== waiting for datastores (up to ~6min; postgres:16 pulls on fresh boxes) ==="
wait_ports "$W_PGSH" "$(printf '%s:5432 ' "${PG_PRIV[@]}")"  && echo "  PG pool ready"
wait_ports "$W_VAL"  "$(printf '%s:6379 ' "${VAL_PRIV[@]}")" && echo "  valkey pool ready"
wait_ports "$W_PGT"  "$PGT_PRIV:5432"                        && echo "  tuned PG ready"

connvar() { [ "$1" = postgres ] && echo PLQ_POSTGRES_DSN || echo PLQ_VALKEY_ADDR; }

# run_track <backend> <label> <worker_ip> <producer_ips_csv> <conn...>
# Per shard count: reset → start producers → prime → sub-sweep → stop producers.
run_track() {
  local backend=$1 label=$2 wip=$3 pcsv=$4; shift 4
  local cvar; cvar=$(connvar "$backend")
  IFS=, read -ra pips <<< "$pcsv"
  local last=""
  for conn in "$@"; do
    [ "$conn" = "$last" ] && continue  # pool < requested shard count → skip the dup (e.g. ×8==×4 at 4 boxes)
    last="$conn"
    local shards; shards=$(echo "$conn" | tr ',' '\n' | grep -c .)
    echo "--- [$label] shards=$shards: reset → producers → sweep ---"
    $SSH "$USER@$wip" "env PLQ_BACKEND=$backend $cvar='$conn' ./plq-$backend reset"
    for pip in "${pips[@]}"; do
      $SSH "$USER@$pip" "pkill -f 'plq-' || true; nohup ./producers.sh $backend '$conn' 256 >producers.log 2>&1 &"
    done
    $SSH "$USER@$wip" "sleep 10"  # prime the queue past the worker's appetite
    $SSH "$USER@$wip" "./subsweep.sh $backend $label '$conn'"
    for pip in "${pips[@]}"; do $SSH "$USER@$pip" "pkill -f 'plq-' || true"; done
  done
}

echo "=== launching 3 tracks in parallel ==="
run_track postgres postgres       "$W_PGSH" "$P_PGSH" "$PG1" "$PG2" "$PG4" "$PG8" &
run_track valkey   valkey         "$W_VAL"  "$P_VAL"  "$V1"  "$V2"  "$V4"  "$V8"  &
run_track postgres postgres-tuned "$W_PGT"  "$P_PGT"  "$PGT" &
wait
echo "=== all tracks done ==="

echo "=== durability tail (valkey×1, process=0, fsync off/everysec/always) ==="
# Reconfigure the single primary live between runs; write to a separate dir → durability.csv.
DUR_ADDR="$V1"; DUR_HOST="${DUR_ADDR%%:*}"
for mode in off everysec always; do
  case "$mode" in
    off)      cfg='CONFIG SET appendonly no' ;;
    everysec) cfg='CONFIG SET appendonly yes; CONFIG SET appendfsync everysec' ;;
    always)   cfg='CONFIG SET appendonly yes; CONFIG SET appendfsync always' ;;
  esac
  $SSH "$USER@$W_VAL" "docker exec valkey valkey-cli $cfg" 2>/dev/null || \
    $SSH "$USER@$DUR_HOST" "docker exec valkey valkey-cli $cfg" || true
  $SSH "$USER@$W_VAL" "pkill -f 'plq-' || true; env PLQ_BACKEND=valkey PLQ_VALKEY_ADDR='$DUR_ADDR' ./plq-valkey reset"
  $SSH "$USER@${PRODUCERS[3]}" "nohup ./producers.sh valkey '$DUR_ADDR' 256 >producers.log 2>&1 &"
  $SSH "$USER@$W_VAL" "sleep 10; ./subsweep.sh valkey valkey-$mode '$DUR_ADDR' '100 1000' 'zero' 20s 8s"
  $SSH "$USER@${PRODUCERS[3]}" "pkill -f 'plq-' || true"
  # move this mode's rows into the durability sample area on the worker box
  $SSH "$USER@$W_VAL" "mkdir -p durresults && mv results/sweep.csv durresults/sweep-$mode.csv 2>/dev/null || true"
done

echo "=== merge + graph ==="
merge() { # merge <ip> <remote-path>  → append rows (header once) to $1-collected
  $SSH "$USER@$1" "cat $2" 2>/dev/null
}
{ # main sweep.csv: header from the first worker, then all data rows
  merge "$W_PGSH" results/sweep.csv | head -1
  for ip in "$W_PGSH" "$W_VAL" "$W_PGT"; do merge "$ip" results/sweep.csv | tail -n +2; done
} > "$OUT/sweep.csv"
{ # durability.csv: same header, rows from each mode file
  merge "$W_VAL" durresults/sweep-off.csv | head -1
  for m in off everysec always; do merge "$W_VAL" "durresults/sweep-$m.csv" | tail -n +2; done
} > "$OUT/durability.csv"

python3 scripts/plot.py "$OUT"
echo "=== done — results in $OUT/ ==="
echo "Remember: make cloud-down"
