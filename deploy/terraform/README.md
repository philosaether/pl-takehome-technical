# Canonical benchmark infra (GATED)

Provisions the head-to-head run: **worker** (`m5.xlarge`, alone =
production-match), **pg** (`m5.xlarge`, stock `postgres:16`), **producer**
(`m5.large`), and **valkey** (`var.valkey_count` × `m5.xlarge`, N independent
primaries with the compose durability config). `terraform destroy` is the spend
control — the only real cost is instance *uptime*, so tear down right after.

> **This is the gated step.** `terraform apply` is the only spend + irreversible
> action in the project. Everything else (loadgen, proofs, look-ahead bench) is
> verified locally against Docker Postgres + Valkey first.

**Spend note.** The default adds 4× `m5.xlarge` for Valkey on top of the 3 PG-path
boxes — still ≈$1 for a sub-hour run torn down promptly, but it's the bulk of the
cost. For a PG-vs-1-shard baseline only, set `TF_VAR_valkey_count=1`.

## Run it (PG ↔ Valkey head-to-head)

```sh
export TF_VAR_ssh_public_key="$(cat ~/.ssh/id_ed25519.pub)"
export TF_VAR_ssh_cidr="$(curl -s ifconfig.me)/32"

make cloud-up                      # terraform init + apply  → prints the IPs + DSN + valkey addrs
cd deploy/terraform
DSN=$(terraform output -raw dsn_from_cluster)
V1=$(terraform output -raw valkey_addrs_1)   # one primary
V2=$(terraform output -raw valkey_addrs_2)   # two
V4=$(terraform output -raw valkey_addrs_4)   # four
cd -

# build BOTH static binaries (one per build tag) and ship them to the worker box
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags postgres -o plq-postgres ./cmd/plq
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags valkey   -o plq-valkey   ./cmd/plq
scp plq-postgres plq-valkey ec2-user@<worker_ip>:
scp plq-postgres            ec2-user@<producer_ip>:

# worker box: PG sweep, then the Valkey 1/2/4-shard sweep — all appending to one sweep.csv
#   for n in 1 10 100 1000; for p in zero cost; do PLQ_WORKERS=$n PLQ_PROCESS=$p \
#     PLQ_BACKEND=postgres PLQ_POSTGRES_DSN=$DSN ./plq-postgres loadrun; done
#   for addrs in $V1 $V2 $V4; do for n in 1 10 100 1000; for p in zero cost; do \
#     PLQ_WORKERS=$n PLQ_PROCESS=$p PLQ_BACKEND=valkey PLQ_VALKEY_ADDR=$addrs ./plq-valkey loadrun; done
# (the producer box pushes load against whichever store the worker is hitting)
# pull results/sweep.csv back, then: python3 scripts/plot.py results
#   → throughput.png / latency.png overlay postgres vs valkey×1/×2/×4

make cloud-down                    # terraform destroy — DO NOT SKIP
```
