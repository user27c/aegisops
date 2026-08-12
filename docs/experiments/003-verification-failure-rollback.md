---
id: 003-verification-failure-rollback
status: executed
reviewed: false
---

# 实验：CPU 限流 → ScaleDeployment 审批 → 验证失败 fail-closed → 回滚

> 基于真实 Kind 集群（`kind-aegisops-e2e`）的受控负向 E2E 运行数据填写（Run ID `localgates20260812`）。诊断 provider 为 **fake（确定性客户端）**，非真实 DeepSeek；本报告不据此宣称模型质量。

## 目标与假设

- 目标：证明受控负向 E2E——CPU 单 Pod 故障不能因副本 Ready 被误判为已修复：ScaleDeployment 的 Apply 真实扩容（`spec.replicas` 1→2），Prometheus 限流比例持续超阈值时进入回滚而非 Resolved，且 Rollback 真实还原副本数。
- 可验证假设：
  1. ScaleDeployment Apply 真实扩容：`spec.replicas` 从 `1` 升到提案目标值（本场景为 `2`）。
  2. CPU 指标未恢复时 `Verification.State == Unhealthy`，终态 `RolledBack`（不误判 Resolved）。
  3. Rollback 真实还原：回滚后 `spec.replicas` 回到 `1`。
- 失败判定：副本数未升到提案目标；或终态非 `RolledBack`；或 `Verification.State != Unhealthy`；或回滚后副本数 ≠ 1。

## 环境与可复现性

- 时间、时区、运行人：2026-08-13 +08:00，脚本化 E2E。
- Git SHA / 镜像 digest / Helm release：`74f32c8`（`test(e2e): 补 Scale/RestoreConfigMap 真实闭环`）；测试文件在当前 HEAD `11f08a59529f43e1daa46b1841c161e14116d2a1` 存在。
- 集群和 namespace：`kind-aegisops-e2e`，namespace `aegisops-e2e-localgates20260812`，profile `full`（含 Prometheus/Alertmanager/Loki/MailHog）。
- FaultLab 注入与 ground truth：`InjectFault(ctx, e, "cpu", 5m)` → `faultlab /inject?type=cpu&duration=5m`；ground truth = Prometheus `container_cpu_throttled_ratio` 持续超阈值。fake 诊断要求 `ContainerState + KubernetesEvent + MetricSeries` 三类证据齐备才产出扩容方案（见 `fake.py` CPUThrottling 分支）。
- Policy / approval 模式：`fault-lab-default` RemediationPolicy，`ScaleDeployment` 动作 `mode=ApprovalRequired`、`maxReplicaDelta=2`、`maxReplicas=8`；`verificationWindow=5m`（E2E 覆写，`AEGISOPS_E2E_VERIFICATION_WINDOW=5m`）、`rollbackOnVerificationFailure=true`、`requireAudit=true`。

## 时间线与测量

| 阶段             | 时间      | 原始证据位置                                           | 说明                                                         |
| ---------------- | --------- | ------------------------------------------------------ | ------------------------------------------------------------ |
| 注入 CPU         | t0        | `approval_scale_cpu_test.go:47` `InjectFault(cpu, 5m)` | `/inject?type=cpu&duration=5m`                               |
| 告警             | —         | 同上 `:50` `PostAlert`                                 | `ContainerCPUThrottlingHigh`，fp `sha256:e2e-cpu-scale-0001` |
| AwaitingApproval | ≤4min     | 同上 `:59` `WaitIncidentPhase`                         | 提案 `ScaleDeployment`，`replicas=2`                         |
| 批准             | —         | 同上 `:77` `ApproveIncident`                           | approver token → `ApprovalGranted`                           |
| Apply 扩容       | ≤2min     | 同上 `:81` `waitFaultlabSpecReplicas`                  | `spec.replicas` 1→2（真实变更）                              |
| 验证（持续失败） | 每 15s    | `execution_phases.go:317-320`                          | 限流比例超阈值 → `State=Unhealthy`，连续成功=0               |
| 验证超时         | 5min 窗口 | `execution_phases.go:279-288`                          | `VerificationTimeout` → `RollingBack`                        |
| 回滚             | —         | `execution_phases.go:392-415`                          | 从持久化快照还原 `spec.replicas` 2→1                         |
| RolledBack       | —         | 同上 `:87` `WaitIncidentPhase(RolledBack)`             | 终态                                                         |

