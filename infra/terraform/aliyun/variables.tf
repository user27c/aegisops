variable "region" {
  type        = string
  description = "Alibaba Cloud region, for example cn-hangzhou."
  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region 不能为空。"
  }
}

variable "zone_id" {
  type        = string
  description = "Availability zone with the chosen ECS instance type and image."
  validation {
    condition     = length(trimspace(var.zone_id)) > 0
    error_message = "zone_id 不能为空。"
  }
}

variable "project_name" {
  type        = string
  default     = "aegisops"
  description = "Lowercase project prefix for cloud resources."
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{2,30}$", var.project_name))
    error_message = "project_name 必须是 3-31 位小写字母、数字或连字符，且以字母开头。"
  }
}

variable "environment" {
  type        = string
  default     = "demo"
  description = "Tag value for the temporary environment."
  validation {
    condition     = var.environment == "demo"
    error_message = "本目录只允许 environment=demo，避免误用为长期生产基础设施。"
  }
}

variable "owner" {
  type        = string
  description = "Non-secret owner tag used for cost accountability."
  validation {
    condition     = length(trimspace(var.owner)) >= 2
    error_message = "owner 必须是可追溯且非空的标识。"
  }
}

variable "vpc_cidr" {
  type    = string
  default = "10.42.0.0/16"
  validation {
    condition     = can(cidrhost(var.vpc_cidr, 0))
    error_message = "vpc_cidr 必须为有效 IPv4 CIDR。"
  }
}

variable "vswitch_cidr" {
  type    = string
  default = "10.42.1.0/24"
  validation {
    condition     = can(cidrhost(var.vswitch_cidr, 0))
    error_message = "vswitch_cidr 必须为有效 IPv4 CIDR。"
  }
}

variable "instance_type" {
  type        = string
  description = "Pay-as-you-go ECS instance type, normally 2 vCPU / 4-8 GiB."
  validation {
    condition     = length(trimspace(var.instance_type)) > 0
    error_message = "instance_type 不能为空。"
  }
}

variable "image_id" {
  type        = string
  description = "Ubuntu LTS or Alibaba Cloud Linux image ID available in zone_id."
  validation {
    condition     = length(trimspace(var.image_id)) > 0
    error_message = "image_id 不能为空。"
  }
}

variable "system_disk_category" {
  type    = string
  default = "cloud_essd"
  validation {
    condition     = contains(["cloud", "cloud_efficiency", "cloud_ssd", "cloud_essd", "cloud_essd_entry", "cloud_auto"], var.system_disk_category)
    error_message = "system_disk_category 不是受支持的 ECS 云盘类型。"
  }
}

variable "system_disk_size" {
  type    = number
  default = 40
  validation {
    condition     = var.system_disk_size >= 40 && var.system_disk_size <= 80
    error_message = "演示系统盘必须介于 40 和 80 GiB，避免成本失控。"
  }
}

variable "internet_charge_type" {
  type    = string
  default = "PayByTraffic"
  validation {
    condition     = contains(["PayByTraffic", "PayByBandwidth"], var.internet_charge_type)
    error_message = "internet_charge_type 必须为 PayByTraffic 或 PayByBandwidth。"
  }
}

variable "internet_max_bandwidth_out" {
  type    = number
  default = 5
  validation {
    condition     = var.internet_max_bandwidth_out >= 1 && var.internet_max_bandwidth_out <= 20
    error_message = "演示公网带宽必须介于 1 和 20 Mbps。"
  }
}

variable "ssh_public_key_path" {
  type        = string
  description = "Path to an SSH public key. Never provide a private key or password."
  validation {
    condition     = length(trimspace(var.ssh_public_key_path)) > 0 && !endswith(lower(var.ssh_public_key_path), ".pem")
    error_message = "ssh_public_key_path 必须指向公钥，不能是 .pem 私钥。"
  }
}

variable "admin_cidrs" {
  type        = set(string)
  description = "Explicit management IPv4 CIDRs for SSH and k3s API."
  validation {
    condition     = length(var.admin_cidrs) > 0 && alltrue([for cidr in var.admin_cidrs : can(cidrhost(cidr, 0)) && cidr != "0.0.0.0/0"])
    error_message = "admin_cidrs 必须非空、为有效 CIDR，且绝不能包含 0.0.0.0/0。"
  }
}

variable "public_web_cidrs" {
  type        = set(string)
  default     = []
  description = "Optional HTTP/HTTPS audience CIDRs. Leave empty to expose no web ports."
  validation {
    condition     = alltrue([for cidr in var.public_web_cidrs : can(cidrhost(cidr, 0)) && cidr != "0.0.0.0/0"])
    error_message = "public_web_cidrs 必须为有效 CIDR，且绝不能包含 0.0.0.0/0。"
  }
}

variable "k3s_version" {
  type        = string
  default     = "v1.31.6+k3s1"
  description = "Pinned k3s version installed by cloud-init."
  validation {
    condition     = can(regex("^v[0-9]+\\.[0-9]+\\.[0-9]+\\+k3s[0-9]+$", var.k3s_version))
    error_message = "k3s_version 必须是固定的 vX.Y.Z+k3sN 版本。"
  }
}

variable "auto_release_time" {
  type        = string
  default     = ""
  description = "Optional RFC3339 auto-release time accepted by ECS, configured per demo."
  validation {
    condition     = var.auto_release_time == "" || can(regex("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$", var.auto_release_time))
    error_message = "auto_release_time 为空或 UTC RFC3339，例如 2026-08-12T12:00:00Z。"
  }
}
