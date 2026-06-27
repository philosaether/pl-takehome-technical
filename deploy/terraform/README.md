# Canonical benchmark infra (GATED) — run-cloud-2

Provisions the run-cloud-2 head-to-head: a **sharded-PG pool** (`var.pg_count` ×
`m5.xlarge`, stock `postgres:16`), a **tuned-PG** box (`m5.xlarge`), a **Valkey
pool** (`var.valkey_count` × `m5.xlarge`, compose durability config), and split
runner pools (**3× `m5.2xlarge` workers** + **6× `m5.xlarge` producers**). Both
datastores shard by `hash(workspace)%N`, so the comparison isolates per-primary
engine speed from horizontal scaling. `terraform destroy` is the spend control —
the only real cost is instance *uptime*, so tear down right after.

> **This is the gated step.** `terraform apply` is the only spend + irreversible
> action. The driver (incl. the multi-DSN PG router) is verified by conformance
> locally first; the isolated topology is dry-run locally too.

**Spend note.** Default = 28 resources (8 PG + 1 tuned + 8 valkey + 9 runners +
key + SG) ≈ **\$6–7** for a ~1 hr run torn down promptly. For a smaller run set
`TF_VAR_pg_count` / `TF_VAR_valkey_count` lower (e.g. =1 for a baseline only).

## Run it

```sh
export TF_VAR_ssh_public_key="$(cat ~/.ssh/id_ed25519.pub)"
export TF_VAR_ssh_cidr="$(curl -s ifconfig.me)/32"
export AWS_PROFILE=praxis            # the profile with EC2 perms (terraform-admin)

make cloud-up                        # terraform init + apply → 28 resources

scripts/cloud/run-cloud-2.sh         # builds binaries, distributes them, runs the
                                     # 3 tracks (PG-sharded ‖ Valkey ‖ PG-tuned) +
                                     # the durability tail, merges → results/run-cloud-2/,
                                     # graphs (faceted by process model)

make cloud-down                      # terraform destroy — DO NOT SKIP
```

The coordinator reads the `pg_addrs_{1,2,4,8}` / `pg_tuned_dsn` /
`valkey_addrs_{1,2,4,8}` / `*_runner_ips` outputs, assigns runner roles, and per
shard count: resets the shards, starts `producers.sh` on the producer box(es),
runs `subsweep.sh` (isolated worker: `PLQ_PRODUCERS=0 PLQ_RESET=false`) on the
worker box, then stops the producers. Output: `results/run-cloud-2/sweep.csv` +
`durability.csv` + per-process `throughput-*.png` / `latency-*.png`.

**Saturation protocol:** any point logged `saturated=false` was producer-bound —
bump `producers.sh`'s count (or add a producer box) and re-run that shard count.
