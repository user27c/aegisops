---
id: 001-oom-patch-memory
status: executed
reviewed: false
---

# 实验：OOMKilled → PatchResourceLimit 审批闭环

> 基于真实 Kind 集群（`kind-aegisops-e2e`）的 full E2E 与单场景回归运行数据填写。诊断 provider 为 **fake（确定性客户端）**，非真实 DeepSeek；本报告不据此宣称模型质量。

## 目标与假设

- 目标：证明 OOM 故障注入 → 容器 OOMKilled（exit 137）→ 告警 → Incident → 真实 K8s 证据 → 诊断 → 策略 ApprovalRequired → 人工批准 → PatchResourceLimit 真实执行（memory limit 有界提高）→ 连续 2 次验证 → Resolved 的完整闭环。
- 可验证假设：
  1. 注入 OOM 后 faultlab Pod 真实 OOMKilled（`LastTerminationState.reason=OOMKilled` 或 `exitCode=137`）。
  2. 提案为 `PatchResourceLimit`，`container=faultlab`（容器名从证据 summary 提取）、`memoryLimit=384Mi`，且带非空 `planDigest`。
  3. 批准后真实执行：容器 memory limit 从 `256Mi` 提高到 `384Mi`，且 Deployment 带 `ops.aegis.io/operation-id` 注解。
  4. 连续 2 次验证成功后进入 Resolved。
  5. 审计链含 `ApprovalGranted → ExecutionStarted → ExecutionCompleted → IncidentResolved`。
  6. viewer token 调审批端点返回 403（角色边界）。
- 失败判定：`memAfter != 384Mi`；或审计链缺任一事件；或 viewer 审批未返回 403；或 Incident 未冻结命中策略的验证窗口。

## 环境与可复现性

- 时间、时区、运行人：2026-08-09 +08:00，隔离验证（验证索引 `20260809T050312+0800`）；执行方式为脚本化 E2E，非手工。
- Git SHA / 镜像 digest / Helm release：验证索引记录 Git SHA `a87cd29662e9c35ac41d8be019de641350a0c7bb`；测试文件在当前 HEAD `11f08a59529f43e1daa46b1841c161e14116d2a1` 仍存在。
- 集群和 namespace：`kind-aegisops-e2e`（隔离 E2E 集群，测试完成后已删除），namespace `aegisops-e2e-*`，profile `full`（含 Prometheus/Alertmanager/Loki/MailHog）。
- FaultLab 注入与 ground truth：`InjectOOMFault` → `faultlab /inject?type=oom&duration=5m`；ground truth = `WaitForOOMKilled`（等待 Pod 记录 `reason=OOMKilled` 或 `exitCode=137`），不来自 LLM。
- Policy / approval 模式：`fault-lab-default` RemediationPolicy，`PatchResourceLimit` 动作 `mode=ApprovalRequired`，`maxMemory=1Gi`、`maxIncreasePercent=200`，`verificationWindow=2m`（样本默认；E2E 覆写为 5m）、`requireAudit=true`。

## 时间线与测量

| 阶段             | 时间                  | 原始证据位置                                        | 说明                                            |
| ---------------- | --------------------- | --------------------------------------------------- | ----------------------------------------------- |
| 注入 OOM         | t0                    | `approval_patch_memory_test.go:47` `InjectOOMFault` | `/inject?type=oom&duration=5m`，cgroup 杀进程   |
| OOMKilled 观测   | t0 + ≤60s             | 同上 `:50` `WaitForOOMKilled`                       | LastState `reason=OOMKilled` / `exit 137`       |
| 工作负载稳定     | + ≤2min               | 同上 `:56` `WaitFaultLabHealthy`                    | 避免测试自身制造审批 TOCTOU                     |
| 告警             | —                     | 同上 `:59` `PostAlert`                              | `ContainerOOMKilled`，fp `sha256:e2e-oom-0001`  |
| Incident 创建    | ≤60s                  | 同上 `:72` `WaitIncidentCreated`                    | 复合指纹命名                                    |
| AwaitingApproval | ≤4min                 | 同上 `:75` `WaitIncidentPhase`                      | 提案 `PatchResourceLimit` + `planDigest`        |
| viewer 403       | —                     | 同上 `:99` `approveAs(viewer)`                      | viewer token → HTTP 403                         |
| 批准             | —                     | 同上 `:105` `ApproveIncident`                       | approver token → `ApprovalGranted`              |
| 执行             | —                     | 同上 `:108`                                         | `PatchResourceLimit` Apply `256Mi→384Mi`        |
| 验证             | 每 15s，连续 2 次成功 | `execution_phases.go:22-23`                         | `verifyInterval=15s`，`verifyRequiredSuccess=2` |
| Resolved         | —                     | 同上 `:108`                                         | 终态                                            |

