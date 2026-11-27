resource "aws_sqs_queue" "jobs" {
  name                      = "jobs"
  message_retention_seconds = var.retention
}

variable "retention" {
  default = 86400
}
