package aegisops.aliyun

# Apply with conftest against `terraform show -json <plan>`. These policies are
# intentionally deny-only: public SSH/k3s and direct observability ingress are
# never acceptable for the demo.

deny[msg] {
  change := input.resource_changes[_]
  change.type == "alicloud_security_group_rule"
  after := change.change.after
  after.type == "ingress"
  after.cidr_ip == "0.0.0.0/0"
  after.port_range == "22/22"
  msg := "SSH must not be open to 0.0.0.0/0"
}

deny[msg] {
  change := input.resource_changes[_]
  change.type == "alicloud_security_group_rule"
  after := change.change.after
  after.type == "ingress"
  after.cidr_ip == "0.0.0.0/0"
  after.port_range == "6443/6443"
  msg := "k3s API must not be open to 0.0.0.0/0"
}

deny[msg] {
  change := input.resource_changes[_]
  change.type == "alicloud_security_group_rule"
  after := change.change.after
  after.type == "ingress"
  forbidden := {"3000/3000", "3100/3100", "9090/9090", "18081/18081"}
  forbidden[after.port_range]
  msg := sprintf("direct observability/API port must remain private: %s", [after.port_range])
}

deny[msg] {
  change := input.resource_changes[_]
  change.type == "alicloud_instance"
  after := change.change.after
  not after.tags.Project == "AegisOps"
  msg := "ECS must carry Project=AegisOps for cost accounting"
}
