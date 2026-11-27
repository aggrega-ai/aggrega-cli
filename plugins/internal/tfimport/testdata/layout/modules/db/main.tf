variable "name" {}

resource "aws_db_instance" "this" {
  identifier = var.name
  engine     = "postgres"
}
