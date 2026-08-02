# ADR-0004: 类型化动作替代通用工具

- 状态：Accepted
- 日期：2026-07（M5 实现）

## Context

"让模型选工具"（tool-calling）最灵活，但每个工具都是攻击面：参数越界、幂等缺失、回滚无定义。

## Decision

定义 5 个**类型化动作**，每个动作实现统一接口：

```go
type Action interface {
    Preflight(ctx, execCtx) error   // 执行前校验（资源存在、参数合法）
    Snapshot(ctx, execCtx) (Snapshot, error) // 执行前状态（持久化到 PG）
    Apply(ctx, execCtx) (Result, error)      // 幂等执行（OperationID 注解）
    Verify(ctx, execCtx) (bool, error)       // 无副作用健康检查
    Rollback(ctx, execCtx, snap) error       // 用持久化快照恢复
}
```

- RestartWorkload 明确声明不支持回滚（滚动升级人工介入，M5 文档化）。
- 动作注册表只允许白名单动作进入 Executor；`registry` 查不到的动作直接拒绝。
- 参数边界在 CRD CEL + policy 层双重校验（如 ScaleDeployment 的 maxReplicaDelta/maxReplicas）。

## Alternatives

- 通用 `kubectl patch` 工具：拒绝，无法定义幂等与回滚语义。
- 每个动作单独 CRD：过度设计，动作参数耦合在 proposal 中即可。

## Consequences

- 正面：执行语义可测试（executor 79.6% 覆盖率）、可审计（Execution.Reference + audit 事件）、可回滚（快照机制 M6a 集成验证）。
- 代价：新动作需要完整实现 5 个方法，扩动作成本高（可接受的 MVP 约束）。
