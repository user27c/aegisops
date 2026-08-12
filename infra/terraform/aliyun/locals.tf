locals {
  name_prefix = "${var.project_name}-${var.environment}"
  tags = {
    Project     = "AegisOps"
    Environment = var.environment
    Owner       = var.owner
    ManagedBy   = "Terraform"
    AutoDestroy = "Required"
  }
}
