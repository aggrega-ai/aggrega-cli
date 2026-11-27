variable "name" {
  type = string
}

variable "engine_version" {
  type = string
}

variable "instance_type" {
  type = string
}

variable "mode" {
  type = string
}

variable "subnet_ids" {
  type = list(string)
}

variable "username" {
  type = string
}
