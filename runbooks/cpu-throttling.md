---
id: k8s-cpu-throttling
version: 1.0.0
title: CPU 限流与高延迟
category: CPUThrottling
alertnames: [ContainerCPUThrottlingHigh]
targetKinds: [Deployment]
allowedActions: [ScaleDeployment, PatchResourceLimit]
risk: medium
requiredEvidence: [MetricSeries, ContainerState, KubernetesEvent]
---

## Symptoms

- `container_cpu_throttled_ratio` 持续接近 1（被限流）。
- P95 延迟上升、QPS 下降或错误率微升。
- CPU usage 长期贴近 CPU limit。

## Required Evidence

- MetricSeries：cpu usage、cpu limit、throttled ratio 30 分钟曲线。
- MetricSeries：P95 延迟与错误率。
- ContainerState：容器无 OOM/CrashLoop（排除资源叠加故障）。

## Decision Tree

1. 是否接近 HPA 扩容阈值？→ 优先 ScaleDeployment（有界扩容）。
2. 单副本延迟敏感且无 HPA？→ 提高 CPU limit（增幅受 Policy 约束）。
3. 是否流量突增导致？→ 先扩容缓解，再评估是否长期调优。

## Allowed Remediation

- `ScaleDeployment`：副本数在 Policy 上限内，单次 delta 受约束；存在 HPA 时拒绝。
- `PatchResourceLimit`：提高 CPU limit，增幅不超过 Policy 上限。

## Forbidden Conditions

- 禁止在存在 HPA 管理时直接改副本数（与 HPA 抢写）。
- 禁止副本数超过 Policy maxReplicas。
- 禁止单次扩容超过 maxReplicaDelta。
- 禁止同时扩副本和改 limit（同一 Incident 一次只执行一个动作）。

## Verification

- throttled ratio 显著下降（< 0.3）。
- P95 延迟回到阈值以下，错误率不升。
- 新副本全部 Ready。

## Rollback

- ScaleDeployment 验证失败：恢复原副本数。
- PatchResourceLimit 验证失败：恢复原 ResourceRequirements。

## Escalation

- 扩容到上限仍限流：怀疑代码级问题（忙等/无界并发），升级人工。
- 回滚后仍未恢复：停止自动操作，升级。

## References

- Kubernetes: Managing Resources for Containers
- Runbook: dependency-timeout（限流可能由下游慢导致）
