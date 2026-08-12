# 阿里云 k3s 演示报告

> 状态：**已执行**（2026-08-13，cn-hangzhou 单节点 k3s，**gate-down 受控演示**）。本报告记录真实 create → deploy → smoke → destroy 生命周期与成本估算，完整证据见 [`.omo/evidence/task-7-aegisops-v020-release.md`](../.omo/evidence/task-7-aegisops-v020-release.md)。
>
> **关键边界（请勿误读）**：诊断走 `fake` provider（确定性测试替身），未调用真实 DeepSeek、未跑真实邮件闭环、未获得云端自动修复授权——本次**不构成云端自动修复证明**，只验证 create/deploy/只读 smoke/observability/DeepSeek 出口受控 + fake 诊断闭环。

## 运行信息

- 日期与时区：2026-08-13，UTC+8（Asia/Shanghai）
- Git SHA / 镜像 tag：HEAD `b487f7d`（演示开始）；ACR 镜像 tag `aliyun-demo-20260813`（immutable），私有仓库 `crpi-o08cwsvzvszpfel7.cn-chengdu.personal.cr.aliyuncs.com/aliyun-cicd-lab/`（6 个镜像：operator / alert-gateway / incident-api / diagnosis / fault-lab / pgvector:pg16）
- Terraform 与 provider 版本：Terraform v1.15.8（required `>= 1.6.0, < 2.0.0`）；provider `aliyun/alicloud` `~> 1.285`（lockfile 固定 `1.288.0`）
- 区域 / zone / 实例规格：cn-hangzhou / cn-hangzhou-k；`ecs.e-c1m4.large`（2 vCPU / 8 GiB）；镜像 `ubuntu_22_04_x64_20G_alibase_20260723.vhd`；系统盘 `cloud_essd` 60 GiB；k3s v1.31.6+k3s1
- 创建、销毁时间与总运行时长：创建 `2026-08-13T03:36:16+0800`（Terraform apply 完成，8 resources added）；销毁 `2026-08-13T04:45:34+0800` 开始、`04:46:10+0800` 结束；总运行约 **70 分钟**（约 1.17 计费小时）
- 预算上限与最终账单（不含账号标识）：按量单价 `ecs.e-c1m4.large` ¥0.3375/小时 + 系统盘 cloud_essd 60 GiB ¥0.126/小时 = **¥0.4635/小时**（DescribePrice 估算，另计 PayByTraffic 出站流量）；70 分钟估算 **¥0.5–1.0**（阿里云按小时向上取整可能计 2 小时，出站流量极小——镜像为入站拉取）。此为**估算**，非精确账单，最终以账号账单为准。另有 `auto_release_time` 兜底（本实例未触发，按计划手动销毁）。

## 安全与网络

- SSH / k3s API 管理 CIDR 审核：安全组**无 `0.0.0.0/0` 入站**；`ingress tcp 22/22 from 5.34.217.24/32`（仅管理 CIDR SSH）、`ingress tcp 6443/6443 from 5.34.217.24/32`（仅管理 CIDR k3s API）、`egress all -1/-1 to 0.0.0.0/0`（出站拉包/registry/遥测必需）。
- Grafana、Prometheus、Loki、Incident API 的公网暴露：**无**。`public_web_cidrs=[]`，80/443 不开放，四个组件均无公网入口。
- NetworkPolicy 验证：`networkPolicy.enabled=true` 下发现并运行时 patch 修复 **3 处 chart 缺口**（诚实记录）：(1) `aegisops-postgres` NP ingress 未放行 `component=migrations`，alembic 迁移卡在 `wait-postgres` → patch 加入；(2) Prometheus（observability 命名空间）无法抓 aegisops 指标（chart 里 gateway NP 误写 `monitoring` 命名空间，其余组件无指标 ingress）→ 新增 `aegisops-metrics-scrape` NP；(3) 组件缺 API server 出站（default-deny 阻断 leader election，`10.43.0.1:443 connection refused`）→ 新增 `aegisops-internal-egress` NP（放行 pod/service CIDR + UDP53），外部出口仍被 default-deny 阻断。
- Secret、日志和 artifact 脱敏复核：AccessKey/ACR 密码/kubeconfig/STS 临时凭据全部 gitignored（`.local/`、`demo.auto.tfvars`，0600），未进仓库；本报告与证据文件不含任何凭据；公开 IP `8.139.5.104` 已随实例销毁。

## 验收证据

