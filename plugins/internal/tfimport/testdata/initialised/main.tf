module "db" {
  source  = "terraform-aws-modules/rds/aws"
  version = "~> 6.0"

  identifier = "orders"
}

resource "aws_sqs_queue" "jobs" {
  name = "jobs"
}
