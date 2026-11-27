resource "aws_sqs_queue" "jobs" {
  name                       = "jobs"
  visibility_timeout_seconds =
}
