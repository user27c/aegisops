resource "alicloud_key_pair" "this" {
  key_pair_name = "${var.name_prefix}-operator"
  public_key    = var.ssh_public_key
}

resource "alicloud_security_group" "this" {
  security_group_name = "${var.name_prefix}-sg"
  description         = "Restricted AegisOps demo k3s ingress"
  vpc_id              = var.vpc_id
  tags                = var.tags
}

resource "alicloud_security_group_rule" "ssh" {
  for_each          = var.admin_cidrs
  type              = "ingress"
  ip_protocol       = "tcp"
  nic_type          = "intranet"
  policy            = "accept"
  port_range        = "22/22"
  priority          = 1
  security_group_id = alicloud_security_group.this.id
  cidr_ip           = each.value
  description       = "Restricted SSH management"
}

resource "alicloud_security_group_rule" "k3s_api" {
  for_each          = var.admin_cidrs
  type              = "ingress"
  ip_protocol       = "tcp"
  nic_type          = "intranet"
  policy            = "accept"
  port_range        = "6443/6443"
  priority          = 1
  security_group_id = alicloud_security_group.this.id
  cidr_ip           = each.value
  description       = "Restricted k3s API management"
}

resource "alicloud_security_group_rule" "http" {
  for_each          = var.public_web_cidrs
  type              = "ingress"
  ip_protocol       = "tcp"
  nic_type          = "intranet"
  policy            = "accept"
  port_range        = "80/80"
  priority          = 1
  security_group_id = alicloud_security_group.this.id
  cidr_ip           = each.value
  description       = "Optional HTTP demo ingress"
}

resource "alicloud_security_group_rule" "https" {
  for_each          = var.public_web_cidrs
  type              = "ingress"
  ip_protocol       = "tcp"
  nic_type          = "intranet"
  policy            = "accept"
  port_range        = "443/443"
  priority          = 1
  security_group_id = alicloud_security_group.this.id
  cidr_ip           = each.value
  description       = "Optional HTTPS demo ingress"
}

resource "alicloud_security_group_rule" "egress" {
  type              = "egress"
  ip_protocol       = "all"
  nic_type          = "intranet"
  policy            = "accept"
  port_range        = "-1/-1"
  priority          = 1
  security_group_id = alicloud_security_group.this.id
  cidr_ip           = "0.0.0.0/0"
  description       = "Required package, registry, telemetry and LLM egress"
}

resource "alicloud_instance" "this" {
  instance_name              = "${var.name_prefix}-k3s"
  instance_type              = var.instance_type
  image_id                   = var.image_id
  vswitch_id                 = var.vswitch_id
  security_groups            = [alicloud_security_group.this.id]
  key_name                   = alicloud_key_pair.this.key_pair_name
  instance_charge_type       = "PostPaid"
  internet_charge_type       = var.internet_charge_type
  internet_max_bandwidth_out = var.internet_max_bandwidth_out
  system_disk_category       = var.system_disk_category
  system_disk_size           = var.system_disk_size
  auto_release_time          = var.auto_release_time == "" ? null : var.auto_release_time
  user_data                  = var.user_data
  tags                       = var.tags
  volume_tags                = var.tags

  lifecycle {
    precondition {
      condition     = length(var.admin_cidrs) > 0 && !contains(var.admin_cidrs, "0.0.0.0/0")
      error_message = "SSH/k3s API 管理 CIDR 不能为空且禁止 0.0.0.0/0。"
    }
  }
}
