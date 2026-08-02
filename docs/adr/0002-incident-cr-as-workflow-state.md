# ADR-0002: Incident CR 作为工作流状态机

- 状态：Accepted
- 日期：2026-07（M2 实现）

## Context

事故生命周期长（分钟级），需要跨崩溃、跨重启、跨审批人可见。备选：内存编排（进程重启即丢）、外部数据库存储状态（与 K8s 生态割裂）、CR 存储状态（原生、可观测、可审计）。

## Decision

`AIOpsIncident` CR 是事故的唯一状态载体，Phase 状态机：

`Detected → CollectingEvidence → Diagnosing → PolicyChecking → AwaitingApproval → Executing → Verifying → Resolved/RolledBack/Escalated/RecoveredWithoutAction`

- 所有转移经 `ValidateTransition` 全对枚举表校验，非法转移返回错误。
- 终态不可逆（`IsTerminal`）。
- 状态写入走 `Status().Patch(MergeFrom)`，避免 resourceVersion 冲突。
- 时间线（Timeline）与 Conditions 记录可解释的转移历史。

## Alternatives

- 内存编排：拒绝，崩溃即丢失执行进度。
- 仅 PG 存储状态：拒绝，控制台/审批/kubectl 观感割裂，且 Operator 需自建 watch。

## Consequences

- 正面：任何有 kubeconfig 的终端可查看/审计事故；崩溃恢复天然幂等（见 M5/M6 验证）。
- 代价：Status 会增长（timeline/evidence 摘要），需控制只写摘要不写原始证据（M2 决策：原始证据进 PG，CR 只存 hash + counts）。
