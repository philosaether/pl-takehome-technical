output "worker_public_ip" { value = aws_instance.worker.public_ip }
output "producer_public_ip" { value = aws_instance.producer.public_ip }
output "pg_public_ip" { value = aws_instance.pg.public_ip }
output "pg_private_ip" { value = aws_instance.pg.private_ip }

output "dsn_from_cluster" {
  description = "DSN the worker/producer boxes use (PG private IP)"
  value       = "postgres://plq:plq@${aws_instance.pg.private_ip}:5432/plq?sslmode=disable"
}
