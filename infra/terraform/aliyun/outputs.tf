output "instance_id" {
  description = "ECS instance ID; contains no credential."
  value       = module.k3s_node.instance_id
}

output "public_ip" {
  description = "Public IPv4 assigned for the temporary demo. Protect it with admin_cidrs."
  value       = module.k3s_node.public_ip
}

output "security_group_id" {
  description = "Security group containing only explicitly configured ingress rules."
  value       = module.k3s_node.security_group_id
}

output "k3s_ready_marker" {
  description = "Non-secret path written by cloud-init once fixed-version k3s is installed."
  value       = "/var/lib/aegisops/k3s-ready"
}
