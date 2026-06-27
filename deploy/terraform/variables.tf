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

variable "worker_type" {
  type    = string
  default = "m5.xlarge" # plausible EKS general-purpose node; runs the worker pool alone
}

variable "pg_type" {
  type    = string
  default = "m5.xlarge"
}

variable "producer_type" {
  type    = string
  default = "m5.large"
}

variable "valkey_count" {
  type        = number
  default     = 4 # N independent primaries; the sweep uses 1/2/4 of them. Set 1 for a baseline-only run.
  description = "Number of standalone Valkey primaries for the shard sweep"
}

variable "valkey_type" {
  type    = string
  default = "m5.xlarge" # = pg_type, for a fair per-primary comparison vs the single PG box
}
