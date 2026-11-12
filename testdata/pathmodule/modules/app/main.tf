locals {
  config = templatefile("${path.module}/../shared/config.tpl", { value = "x" })
}
