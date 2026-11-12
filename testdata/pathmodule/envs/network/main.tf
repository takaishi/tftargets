terraform {}

variable "policy_path" {}

locals {
  policy = file(var.policy_path)
}
