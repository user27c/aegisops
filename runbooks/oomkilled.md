---
id: k8s-oomkilled
version: 1.0.0
title: Kubernetes OOMKilled
category: OOMKilled
alertnames: [ContainerOOMKilled]
targetKinds: [Deployment]
allowedActions: [PatchResourceLimit, RollbackDeployment]
risk: medium
requiredEvidence: [ContainerState, KubernetesEvent, MetricSeries]
---

## Symptoms

- 容器以 exit code 137 退出，LastState.reason=OOMKilled。
- Kubernetes Event 出现 `OOMKilling`（cgroup 层面）或 `Killing`（kubelet 层面）。
- `container_memory_working_set_bytes` 接近或超过 `container_memory_limit_bytes`。
- Pod 反复重启，restartCount 快速上升。

## Required Evidence

- ContainerState：exitCode、LastTerminationState.reason=OOMKilled、restartCount。
- KubernetesEvent：OOMKilling / Killing 事件及时间。
- MetricSeries：memory working set 与 limit 的 30 分钟曲线。

## Decision Tree

1. 工作集是否持续 > 90% limit？→ 走 PatchResourceLimit（有界提高）。
2. 工作集是否短暂突刺后回落？→ 建议观察，不自动修改。
3. 最近是否刚修改过资源/镜像？→ 优先 RollbackDeployment（回滚至上一健康版本）。

## Allowed Remediation

- `PatchResourceLimit`：把 memory limit 提高至工作集峰值 + 20% 余量，增幅不得超过 Policy 上限。
- `RollbackDeployment`：回滚至最近一个曾 Available 的 ReplicaSet revision。

## Forbidden Conditions

- 禁止把 limit 改为无上限（不设置 limit）。
- 禁止修改 requests 使其超过新 limit。
- 禁止同时修改多个容器的资源。
- 禁止在 HPA/自动扩缩容场景下只靠调 limit 兜底而忽略副本数。

## Verification

- 新 Pod 无 OOMKilled 退出；`container_memory_working_set_bytes` 低于新 limit 的 85%。
- 30 分钟内无新的 OOMKilling 事件。
- 错误率与 P95 回到告警阈值以下。

## Rollback

- PatchResourceLimit 失败或验证未通过：恢复执行前 ResourceRequirements 快照。
- 验证持续失败：恢复原 PodTemplate（执行前快照）并升级人工。

## Escalation

- 同一 Incident 尝试 2 次仍 OOM：停止自动修复，升级 SRE。
- 工作集持续增长且无上限趋势：怀疑内存泄漏，交人工排查。

## References

- Kubernetes: Exceeding container memory limits
- Runbook: crashloop-config（OOM 与配置错误常并发出现）
