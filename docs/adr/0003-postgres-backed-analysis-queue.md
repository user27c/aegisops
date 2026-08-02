# ADR-0003: PostgreSQL 支撑的分析任务队列

- 状态：Accepted
- 日期：2026-07（M3 实现）

## Context

Operator 需要把证据快照异步提交给诊断服务（LLM 推理秒级，不能阻塞 Reconcile）。需要幂等（重试不重复扣费/不重复诊断）、崩溃恢复（worker 重启不丢任务）、审计（事件可追溯）。

## Decision

用 PostgreSQL（pgvector 扩展）作为任务队列与证据存储：

- `analysis_jobs`：Operator 以幂等键 `incidentUID|evidenceHash|promptVersion` 提交，重复提交返回原 job（M3 集成验证）。
- Worker 用 `SELECT ... FOR UPDATE SKIP LOCKED` 领取，带心跳与 stale 重排队（死 worker 任务被新 worker 接管，M3 验证）。
- `evidence_snapshots`：证据快照 JSONB，operator 回滚时按 execution_id 读取执行前快照（M6a 修复假回滚）。
- `audit_events`：哈希链（previous_hash → event_hash）审计。

## Alternatives

- Redis/内存队列：拒绝，Redis 不在依赖清单，且审计需要持久化。
- K8s Job 每次诊断起 Pod：拒绝，启动开销大，且任务状态分散。

## Consequences

- 正面：一套 PG 同时服务队列、快照、审计、RAG 向量检索（pgvector），运维面小。
- 代价：PG 成为关键依赖；LangGraph checkpointer 也使用同一 PG（AsyncPostgresSaver）。
