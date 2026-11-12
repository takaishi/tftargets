terraform {}

module "server" {
  source = "../../modules/server"
  env    = "prod"
}
