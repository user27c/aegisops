# 阿里云 k3s OOM 重拍演练报告（博客图 2–5 来源）

> 状态：**已执行并销毁**（2026-08-14，cn-hangzhou 单节点 k3s，**受控 OOM 故障演练**）。本报告记录第二次阿里云演练：为博客「OOM → PatchResourceLimit」叙事重拍图 2–5，并如实归档运行信息、截图 SHA256、销毁证明与成本。
>
> **关键边界**：诊断走 `fake` provider（确定性测试替身），未调用真实 DeepSeek、未跑真实邮件闭环——不构成云端自动修复证明。这是**受控演练**，非真实事故。

## 运行信息

- 日期与时区：2026-08-13T17:38–17:50 UTC（2026-08-14 01:38–01:50 UTC+8）
- 实例：`i-bp11r2t53knd600hx61q`（cn-hangzhou，`ecs.e-c1m4.large` 2 vCPU / 8 GiB），公网 IP `47.111.29.154`（已随销毁释放）
- k3s：v1.31.6+k3s1（cloud-init 未用中国镜像源，改为 `rancher-mirror.rancher.cn` 手动安装 `INSTALL_K3S_MIRROR=cn`）
- 镜像来源（大陆 ECS 直连 ghcr.io 拉层被限速，改走镜像站）：
  - `ghcr.io/user27c/aegisops-*:0.2.0` → `ghcr.nju.edu.cn` 镜像站
  - `pgvector/pgvector:pg16` 与 pause 镜像 → docker.io 镜像站 `docker.m.daocloud.io` / `dockerproxy.net`（`/etc/rancher/k3s/registries.yaml`）
- 部署方式：helm 本地渲染 → 分块上传 → Cloud Assistant RunCommand `kubectl apply`（AliSecGuard 阻断 22/6443 外部连接，无法本地 kubectl/SSH）
- 集群配置：`LLM_PROVIDER=fake`、`embedding.model=fake`、`networkPolicy.enabled=false`（演练仅验证控制面闭环，非生产网络策略）

## 演练链路（fake provider）

1. `fault-lab` 注入 OOM（`POST /inject?type=oom`，分配 512MiB > 256MiB 限额 → `OOMKilled`，RESTARTS=1）。
2. 合成 `ContainerOOMKilled` 告警 POST 到 gateway webhook → 创建 `AIOpsIncident containeroomkilled-e1d17`（`fault-lab` namespace）。
3. Operator 采集证据（`ContainerState` 含 `reason=OOMKilled`）→ 诊断（fake）→ 方案 `PatchResourceLimit {container:"faultlab", memoryLimit:"384Mi"}`，策略判定 `ApprovalRequired`。
4. 人工审批 → `ApprovalGranted` → `PatchResourceLimit` 执行 → `ExecutionCompleted` → 健康验证 → `IncidentResolved`（**未触发回滚**）。

## 截图（博客图 2–5，阿里云 k3s）

| 文件 | 内容 | SHA256 |
| --- | --- | --- |
| `02-incident-evidence.jpg` | OOMKilled 证据 + PatchResourceLimit 方案 + ApprovalRequired | `e69ce8d43649c422378cd5c23b5872fefaf7f9f2383def22a26a6587c31ee48f` |
| `03-approval-policy.jpg` | 审批确认弹窗（动作/参数/planDigest） | `0846d52afe1fcaeec283e49dfcd96ab72f7cd24c07a5f0476ab86228ced97cf0` |
| `04-execution-resolved.jpg` | 执行 → 验证 → Resolved 时间线 | `ccd9ea9ebcb39e9f22e6427b4ab7ab05179b5c37f2822d7354776afe9537e762` |
| `05-rollback-audit.jpg` | 审计哈希链（ApprovalGranted → IncidentResolved） | `f6f2a28bc9deea3e5ed395ac269ea667489e9f626659beae2d3024e99deb145c` |

> 截图由 ECS 上 Playwright + Chromium 无头采集（`sessionStorage` 注入 viewer/approver token），缩放为 900px 宽 JPEG；OCR 复核无真实邮箱/公网 IP/token。token 未落盘、未外泄。

## 销毁证明

- `terraform destroy -auto-approve`：**8 destroyed**（ECS/EIP/安全组/VPC/vSwitch/密钥对等），`Destroy complete!`。
- 与第一次演练（`docs/cloud-demo-report.md`）一致：destroy 后云资源零残留。

## 成本

- 单次运行约 12 分钟（17:38–17:50 UTC）+ 部署期镜像拉取与构建约 70 分钟；按 `ecs.e-c1m4.large` ¥0.4635/小时估算，合计约 **¥1–2**（估算，非精确账单，最终以账号账单为准）。

## 已知限制（如实）

1. **fake provider**：诊断与 reviewer 均非真实模型，仅验证控制面链路可执行。
2. **无 Tempo 追踪**：otel-collector 镜像在 Docker Hub（大陆 ECS 不可达），本次未采集 trace；博客图 8 仍是本地 Kind 的 trace 截图。
3. **网络策略关闭**：演练环境 `networkPolicy.enabled=false`，不用于网络策略验证。
4. **未跑真实邮件**：gate-down，未触发真实 SMTP。
