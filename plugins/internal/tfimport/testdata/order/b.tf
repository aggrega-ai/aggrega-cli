variable "retention" {
  default = 86400
}

resource "aws_sqs_queue" "jobs" {
  name                      = var.name
  message_retention_seconds = var.retention
}

variable "name" {
  default = "jobs"
}
