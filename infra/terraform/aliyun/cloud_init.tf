locals {
  cloud_init = templatefile("${path.module}/cloud-init.yaml.tftpl", {
    admin_cidrs    = sort(tolist(var.admin_cidrs))
    k3s_version    = var.k3s_version
    ssh_public_key = trimspace(file(var.ssh_public_key_path))
  })
}
