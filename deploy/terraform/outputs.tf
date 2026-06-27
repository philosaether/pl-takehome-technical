# Shard-sweep connection strings — paste straight into PLQ_POSTGRES_DSN /
# PLQ_VALKEY_ADDR on the worker boxes. _1/_2/_4/_8 map onto the sweep's shard counts.
locals {
  pg_dsns      = [for ip in aws_instance.pg[*].private_ip : "postgres://plq:plq@${ip}:5432/plq?sslmode=disable"]
  valkey_addrs = [for ip in aws_instance.valkey[*].private_ip : "${ip}:6379"]
}

output "pg_addrs_1" { value = join(",", slice(local.pg_dsns, 0, min(1, length(local.pg_dsns)))) }
output "pg_addrs_2" { value = join(",", slice(local.pg_dsns, 0, min(2, length(local.pg_dsns)))) }
output "pg_addrs_4" { value = join(",", slice(local.pg_dsns, 0, min(4, length(local.pg_dsns)))) }
output "pg_addrs_8" { value = join(",", slice(local.pg_dsns, 0, min(8, length(local.pg_dsns)))) }

output "pg_tuned_dsn" {
  value = "postgres://plq:plq@${aws_instance.pg_tuned.private_ip}:5432/plq?sslmode=disable"
}

output "valkey_addrs_1" { value = join(",", slice(local.valkey_addrs, 0, min(1, length(local.valkey_addrs)))) }
output "valkey_addrs_2" { value = join(",", slice(local.valkey_addrs, 0, min(2, length(local.valkey_addrs)))) }
output "valkey_addrs_4" { value = join(",", slice(local.valkey_addrs, 0, min(4, length(local.valkey_addrs)))) }
output "valkey_addrs_8" { value = join(",", slice(local.valkey_addrs, 0, min(8, length(local.valkey_addrs)))) }

# Runner boxes the orchestration script SSHes into + assigns roles.
output "worker_runner_ips" { value = aws_instance.worker_runner[*].public_ip }
output "producer_runner_ips" { value = aws_instance.producer_runner[*].public_ip }

# Valkey box public IPs — the durability tail SSHes valkey[0] to CONFIG SET its
# fsync mode live (the datastores' private IPs aren't reachable from the laptop).
output "valkey_public_ips" { value = aws_instance.valkey[*].public_ip }

# Private IPs for intra-cluster wiring / debugging.
output "pg_private_ips" { value = aws_instance.pg[*].private_ip }
output "valkey_private_ips" { value = aws_instance.valkey[*].private_ip }
output "pg_tuned_private_ip" { value = aws_instance.pg_tuned.private_ip }
