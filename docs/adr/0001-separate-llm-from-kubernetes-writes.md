# ADR-0001: LLM 与 Kubernetes 写操作分离

- 状态：Accepted
- 日期：2026-07（M0 设计期确定，M3 实现固化）

## Context

早期方案存在两种诱惑：让 LLM 直接持有 kubeconfig 执行修复（RAG + tool-calling 最流行），或把 LLM 封装成通用 kubectl 代理。两者都引入不可控面：模型输出无法在语法层约束为安全操作，Prompt Injection 一旦成功即可越权改集群。

## Decision

AegisOps 采用**双进程信任边界**：

- `services/diagnosis`（FastAPI + LangGraph）只持有 DeepSeek Key 与 PostgreSQL，**没有任何 Kubernetes 凭据**；LLM 只能输出满足严格 JSON Schema 的候选方案（`proposal`）。
- `operator`（controller-runtime）持有集群写权限，但只接受 5 个**类型化动作**（RestartWorkload/ScaleDeployment/PatchResourceLimit/RollbackDeployment/RestoreConfigMap），每个动作有参数白名单与 CEL 校验；未知动作、任意 Patch、Shell、Secret/RBAC/PVC/Namespace 修改在 CRD schema 层即被拒绝。

## Alternatives

- **LLM tool-calling 直接执行 kubectl**：拒绝，无法审计、无法回滚、注入即沦陷。
- **Operator 调 LLM**：拒绝，Operator 会拿到 DeepSeek Key（违反最小暴露），且诊断是慢路径（秒级）不应阻塞 Reconcile。

## Consequences

- 正面：Prompt Injection 的后果上限 = "给出一份将被策略层再次校验的方案"；DeepSeek Key 泄露不导致集群沦陷。
- 代价：需要异步任务队列（见 ADR-0003）与显式的 CRD 方案传递协议（incident → analysis job → proposal）。
