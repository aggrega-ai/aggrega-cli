terraform {
  backend "s3" {
    bucket = "state"
    key    = "orders.tfstate"
  }
}

resource "aws_sqs_queue" "jobs" {
  name = "jobs"
}

moved {
  from = aws_sqs_queue.old
  to   = aws_sqs_queue.jobs
}

import {
  to = aws_sqs_queue.jobs
  id = "https://sqs.example/jobs"
}

removed {
  from = aws_sqs_queue.legacy
}

check "health" {
  assert {
    condition     = aws_sqs_queue.jobs.name != ""
    error_message = "the queue has no name"
  }
}

ephemeral "random_password" "db" {
  length = 16
}
