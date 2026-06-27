variable "region" {
  type    = string
  default = "us-east-1"
}

variable "ssh_public_key" {
  type        = string
  description = "SSH public key for the bench boxes (e.g. file(\"~/.ssh/id_ed25519.pub\"))"
}

variable "ssh_cidr" {
  type        = string
  description = "CIDR allowed to SSH in (your IP/32)"
}

variable "pg_type" {
  type    = string
  default = "m5.xlarge"
}

variable "pg_count" {
  type        = number
  default     = 8 # the sharded-PG pool; the sweep uses 1/2/4/8 of them
  description = "Number of standalone Postgres primaries for the shard sweep"
}

variable "valkey_count" {
  type        = number
  default     = 8 # N independent primaries; the sweep uses 1/2/4/8 of them
  description = "Number of standalone Valkey primaries for the shard sweep"
}

variable "valkey_type" {
  type    = string
  default = "m5.xlarge" # = pg_type, for a fair per-primary comparison
}

variable "worker_runner_count" {
  type        = number
  default     = 3 # pg-sharded-worker, pg-tuned-worker, valkey-worker
  description = "Worker runner boxes (run plq loadrun, PLQ_PRODUCERS=0)"
}

variable "worker_runner_type" {
  type    = string
  default = "m5.2xlarge" # bigger so the worker never bottlenecks driving valkey×8 (~180k/s)
}

variable "producer_runner_count" {
  type        = number
  default     = 6 # pg-sharded ×2, pg-tuned ×1, valkey ×3
  description = "Producer runner boxes (run plq loadgen continuously)"
}

variable "producer_runner_type" {
  type    = string
  default = "m5.xlarge"
}
