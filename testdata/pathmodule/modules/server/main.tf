variable "env" {}

locals {
  user_data = templatefile("${path.module}/../../templates/${var.env}/user_data.sh", {})
}
