module "k3s_node" {
  source                     = "./modules/k3s-node"
  name_prefix                = local.name_prefix
  vpc_id                     = module.network.vpc_id
  vswitch_id                 = module.network.vswitch_id
  instance_type              = var.instance_type
  image_id                   = var.image_id
  system_disk_category       = var.system_disk_category
  system_disk_size           = var.system_disk_size
  internet_charge_type       = var.internet_charge_type
  internet_max_bandwidth_out = var.internet_max_bandwidth_out
  ssh_public_key             = trimspace(file(var.ssh_public_key_path))
  admin_cidrs                = var.admin_cidrs
  public_web_cidrs           = var.public_web_cidrs
  auto_release_time          = var.auto_release_time
  user_data                  = base64encode(local.cloud_init)
  tags                       = local.tags
}
