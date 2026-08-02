# ADR-0005: 审批绑定方案摘要（planDigest）

- 状态：Accepted（M4 实现，M4.1 缺陷修复后补充恢复路径）
- 日期：2026-07

## Context

审批人批准的是"当时的方案"。如果批准后目标资源变化（rollout、重新发布），旧审批不应自动适用于新方案——TOCTOU 攻击面。但完全失效会导致审批永久卡死（无恢复路径，M4 用户报告）。

## Decision

- `planDigest = SHA256(incidentUID | 目标 resourceVersion | Action+Params | Policy generation)`，在 PolicyChecking 阶段计算并写入 `Status.Proposal.PlanDigest`。
- 审批 CR 创建时**服务端从 Status 复制** digest（客户端无法伪造，httpapi 实现）。
- 执行前重校验：digest 或 resourceVersion 变化 → 旧审批失效（`ApprovalInvalid`，fail-closed）。
- **恢复路径**（M4.1，commit 587a39f）：digest 不匹配时 `refreshPlanDigest` 用实时状态重新计算并更新 Proposal（`ProposalRefreshed` 条件），审批人重新审批；rollout 继续变化则继续刷新。保留了"旧审批自动失效"的 TOCTOU 语义，同时不卡死流程。

## Alternatives

- 仅绑定 revision：不防 digest 级篡改。
- 不绑定 RV、审批一次永久有效：拒绝，TOCTOU。
- 摘要不匹配即永久 Escalated：可用性缺陷，被用户报告后修复。

## Consequences

- 正面：TOCTOU 防护 + 可恢复路径两者兼得（集成实测：审批等待期 rollout → 旧审批失效 → 刷新 → 重新审批通过）。
- 代价：Proposal 可能被刷新多次，时间线需记录 `ProposalRefreshed` 供审计。
