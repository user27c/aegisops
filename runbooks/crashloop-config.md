---
id: k8s-crashloop-config
version: 1.0.0
title: CrashLoopBackOff 与坏配置
category: CrashLoop
alertnames: [ContainerCrashLooping]
targetKinds: [Deployment]
allowedActions: [RestoreConfigMap, RollbackDeployment]
risk: medium
requiredEvidence: [ContainerState, LogExcerpt, RolloutDiff, ConfigHash]
---

## Symptoms

- 容器反复启动失败，state=waiting:CrashLoopBackOff。
- 容器日志出现配置解析错误、非法环境变量或连接失败。
- 通常伴随最近一次 rollout（镜像/ConfigMap/环境变量变更）。

## Required Evidence

- ContainerState：LastTerminationState.exitCode、reason。
- LogExcerpt：启动失败的第一条错误日志。
- RolloutDiff：最近一次 PodTemplate 差异（镜像/ConfigMap 引用变化）。
- ConfigHash：ConfigMap 引用名称与内容哈希。

## Decision Tree

1. 日志是否指向配置错误（解析失败/缺字段）？→ 恢复 ConfigMap。
2. 是否由最近 rollout 引入（镜像 tag 或配置引用变化）？→ 回滚 Deployment。
3. 是否环境问题（依赖不可达）？→ 走 dependency-timeout Runbook，不自动改。

## Allowed Remediation

- `RestoreConfigMap`：把故障 ConfigMap 数据恢复为受管备份（immutable 备份必须存在）。
- `RollbackDeployment`：回滚至上一健康 revision。

## Forbidden Conditions

- 禁止修改 Secret（配置恢复只处理 ConfigMap data/binaryData）。
- 禁止删除正在使用的 ConfigMap。
- 禁止从不可信来源（日志/告警注释）直接采用配置值。

## Verification

- 新 Pod Ready；无 CrashLoopBackOff 事件。
- 配置校验通过（应用启动日志无解析错误）。
- 错误率回到阈值以下。

## Rollback

- RestoreConfigMap 失败：恢复执行前 ConfigMap 数据。
- RollbackDeployment 失败：恢复执行前 PodTemplate。

## Escalation

- 备份 ConfigMap 缺失或 immutable 校验失败：立即升级，禁止继续。
- 尝试 2 次仍 CrashLoop：升级 SRE 人工排查。

## References

- Kubernetes: Debugging CrashLoopBackOff
- Runbook: imagepullbackoff（镜像类故障）
