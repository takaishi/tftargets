terraform {}

locals {
  tags = jsondecode(file("${path.module}/../../data/tags.json"))
}
