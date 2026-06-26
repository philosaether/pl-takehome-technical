# Canonical benchmark infra (GATED)

Provisions the 3-box canonical run: **worker** (`m5.xlarge`, alone =
production-match), **pg** (`m5.xlarge`, stock `postgres:16`), **producer**
(`m5.large`). `terraform destroy` is the spend control — the only real cost is
instance *uptime*, so tear down right after.

> **This is the gated step.** `terraform apply` is the only spend + irreversible
> action in the project. Everything else (loadgen, proofs, look-ahead bench) is
> verified locally against a Docker Postgres first.

## Run it

```sh
export TF_VAR_ssh_public_key="$(cat ~/.ssh/id_ed25519.pub)"
export TF_VAR_ssh_cidr="$(curl -s ifconfig.me)/32"

make cloud-up                      # terraform init + apply  → prints the IPs + DSN
DSN=$(terraform -chdir=deploy/terraform output -raw dsn_from_cluster)

# build the static binary locally and ship it to the worker + producer boxes
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags postgres -o plq ./cmd/plq
scp plq ec2-user@<worker_ip>:    ;  scp plq ec2-user@<producer_ip>:

# producer box: push load
ssh ec2-user@<producer_ip> "PLQ_BACKEND=postgres PLQ_POSTGRES_DSN=$DSN PLQ_PRODUCERS=64 ./plq loadgen"

# worker box: run the sweep (it owns the headline metrics + self-certifies saturation)
#   for n in 1 10 100 1000; for p in zero cost; do PLQ_WORKERS=$n PLQ_PROCESS=$p ./plq loadrun; done
# pull results/sweep.csv back, then: python3 scripts/plot.py results

make cloud-down                    # terraform destroy — DO NOT SKIP
```
