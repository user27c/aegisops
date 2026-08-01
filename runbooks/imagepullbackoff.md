---
id: k8s-imagepullbackoff
version: 1.0.0
title: ImagePullBackOff
category: ImagePullBackOff
alertnames: [ImagePullBackOff]
targetKinds: [Deployment]
allowedActions: [RollbackDeployment]
risk: medium
requiredEvidence: [ContainerState, KubernetesEvent, RolloutDiff]
---

## Symptoms

- Pod 处于 ImagePullBackOff / ErrImagePull。
- Kubernetes Event 出现 `Failed` reason=FailedToPullImage / ErrImagePull。
- 最近 rollout 引入了不存在的镜像 tag 或不可达仓库地址。

## Required Evidence

- ContainerState：Waiting.reason=ImagePullBackOff。
- KubernetesEvent：FailedToPullImage 事件及消息（镜像地址）。
- RolloutDiff：最近一次 image 变更（旧 digest/tag vs 新 tag）。

## Decision Tree

1. 镜像 tag 是否存在？不存在 → 回滚至已知健康 digest。
2. 仓库是否不可达（认证/网络）？→ 不自动改，升级人工（涉及 Secret 类问题）。
3. 是否 registry 限流（429）？→ 等待后重试，不自动回滚。

## Allowed Remediation

- `RollbackDeployment`：回滚至最近一个包含可拉取镜像的 revision。
- 回滚目标必须曾达到 Available，且镜像为已知 digest。

## Forbidden Conditions

- 禁止修改 imagePullSecrets（RBAC 边界内不允许）。
- 禁止写入任意镜像地址（只能从历史 ReplicaSet 模板取回）。
- 禁止删除失败 Pod（由 Deployment 控制器处理）。

## Verification

- 新 ReplicaSet Pod 全部 Running 且 Ready。
- 无新的 FailedToPullImage 事件。
- 镜像 digest 与回滚目标一致。

## Rollback

- 回滚自身失败：恢复执行前 PodTemplate。
- 目标 revision 不可用（从未 Available）：放弃回滚并升级。

## Escalation

- 仓库认证问题：升级平台团队处理凭据。
- 回滚后仍拉取失败：怀疑 registry 故障，升级并暂停自动操作。

## References

- Kubernetes: ImagePullBackOff troubleshooting
- Runbook: crashloop-config（配置类故障）
