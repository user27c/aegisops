---
id: k8s-probe-failure
version: 1.0.0
title: Readiness/Liveness 探针失败
category: ProbeFailure
alertnames: [ProbeFailure]
targetKinds: [Deployment]
allowedActions: [RestartWorkload]
risk: low
requiredEvidence: [ContainerState, KubernetesEvent, MetricSeries]
---

## Symptoms

- Kubernetes Event 出现 `Unhealthy`（readiness/liveness probe failed）。
- Pod 处于 Running 但 NotReady；端点从 Service 摘除。
- 错误率或延迟可能上升（流量被摘除或探针抖动）。

## Required Evidence

- KubernetesEvent：Unhealthy 事件（probe 类型、失败次数）。
- ContainerState：容器仍在运行（区分 CrashLoop）。
- MetricSeries：错误率/延迟是否同时恶化（区分单纯探针抖动）。

## Decision Tree

1. 容器仍在运行且只有探针失败？→ RestartWorkload（低风险）。
2. 探针失败伴随 5xx/延迟恶化？→ 检查是否资源争用（走 cpu-throttling）。
3. 探针刚被修改（配置变更）？→ 优先回滚配置，不重启。

## Allowed Remediation

- `RestartWorkload`：通过更新 PodTemplate annotation 触发滚动重启。
- 注意：Restart 不可反向恢复旧 Pod，验证失败直接升级（CompensatingActionUnsupported）。

## Forbidden Conditions

- 禁止在 rollout 进行中（Progressing=True 且未完成）触发重启。
- 禁止在 10 分钟冷却期内重复重启。
- 禁止修改探针配置本身（属于配置变更，需审批）。

## Verification

- 新 Pod Ready 且无 Unhealthy 事件。
- 错误率与 P95 回到阈值以下。
- Deployment observedGeneration 已追上。

## Rollback

- RestartWorkload 不支持回滚（旧 Pod 已销毁）：验证失败升级人工。
- 升级时应保留执行前 Incident 摘要与时间线供人工判断。

## Escalation

- 重启后仍探针失败：怀疑探针配置错误或依赖问题，升级人工。
- 同一目标 10 分钟内第 2 次探针失败：停止自动重启，升级。

## References

- Kubernetes: Configure Liveness, Readiness and Startup Probes
- Runbook: dependency-timeout（探针失败的隐藏依赖原因）
