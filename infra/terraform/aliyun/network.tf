module "network" {
  source       = "./modules/network"
  name_prefix  = local.name_prefix
  zone_id      = var.zone_id
  vpc_cidr     = var.vpc_cidr
  vswitch_cidr = var.vswitch_cidr
  tags         = local.tags
}