- Terraform fmt / validate / policy 检查：`fmt -check -recursive` 通过（无输出）；`validate` Success! 配置有效；`plan` 8 to add / 0 to change / 0 to destroy。
- Node 与全部 workload Ready：`izbp1dypli8tftt5tdwwc6z   Ready   control-plane,master   v1.31.6+k3s1`。
- Prometheus 五个 target：`aegisops-diagnosis-api` / `aegisops-gateway` / `aegisops-incident-api` / `aegisops-operator` / `faultlab` 全部 `up`。
- DeepSeek、邮件、Auto RestartWorkload 闭环（**gate-down，仅验证受控 read-path 与 fake 闭环**）：
  - **DeepSeek 出口受控且可用**（仅网络可达性，零真实调用）：default-deny NP 下 diagnosis-worker 连 `api.deepseek.com:443` 被拒（`ConnectionRefusedError`）；加临时显式 egress NP（放行 `183.131.191.171/32`、`58.221.54.26/32` 的 443）后 worker 可达并返回 **HTTP 401 Authorization Required**（可达、需 key，全程未提供 key、未产生 DeepSeek 调用），测试后已删除该临时 NP。
  - **邮件闭环：未执行**（gate-down，不宣称真实邮件）。
  - **Auto RestartWorkload 闭环（fake）**：合成告警 POST 到 gateway webhook 后，Incident 全链 `CheckoutFailure → RestartWorkload → Auto → Resolved`；fake 确定性输出 `rootCause="checkout 接口返回 500（配置/进程状态异常）"`、`confidence=0.9`、conditions 全链 `EvidenceReady→…→VerificationReady(Healthy)`；`LLM_PROVIDER=fake`，未挂载 `DEEPSEEK_API_KEY`。
- 故障与回滚证据：本次故障为合成告警，fake 诊断直达 `Resolved`，未触发回滚路径（回滚真实证据见 Kind full E2E，[implementation-status.md 第 21 行](implementation-status.md)）。
- 截图 / 视频的脱敏位置：本次无截图/视频，全部文本证据脱敏后记录于 [`.omo/evidence/task-7-aegisops-v020-release.md`](../.omo/evidence/task-7-aegisops-v020-release.md)。

## 销毁证明

- `terraform plan -destroy` 审核：0 add / 0 change / **8 destroy**（`destroy-review.tfplan`）。
- `terraform destroy` 执行人和时间：本机 CLI（阿里云 RAM 短期 STS 凭据）执行，`2026-08-13T04:45:34+0800` 开始、`04:46:10+0800` 结束，`Destroy complete! 8 destroyed`。
- ECS、EIP、磁盘、安全组残留查询（aliyun CLI，全部 0）：ECS `i-bp1dypli8tftt5tdwwc6` `TotalCount 0`；安全组 `sg-bp12wotd7efwrb30baou` `TotalCount 0`；磁盘（Project=AegisOps）`TotalCount 0`；EIP `8.139.5.104` `TotalCount 0`；VPC（Project=AegisOps）`TotalCount 0`；密钥对 `aegisops-demo-operator` `TotalCount 0`。
- Terraform state 清空或 workspace 删除：`terraform state list` 为空。
- 已知限制与未完成项：见下一节。

## 已知限制与未完成项（诚实记录）

1. **未宣称云端自动修复**：诊断用 `fake` provider，未调用真实 DeepSeek，未跑真实邮件/Auto Restart 邮件闭环（gate-down 约束）。
2. **cloud-init k3s 安装失败**：`cloud-init.yaml.tftpl` 未用 `INSTALL_K3S_MIRROR=cn`，大陆 ECS 从 GitHub 拉 k3s 失败；本次改为从 Rancher 中国镜像 `rancher-mirror.rancher.cn` 手动安装（`INSTALL_K3S_MIRROR=cn`）。
3. **主机防火墙（AliSecGuard）**：阿里云安全中心内核模块会复位 22/6443 外部连接（SSH `kex_exchange_identification`、kubectl 直连均失败），无法在实例内停止；部署全程改走 **Cloud Assistant（RunCommand）**。
4. **chart NetworkPolicy 缺口 3 处**（migrations→postgres、metrics scrape、API-server egress）在 `networkPolicy.enabled=true` 下未覆盖；kind 环境关 NP 故 E2E 未暴露，本次运行时 patch 修复并记录。
5. **`cloud-smoke.sh` 陈旧**：expected-job 写 `aegisops-diagnosis`（实际 job label 为 `aegisops-diagnosis-api`），且脚本断言 `LLM_PROVIDER=deepseek` 与 gate-down（fake）冲突；smoke 以等价命令手工执行（4 项覆盖一致）。
6. **ACR 镜像未删除**：6 个镜像（含 8.45GB diagnosis）仍在 `aliyun-cicd-lab` 命名空间（约 ¥几角/月存储），非 ECS 计费残留；如需清理走 ACR 个人版 API。
7. **operator RBAC 小瑕疵**：operator 在 `aegisops-system` 创建 leader-election Event 被拒（`events is forbidden`），不影响主流程。

## 成本记录

- 单价 ¥0.4635/小时（实例 ¥0.3375 + ESSD 60 GiB ¥0.126）。
- 运行 70 分钟（约 1.17 计费小时，阿里云按小时向上取整可能计 2 小时）≈ **¥0.5–1.0**；出站流量极小（镜像为入站拉取）。最终账单以账号账单为准（不含账号标识）。
