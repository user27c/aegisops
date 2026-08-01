---
id: k8s-dependency-timeout
version: 1.0.0
title: 下游超时与网络策略
category: DependencyTimeout
alertnames: [DependencyTimeout, HighDependencyLatency]
targetKinds: [Deployment]
allowedActions: []
risk: medium
requiredEvidence: [TraceSummary, LogExcerpt, MetricSeries, KubernetesEvent]
---

## Symptoms

- 业务错误率上升但容器本身健康（无 OOM/CrashLoop/限流）。
- 日志出现下游连接超时、connection refused、5xx 代理错误。
- Trace 显示依赖服务 span 耗时占比极高。

## Required Evidence

- TraceSummary：依赖 span 耗时、是否超时、错误状态。
- LogExcerpt：超时/重试日志（脱敏）。
- MetricSeries：本服务错误率、下游错误率（如有）。
- KubernetesEvent：无探针/资源事件（排除自身故障）。

## Decision Tree

1. 下游是否整体不可达（NetworkPolicy/防火墙）？→ 只诊断，不自动改网络。
2. 下游是否高延迟（性能问题）？→ 只诊断，升级依赖团队。
3. 是否本服务连接池/超时配置过小？→ 只给出 Runbook 建议，升级人工审批。

## Allowed Remediation

- 无自动动作。本场景演示"系统知道自己不该动"：
  网络策略、DNS、防火墙与第三方依赖不在 AegisOps 动作白名单内。

## Forbidden Conditions

- 禁止修改 NetworkPolicy、Ingress、Service（网络层不在白名单）。
- 禁止重启工作负载掩盖下游故障（重启不解决依赖问题）。
- 禁止通过任意 Patch 修改连接池配置。

## Verification

- 不适用（无自动修复动作）。验证由人工确认下游恢复后告警消失。

## Rollback

- 不适用（无动作执行）。

## Escalation

- 诊断完成后自动升级人工：输出证据包、Trace 摘要与候选 Runbook，
  由 SRE 与依赖团队联合处理。

## References

- Kubernetes: Network Policies
- Runbook: cpu-throttling（延迟上升的自身原因排查）
