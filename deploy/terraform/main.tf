# Canonical benchmark infra: worker box (alone), PG box, producer box.
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

# PG box — runs the stock postgres:16 container via user-data.
resource "aws_instance" "pg" {
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
  tags      = { Name = "plq-pg" }
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

# Worker box — runs `plq worker` ALONE (production-match). Static binary scp'd in.
resource "aws_instance" "worker" {
  ami                    = data.aws_ami.al2023.id
  instance_type          = var.worker_type
  key_name               = aws_key_pair.plq.key_name
  vpc_security_group_ids = [aws_security_group.plq.id]
  tags                   = { Name = "plq-worker" }
}

# Producer box — runs `plq loadgen`.
resource "aws_instance" "producer" {
  ami                    = data.aws_ami.al2023.id
  instance_type          = var.producer_type
  key_name               = aws_key_pair.plq.key_name
  vpc_security_group_ids = [aws_security_group.plq.id]
  tags                   = { Name = "plq-producer" }
}
