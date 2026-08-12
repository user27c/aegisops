# Credentials are deliberately omitted. The provider reads a short-lived local
# credential chain (for example RAM role, environment, or Alibaba CLI profile).
provider "alicloud" {
  region = var.region
}
