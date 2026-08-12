# 阿里云单节点 k3s 演示基础设施

这是 M9.8 的短生命周期、按量付费 ECS 演示环境，不是生产或高可用 Kubernetes 集群。它创建 VPC、vSwitch、限制性安全组、SSH 公钥和一台安装固定版本 k3s 的 ECS；所有资源都带 `Project=AegisOps`、`Environment=Demo`、`Owner` 标签。

## 安全边界

- Provider 不接收或保存 AccessKey；请使用本地短期凭据链。
- 只接受 SSH 公钥；cloud-init 禁用密码和 root SSH。
- `admin_cidrs` 非空且禁止 `0.0.0.0/0`，仅用于 SSH 22 与 k3s API 6443。
- 默认不开放 HTTP/HTTPS；如需要公开演示，显式设置 `public_web_cidrs`，并通过 Ingress/TLS 或 SSH tunnel 暴露服务。
- Grafana、Prometheus、Loki、Tempo、Incident API 没有公网安全组规则。
- Terraform state/output 从不包含 AegisOps、DeepSeek、SMTP 或 SSH 私钥 Secret；k3s server token 也不输出。

## 使用

```bash
cp budget.auto.tfvars.example demo.auto.tfvars
# 编辑 demo.auto.tfvars；它必须保持本地且不提交。
terraform init
terraform fmt -check -recursive
terraform validate
terraform plan -var-file=demo.auto.tfvars
```

apply 前人工确认 region、zone、实例规格、镜像、管理 CIDR、自动释放时间与预估费用。执行后将 kubeconfig 通过 SSH tunnel 保存到本地，随后按仓库 [运维手册](../../../docs/operations.md) 创建集群内 Secret 并运行 `scripts/cloud-deploy.sh`。

销毁前导出脱敏证据并执行 `scripts/cloud-destroy-checklist.sh`。该脚本只生成 destroy plan；实际 `terraform destroy` 是独立、人工确认的云账号操作。

## 本地校验

```bash
tests/terraform_validate.sh
```

`tests/policy.rego` 可由 conftest 针对 `terraform show -json` 产物执行，以拒绝开放 SSH、k3s API、直连可观测性/API 端口或缺少成本标签的计划。
