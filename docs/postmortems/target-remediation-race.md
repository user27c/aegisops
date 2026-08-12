---
run_id: M9.1c-target-remediation-race
reviewed: false
---

# Postmortem: 目标级修复锁缺失（同目标并发修复竞态）

## Summary

Operator 在 M9.1c 之前对同一 Kubernetes 目标（Deployment / StatefulSet 等）没有互斥修复锁。两个针对同一目标的 Incident 可能并发执行 Snapshot / Apply / Rollback，产生 last-writer-wins 的冲突修复与不可预测的回滚状态（回滚可能基于过期快照）。已新增 `internal/targetlock` 包，基于 `coordination.k8s.io/v1` Lease 实现目标级互斥修复锁，并在执行路径（Snapshot / Apply / Rollback）前做 fencing check。

## Impact

- 影响面：同一目标的并发修复冲突；回滚可能基于过期快照或与新 holder 冲突。
- 时长：从 M5（执行路径上线）到 M9.1c（commit `9e37daa`）。
- 严重级别：高（并发写目标工作负载，可能造成反复扩缩容 / 配置覆盖 / 回滚状态不一致）。

## Timeline

| 时间                           | 事件                                                                           |
| ------------------------------ | ------------------------------------------------------------------------------ |
| M5                             | 5 个 Typed Action 上线，执行路径仅做 Incident 级隔离                           |
| M9.1 规划（NEXT-STEPS §6.3）   | 识别「目标级锁缺失」                                                           |
| 2026-08-02（commit `9e37daa`） | 新增 internal/targetlock + Controller 接线 + 8 个包单测 + 2 个 controller 测试 |

## Detection

非运行时告警。由 M9.1 事实表审计（对照 `docs/implementation-status.md` 第 16 行与 NEXT-STEPS §6.3）发现的架构缺口：执行路径只做 Incident 级隔离，缺少目标级互斥。

## Evidence

- 代码证据：修复前 `handleExecuting` 在 Snapshot / Apply 前无任何目标级锁校验；`AIOpsIncident.Status.Execution` 无 `TargetLock` 引用。
- 修复 commit：`9e37daa`（M9.1c: 同目标 Incident Lease 修复锁）。
- CRD：`status.execution.targetLock`（leaseName / holderIdentity / acquiredAt / renewTime）为 additive 变更。

## Root Cause

执行路径只做 Incident 级隔离：每个 Incident 独立 Reconcile，无机制防止两个 Incident 同时修改同一 Deployment。缺一个以「目标」为粒度的互斥原语。

## Contributing Factors

- 并发修复是低频场景，常规 envtest 场景每个只创建一个 Incident，未暴露。
- 缺少 fencing（写入前校验持有权）这一层保护。

## Why Tests Missed It

- 既有 controller / envtest 测试每个场景只创建单个 Incident，未构造「同目标双 Incident 并发」场景。
- 无针对「第二个 Incident 应被锁阻挡、不进入 Apply」的断言。

## Corrective Action

- 新增 `internal/targetlock/lock.go`：`TargetKey` / `Handle` / `Manager` 接口；`LeaseName` = `aegis-target-<sha256(cluster|ns|kind|name)[:20]>`；`HolderIdentity` 用 Incident UID（不用 name）。
- 新增 `internal/targetlock/kubernetes.go`：基于 Lease 的 `Acquire`（重入幂等 / 过期乐观接管）/ `Renew`（holder 变化或过期 → `ErrTargetLockLost`）/ `Release`（仅 holder 本人）/ `AssertHeld`（写入前 fencing check）。
- Controller（`internal/controller/execution_phases.go`）：
  - `handleExecuting` 前 `ensureTargetLock`；被锁（`ErrTargetLocked`）→ 保持阶段并 `RequeueAfter=10s`，不执行。
  - `Verifying` / `RollingBack` 每次 Reconcile `renewTargetLock`（同步 Renew + fencing，失锁 fail-closed 进入 Escalated）。
  - 终态 `releaseTargetLockBestEffort`（失败仅记录，由租约过期兜底）。
- CRD：`status.execution.targetLock` additive 变更，重新生成 deepcopy / CRD。
- RBAC：operator 增加 leases get/list/watch/create/update/patch/delete（Helm + config/rbac）。

## Regression Test

- 测试文件：`internal/targetlock/lock_test.go`（8 个用例，真实存在且通过）。
- 命令：`go test ./internal/targetlock/... -race -count=1`
- 覆盖：首 holder 获锁；同目标第二 Incident 返回 `ErrTargetLocked`；不同目标可并发；同 Incident 重入幂等；租约过期后新 Incident 接管；旧 holder 不能释放新 holder 的锁；holder 变化后旧 holder 续约失败；失锁后 fencing（`AssertHeld`）失败。
- 实测结果：`ok github.com/user27c/aegisops/internal/targetlock`。

## Preventive Control

- 执行前 Acquire + 每次资源写入前 `AssertHeld`（fencing），旧 holder 即使晚到也不能覆盖新 holder。
- Holder 使用 Incident UID 而非 name，杜绝同名 Incident 误释放。
- 默认租约 60s + 续约；终态 / 删除 finalizer 流程 best-effort Release，配合租约过期兜底。
- controller 层另有 2 个测试（Contended / ReleasedOnTerminal）锁定接线行为。

## Verification

- `go test ./internal/targetlock/... -race -count=1` → `ok`。
- 同目标互斥 / 异目标并发 / 重入 / 过期接管 / 旧 holder 不能释放 / 失锁续约 / fencing / 释放共 8 个包测试通过。

## What Went Well / What Failed

- Well：锁语义（holder=UID、fencing、仅 holder 可释放）一次到位，并附 controller 接线测试。
- Failed：目标级互斥原语应在 M5 执行路径上线时即引入。

## Action Items

- [ ] 由人工确认并把 frontmatter 改为 `reviewed: true` 后进入 RAG 索引。

## Raw Artifact Links

- 修复 commit：`9e37daa`（M9.1c: 同目标 Incident Lease 修复锁(Target Lock)）。
- 代码：`internal/targetlock/lock.go`、`internal/targetlock/kubernetes.go`、`internal/controller/execution_phases.go`。

> 约束：LLM 生成的草稿必须经人工确认并把 frontmatter 改为 `reviewed: true` 才能进入 RAG 索引。
