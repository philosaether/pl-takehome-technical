# Canonical benchmark infra (run-cloud-2): a sharded-PG pool, a tuned-PG box, a
# Valkey pool, and split worker/producer runner pools (roles assigned by the
# orchestration script). Both datastores shard by hash(workspace)%N.
# GATED: `terraform apply` is the only spend action — run it when ready, then
# `terraform destroy` (== `make cloud-down`) tears everything down atomically.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.region
}

data "aws_ami" "al2023" {
  most_recent = true
  owners      = ["amazon"]
  filter {
    name   = "name"
    values = ["al2023-ami-*-x86_64"]
  }
  filter {
    name   = "state"
    values = ["available"]
  }
}

resource "aws_key_pair" "plq" {
  key_name   = "plq-bench"
  public_key = var.ssh_public_key
}

resource "aws_security_group" "plq" {
  name        = "plq-bench"
  description = "plq benchmark: SSH from operator, all traffic within the group"

  ingress {
    description = "SSH from operator"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [var.ssh_cidr]
  }
  ingress {
    description = "intra-cluster (postgres, etc.)"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    self        = true
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

# Sharded-PG pool — N independent stock postgres:16 primaries. The router points at
# 1/2/4/8 of them via the pg_addrs_* outputs; postgres×1 = one of these (the stock
# baseline). Mirrors the Valkey pool, so the head-to-head is fair by construction.
resource "aws_instance" "pg" {
  count                  = var.pg_count
  ami                    = data.aws_ami.al2023.id
  instance_type          = var.pg_type
  key_name               = aws_key_pair.plq.key_name
  vpc_security_group_ids = [aws_security_group.plq.id]
  root_block_device {
    volume_size = 20 # the postgres:16 image (LLVM/JIT layers) overflows the ~8 GiB AMI default
  }
  user_data = <<-EOF
    #!/bin/bash
    dnf install -y docker
    systemctl enable --now docker
    docker run -d --name pg --restart always \
      -e POSTGRES_PASSWORD=plq -e POSTGRES_USER=plq -e POSTGRES_DB=plq \
      -p 5432:5432 postgres:16
  EOF
  tags      = { Name = "plq-pg-${count.index + 1}" }
}

# Tuned-PG box — a single primary with server tuning, to preempt "you didn't tune
# PG." Same image, tuned via command flags; --shm-size for the larger shared_buffers.
resource "aws_instance" "pg_tuned" {
  ami                    = data.aws_ami.al2023.id
  instance_type          = var.pg_type
  key_name               = aws_key_pair.plq.key_name
  vpc_security_group_ids = [aws_security_group.plq.id]
  root_block_device {
    volume_size = 20
  }
  user_data = <<-EOF
    #!/bin/bash
    dnf install -y docker
    systemctl enable --now docker
    docker run -d --name pg --restart always --shm-size=2g \
      -e POSTGRES_PASSWORD=plq -e POSTGRES_USER=plq -e POSTGRES_DB=plq \
      -p 5432:5432 postgres:16 \
      -c synchronous_commit=off -c shared_buffers=2GB -c max_connections=400 -c max_wal_size=4GB
  EOF
  tags      = { Name = "plq-pg-tuned" }
}

# Valkey primaries — N independent standalone instances (NOT Cluster; the design's
# `hash(workspace)%N` routing models exactly this). The sweep points the worker at
# 1/2/4 of them via the valkey_addrs_* outputs. Same durability config as compose
# (valkey-work-unit-queue.md §Durability & loss): ≤~1s loss window, never evict.
resource "aws_instance" "valkey" {
  count                  = var.valkey_count
  ami                    = data.aws_ami.al2023.id
  instance_type          = var.valkey_type
  key_name               = aws_key_pair.plq.key_name
  vpc_security_group_ids = [aws_security_group.plq.id]
  user_data              = <<-EOF
    #!/bin/bash
    dnf install -y docker
    systemctl enable --now docker
    docker run -d --name valkey --restart always -p 6379:6379 valkey/valkey:8.1 \
      valkey-server --appendonly yes --appendfsync everysec --aof-use-rdb-preamble yes \
      --maxmemory 512mb --maxmemory-policy noeviction
  EOF
  tags                   = { Name = "plq-valkey-${count.index + 1}" }
}

# Worker runners — each runs `plq loadrun` with PLQ_PRODUCERS=0 (the isolated,
# production-match worker pool, measured against external load). Bigger box so the
# worker never bottlenecks driving valkey×8 (~180k/s). Roles (pg-sharded / pg-tuned /
# valkey) are assigned by the orchestration script. Binaries scp'd in.
resource "aws_instance" "worker_runner" {
  count                  = var.worker_runner_count
  ami                    = data.aws_ami.al2023.id
  instance_type          = var.worker_runner_type
  key_name               = aws_key_pair.plq.key_name
  vpc_security_group_ids = [aws_security_group.plq.id]
  tags                   = { Name = "plq-worker-${count.index + 1}" }
}

# Producer runners — each runs `plq loadgen` continuously against its assigned
# backend. Assigned by the script (pg-sharded ×2, pg-tuned ×1, valkey ×3).
resource "aws_instance" "producer_runner" {
  count                  = var.producer_runner_count
  ami                    = data.aws_ami.al2023.id
  instance_type          = var.producer_runner_type
  key_name               = aws_key_pair.plq.key_name
  vpc_security_group_ids = [aws_security_group.plq.id]
  tags                   = { Name = "plq-producer-${count.index + 1}" }
}
