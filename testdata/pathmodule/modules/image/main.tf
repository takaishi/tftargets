locals {
  dockerfile_hash = filemd5("${path.module}/../../docker/base/Dockerfile")
}
