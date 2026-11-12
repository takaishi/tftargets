terraform {}

locals {
  sources = fileset("${path.module}/../../src/lambda", "**")
}
