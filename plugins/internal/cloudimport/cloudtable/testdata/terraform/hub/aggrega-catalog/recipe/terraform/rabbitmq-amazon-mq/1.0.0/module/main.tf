resource "aws_mq_broker" "this" {
  broker_name        = var.name
  engine_type        = "RabbitMQ"
  engine_version     = var.engine_version
  host_instance_type = var.instance_type
  deployment_mode    = var.mode
  subnet_ids         = var.subnet_ids

  user {
    username = var.username
  }
}

output "endpoint" {
  value = aws_mq_broker.this.instances[0].endpoints[0]
}
