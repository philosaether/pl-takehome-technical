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
  user_data              = <<-EOF
    #!/bin/bash
    dnf install -y docker
    systemctl enable --now docker
    docker run -d --name pg --restart always \
      -e POSTGRES_PASSWORD=plq -e POSTGRES_USER=plq -e POSTGRES_DB=plq \
      -p 5432:5432 postgres:16
  EOF
  tags                   = { Name = "plq-pg" }
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
