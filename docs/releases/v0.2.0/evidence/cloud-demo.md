# 阿里云 k3s 云端演示证据（v0.2.0，脱敏）

- 类型：**gate-down 受控演示**（诊断用 `fake` provider，未调用真实 DeepSeek、未跑真实邮件闭环）
- 区域：cn-hangzhou / cn-hangzhou-k；实例 `ecs.e-c1m4.large`（2 vCPU / 8 GiB）；k3s v1.31.6
- 公网 IP / 管理 CIDR / 云资源 ID / ACR 仓库路径均脱敏为 `<redacted>`

## Terraform

```bash
terraform -chdir=infra/terraform/aliyun fmt -check -recursive   # FMT_OK
terraform -chdir=infra/terraform/aliyun validate               # Success!
terraform -chdir=infra/terraform/aliyun plan -out=plan.tfplan  # Plan: 8 to add
terraform -chdir=infra/terraform/aliyun apply plan.tfplan      # 8 added, 0 changed, 0 destroyed
```

8 个资源：VPC、vSwitch、key pair、安全组、SSH 22 规则、k3s API 6443 规则、egress 规则、ECS。
安全组**无 0.0.0.0/0 入站**（仅管理 CIDR 的 22/6443）；80/443 不开放，Grafana/Prometheus/Loki 无公网暴露。

## 成本

- 单价 ¥0.4635/小时（实例 ¥0.3375 + ESSD 60GiB ¥0.126）
- 总运行约 70 分钟，估算 ¥0.5–1.0（非精确账单）

## Smoke 结果（全部通过）

1. Node Ready（单节点 control-plane）
2. 五个 Prometheus target up（diagnosis-api / gateway / incident-api / operator / faultlab）
3. DeepSeek 出口受控且可用：default-deny 下被拒；加临时 egress 后可达 `https://api.deepseek.com/`，
   返回 **HTTP 401**（全程未提供 key、未产生调用），测试后已删除临时 NP
4. fake-provider 诊断闭环：CheckoutFailure → RestartWorkload → Resolved（`LLM_PROVIDER=fake`）

## 销毁与残留验证

`terraform destroy -auto-approve` → 8 destroyed；aliyun CLI 逐项查询 ECS/EIP/磁盘/安全组/VPC/密钥对
全部 `TotalCount 0`。

## 已知限制（如实）

- cloud-init 未用 `INSTALL_K3S_MIRROR=cn`（大陆 ECS 拉 GitHub 失败，本次手动装 k3s）
- 阿里云安全中心内核模块会复位 22/6443 外部连接，全程改走 Cloud Assistant（RunCommand）
- chart NetworkPolicy 3 处缺口已在仓库修复（任务 T18），云端以运行时 patch 绕过
- operator 在 `aegisops-system` 创建 leader-election Event 被拒（`events is forbidden`），不影响主流程
- 完整原始记录见 `docs/cloud-demo-report.md` 与 `.omo/evidence/task-7-*`
