output "worker_public_ip" { value = aws_instance.worker.public_ip }
output "producer_public_ip" { value = aws_instance.producer.public_ip }
output "pg_public_ip" { value = aws_instance.pg.public_ip }
output "pg_private_ip" { value = aws_instance.pg.private_ip }

output "dsn_from_cluster" {
  description = "DSN the worker/producer boxes use (PG private IP)"
  value       = "postgres://plq:plq@${aws_instance.pg.private_ip}:5432/plq?sslmode=disable"
}

# Valkey shard-sweep addr strings — paste straight into PLQ_VALKEY_ADDR on the
# worker box. _1/_2/_4 map 1:1 onto the local VALKEY_ADDRS_SWEEP entries.
locals {
  valkey_addrs = [for ip in aws_instance.valkey[*].private_ip : "${ip}:6379"]
}

output "valkey_private_ips" { value = aws_instance.valkey[*].private_ip }
output "valkey_addrs_1" { value = join(",", slice(local.valkey_addrs, 0, min(1, length(local.valkey_addrs)))) }
output "valkey_addrs_2" { value = join(",", slice(local.valkey_addrs, 0, min(2, length(local.valkey_addrs)))) }
output "valkey_addrs_4" { value = join(",", slice(local.valkey_addrs, 0, min(4, length(local.valkey_addrs)))) }
