resource "alicloud_vpc" "this" {
  vpc_name   = "${var.name_prefix}-vpc"
  cidr_block = var.vpc_cidr
  tags       = var.tags
}

resource "alicloud_vswitch" "this" {
  vswitch_name = "${var.name_prefix}-vswitch"
  vpc_id       = alicloud_vpc.this.id
  zone_id      = var.zone_id
  cidr_block   = var.vswitch_cidr
  tags         = var.tags
}
