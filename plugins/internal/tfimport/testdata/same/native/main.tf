terraform {
  required_version = ">= 1.6"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.region
}

variable "region" {
  description = "where the queue runs"
  default     = "eu-west-1"
}

variable "extra_ports" {
  default = [8080, 8443]
}

locals {
  name = "orders-${var.region}"
}

resource "aws_security_group" "web" {
  name        = local.name
  description = "web tier"

  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["10.0.0.0/8", "172.16.0.0/12"]
  }

  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["10.0.0.0/8"]
  }

  egress {
    from_port = 0
    to_port   = 0
    protocol  = "-1"
  }

  tags = {
    Name = "web"
    "Tier" = "edge"
  }

  dynamic "ingress" {
    for_each = var.extra_ports
    content {
      from_port = ingress.value
      to_port   = ingress.value
      protocol  = "tcp"
    }
  }

  dynamic "egress" {
    for_each = var.extra_ports
    content {
      from_port = egress.value
      to_port   = egress.value
      protocol  = "udp"
    }
  }
}

data "aws_vpc" "main" {
  default = true
}

module "queue" {
  source = "./modules/queue"
  name   = local.name
}

output "group_id" {
  value = aws_security_group.web.id
}
