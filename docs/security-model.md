# 安全模型

## Assets

- 集群资源（Deployment/ConfigMap 等）
- DeepSeek API Key（仅 diagnosis 持有）
- Webhook token / Console tokens（Secret）
- 审计链（PG）

## Actors

- Alertmanager（可信 webhook 来源，Bearer token 认证）
- 审批人（console，static token + approver 角色）
- 运维（kubectl，RBAC 最小权限）
- LLM（不受信输出，无凭据）

## Threats 与 Controls

| 威胁 | 控制 |
|---|---|
| Prompt Injection 让模型执行危险操作 | LLM 无 K8s 凭据；输出仅 JSON proposal；5 个动作白名单 + CEL 参数边界；未知动作在 CRD 层拒绝 |
| 审批后目标变化（TOCTOU） | planDigest 绑定 UID+RV+参数+策略 generation；执行前重校验；旧审批自动失效；ProposalRefreshed 恢复路径 |
| 伪造审批 | digest 由服务端从 Status 复制；审批 CR 校验 Incident UID/revision/digest/TTL |
| 执行重复（崩溃重放） | OperationID = SHA256(UID|digest) 注解幂等；Execution.Reference 比对 |
| 无快照回滚（假回滚） | 执行前快照持久化 PG；快照缺失 fail-closed Escalated |
| 审计篡改 | 事件 hash chain（previous_hash 校验 + advisory lock） |
| Secret 泄漏到证据/日志 | 证据正则脱敏（5 类内置模式）；日志经 Redactor |
| Webhook 伪造 | Bearer token（SHA256 + constant-time 比较） |
| 证据毒化（假指标） | 必需源 K8s fail-closed；Prom/Loki 失败标记 partial 并降级 |

## Residual Risk

- LLM 输出合法但语义错误的方案（如错误根因）→ 二次审查 + 审批 + 验证/回滚兜底。
- RBAC field-level 限制：operator 的 Deployment patch 权限是全字段级（CRD/动作层约束补偿）。
- DeepSeek 训练数据/日志可能含脱敏后证据（开发期用 fake provider，生产需租户隔离评估）。