- MTTD：告警 → Incident 创建 ≤60s（`WaitIncidentCreated` 上限；精确逐事件时间戳在审计时间线，集群删除后未落盘）。
- Diagnosis latency：未单独打点（fake provider 同步路径）。
- System MTTR（不含审批等待）：由 5 分钟验证窗口主导（`verificationWindow=5m`）。
- 总 MTTR：**325.25s**（注入 → RolledBack 终态，含审批与 5 分钟验证窗口）。

## 证据、安全与结果

- Evidence 摘要和引用：fake 诊断从证据 markers 得出 `category=CPUThrottling`、`root_cause=CPU 限流持续，工作负载需要受控扩容`、`runbook_refs=[runbook://k8s-cpu-throttling/v1.0.0]`（`runbooks/cpu-throttling.md` 要求 `MetricSeries / ContainerState / KubernetesEvent`）。
- 诊断/Reviewer 摘要：fake provider 确定性产出 `ScaleDeployment{replicas:2}`（`fake.py` CPUThrottling 分支）；reviewer 恒 `pass`。**诚实声明**：本 E2E 使用 `LLM_PROVIDER=fake`，未调用真实 DeepSeek；真实 DeepSeek 严格决策合同 `0/54`（见 `m97-r5-deepseek-20260811.md`）。
- 资源变更 before/after（`approval_scale_cpu_test.go` 断言）：
  - `faultlab spec.replicas`：`1`（前置断言 `origReplicas==1`）→ `2`（Apply，`waitFaultlabSpecReplicas` 轮询到目标）→ `1`（Rollback，从执行前快照还原）。
  - `Verification.State`：`Unhealthy`（CPU 指标未恢复时保持，不因副本 Ready 误判）。
- Audit chain 与邮件时间：
  - 审计链（由 controller 状态机在 fail-closed 回滚路径必然产生，事件类型见 `execution_phases.go`）：`ApprovalGranted → ExecutionStarted → ExecutionCompleted → VerificationTimeout → IncidentRolledBack`。
  - 本场景测试断言聚焦终态（`phase=RolledBack`、`Verification.State=Unhealthy`、副本还原）；同一次 run 的 `TestE2ERestoreConfigMapFromImmutableBackup` 显式断言了 `ApprovalGranted/ExecutionStarted/ExecutionCompleted/IncidentResolved` 链类型（`restore_configmap_test.go:139`）。
  - 邮件：本场景无邮件断言；full E2E 的 `alert-email` 场景覆盖 Alertmanager→MailHog 邮件闭环。
- 结果：**回滚（fail-closed，未误判 Resolved）**。
- 意外现象、限制和后续动作：
  - 本场景是受控负向 E2E，把「无因果关系的扩容」当作 fail-closed 回滚来验证——不把扩容宣传为自愈。
  - 提案 `replicas=2` 未达到 `maxReplicas=8`/`maxReplicaDelta=2` 上限，但断言动态读取提案目标而非硬编码，确保「副本真实变更」而非 no-op。
  - 诊断非真实模型。

## Artifact

- 脱敏 artifact 路径与 SHA256：
  - `tests/e2e/approval_scale_cpu_test.go` — `4118ade49394d22abe76f2c87a6ab2ca7611e0237060d404fd61a50a4a31ede6`
  - 运行日志 `/tmp/opencode/e2e-scale-cm-run.log` — `350b72bf40b1c7a7855c464e0532253b010f21863f60456532feac262a6fdc68`
  - 证据 `.omo/evidence/task-2-aegisops-v020-release.txt`（Scale/RestoreConfigMap 真实闭环）
- 截图、视频片段或 Dashboard 链接：无。
- 可复现命令：
  ```bash
  # Run ID localgates20260812（Scale CPU fail-closed + RestoreConfigMap）
  scripts/run-e2e.sh -run 'TestE2EApprovalScaleCPUFailClosed|TestE2ERestoreConfigMapFromImmutableBackup'
  ```