- MTTD：告警 → Incident 创建 ≤60s（`WaitIncidentCreated` 超时上限；精确逐事件时间戳在 Incident 审计时间线，集群删除后未落盘到已提交日志）。
- Diagnosis latency：未单独打点（fake provider 同步路径，与 Evidence 就绪合并）。
- System MTTR（不含审批等待）：单场景回归总时长 **57.24s**（注入 → Resolved 全闭环，含审批与连续 2 次验证）。
- 总 MTTR：单场景回归 **57.24s**；full E2E 整体 **498.211s**（5 场景串行：alert-email / approval-patch / auto-restart / rollback / security-boundaries）。

## 证据、安全与结果

- Evidence 摘要和引用：fake 诊断从证据 summary 提取 `category=OOMKilled`、`root_cause=内存 limit 低于工作集`、`container=faultlab`、`runbook_refs=[runbook://k8s-oomkilled/v1.0.0]`。Runbook `k8s-oomkilled` 要求 `ContainerState / KubernetesEvent / MetricSeries` 三类必需证据（见 `runbooks/oomkilled.md`）。
- 诊断/Reviewer 摘要：fake provider 确定性产出 `PatchResourceLimit{container=faultlab, memoryLimit=384Mi}`（`services/diagnosis/app/llm/fake.py` `_pick_action`，OOMKilled 分支）；reviewer 恒 `pass`。**诚实声明**：本 E2E 使用 `LLM_PROVIDER=fake`，未调用真实 DeepSeek；真实 DeepSeek v2 严格决策合同为 `0/54`（见 `m97-r5-deepseek-20260811.md`、`m97-r6-deepseek-20260813.md`），因此本报告不构成任何模型质量结论。
- 资源变更 before/after：
  - `faultlab` 容器 memory limit：`256Mi`（`deploy/kind/faultlab.yaml:58` 默认）→ `384Mi`（PatchResourceLimit 提案参数，测试断言 `memAfter == 384Mi`）。
  - Deployment 新增注解 `ops.aegis.io/operation-id`（非空）。
- Audit chain 与邮件时间：
  - 审计链（`assertTimelineTypes`）：`ApprovalGranted → ExecutionStarted → ExecutionCompleted → IncidentResolved`。
  - 邮件：单场景回归不含邮件断言；full E2E 的 `alert-email` 场景覆盖 Alertmanager→MailHog 的 FIRING/RESOLVED 邮件闭环（`alert_email_test.go`）。
- 结果：**成功（Resolved）**。
- 意外现象、限制和后续动作：
  - 诊断非真实模型；memory limit 基线为 Helm/kind 默认 `256Mi`。README「端到端验证」与 `PROJECT-COMPLETE.md` 记录的 `300Mi→384Mi` 来自更早的 M8 手动验收（手动部署用了 `300Mi` 基线），与自动化 E2E 的 `256Mi` 基线不同，非数据冲突。
  - 真实采集样本 A/B/C/D 对照与生产化验收仍未完成，本项目不宣称生产可用。

## Artifact

- 脱敏 artifact 路径与 SHA256：
  - `tests/e2e/approval_patch_memory_test.go` — `decd1e0276661f847d8fa25d19e3b4165d801837b5061ca5698bee4f4e91c3b0`
  - `docs/validation/20260809T050312+0800/summary.json` — `e24f312d11e9ebaacb939fef7b713a180cb68efbc3aa12ce01838c8672cfa15e`
- 截图、视频片段或 Dashboard 链接：无（Alertmanager/MailHog 视觉截图未采集，见验证索引）。
- 可复现命令：
  ```bash
  # 单场景回归（记录 57.24s）
  scripts/run-e2e.sh -run '^TestE2EApprovalPatchMemory$'

  # full E2E（记录 498.211s）
  AEGISOPS_E2E_KUBECONFIG=$HOME/.kube/config E2E_TIMEOUT=35m scripts/run-e2e.sh

  # GitHub Actions 托管 Kind full E2E（provider=fake）
  # https://github.com/user27c/aegisops/actions/runs/31300651719
  ```
