variable "identifier" {}

resource "aws_db_instance" "this" {
  identifier = var.identifier
  engine     = "postgres"
}

resource "aws_db_parameter_group" "this" {
  name   = var.identifier
  family = "postgres16"
}
