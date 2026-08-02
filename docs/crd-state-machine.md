# Incident CR 状态机

## Phase 与转移

| Phase | 职责 | 出口 |
|---|---|---|
| Detected | 目标存在检查、finalizer、resolved 检查 | CollectingEvidence / RecoveredWithoutAction / Escalated |
| CollectingEvidence | 采集证据、写摘要、提交分析 | Diagnosing（保持等诊断） |
| Diagnosing | 轮询 job（5s）、ErrTransient 退避 | PolicyChecking / Escalated |
| PolicyChecking | 策略判定、计算 planDigest | AwaitingApproval / Executing / Deny→Escalated / 无方案→RecoveredWithoutAction |
| AwaitingApproval | 等待审批 CR，校验 digest/RV（ProposalRefreshed 恢复路径） | Executing / Reject→Escalated |
| Executing | 快照持久化 → Apply（幂等） | Verifying / Escalated |
| Verifying | 连续 2 次健康检查；超时 | Resolved / RollingBack / Escalated |
| RollingBack | 读持久化快照 → Rollback | RolledBack / Escalated |
| Resolved / RolledBack / Escalated / RecoveredWithoutAction | 终态（不可逆） | — |

非法转移由 `ValidateTransition` 全对枚举表拒绝（fail-closed）。

## 关键 Conditions

- `EvidenceReady`：证据采集成败。
- `DiagnosisReady`：诊断完成/失败/无方案。
- `PolicyReady`：策略判定结果。
- `ApprovalReady`：审批状态（ApprovalInvalid 表示 digest 不匹配，可刷新恢复）。
- `RollbackReady`：回滚路径就绪（快照读取失败时 False + Escalated）。

## 重试语义

- 诊断轮询网络错误：`ErrTransient`，Attempts 指数退避 30s→60s→120s→5min 封顶，保持 Phase。
- 未知阶段：stuck 间隔重试。
- 证据采集失败：Escalated（必需源 fail-closed）。

## 终态清理

进入终态/Verifying/RollingBack 时 `ClearPhaseEphemeralStatus` 清理临时数据（审批引用、验证明细、错误细节），保留审计摘要。
