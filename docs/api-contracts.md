# API 契约

## 1. alert-gateway（:18080）

| 端点 | 方法 | 认证 | 说明 |
|---|---|---|---|
| `/webhooks/alertmanager` | POST | Bearer（webhook token） | Alertmanager v4 webhook；body ≤1MiB；返回 `{accepted,deduplicated,rejected}` |
| `/healthz` `/readyz` | GET | 无 | 健康检查 |
| `/metrics` | GET | 无 | Prometheus |

## 2. incident-api（:18081）

认证：`Authorization: Bearer <token>`，token 文件格式 `token:role1,role2`（viewer/approver），SHA256 + constant-time 校验。

| 端点 | 方法 | 角色 | 说明 |
|---|---|---|---|
| `/api/v1/incidents?namespace=&phase=&severity=&limit=&continue=` | GET | viewer | 分页（continue token）+ 过滤 |
| `/api/v1/incidents/{ns}/{name}` | GET | viewer | 详情（timeline/evidence 摘要/诊断） |
| `/api/v1/incidents/{ns}/{name}/timeline` | GET | viewer | 时间线 |
| `/api/v1/incidents/{ns}/{name}/approval` | POST | approver | `{decision: Approve\|Reject, reason}`；digest 由服务端从 Status 复制 |
| `/api/v1/policies` | GET | viewer | 策略列表 |
| `/healthz` `/readyz` `/metrics` | GET | 无 | 健康/指标 |

错误码：`UNAUTHORIZED`、`FORBIDDEN`、`NOT_FOUND`、`CONFLICT`（阶段不允许审批）、`INVALID_ARGUMENT`、`INTERNAL`。

SPA：非 API 路径 fallback 到 `web/dist`（路径穿越防护）。

## 3. diagnosis（:8000）

| 端点 | 方法 | 说明 |
|---|---|---|
| `/v1/analyses` | POST | 幂等提交（`Idempotency-Key` 头）；DTO `extra=forbid` |
| `/v1/analyses/{id}` | GET | 轮询结果（status/result/reviewer） |
| `/v1/evidence/{id}` | GET | 证据快照（含 SHA256） |
| `/v1/audit-events` | GET | 审计链 |
| `/v1/execution-snapshots/{execution_id}` | GET/PUT | 执行快照读写（回滚用） |
| `/v1/runbooks` | GET | RAG runbook 列表 |
| `/healthz` `/readyz` | GET | 健康（readyz 依赖 DB） |

## CRD 示例

```yaml
apiVersion: ops.aegis.io/v1alpha1
kind: AIOpsIncident
metadata: {name: checkouthttp500s-f1e5b, namespace: fault-lab}
spec:
  alert: {name: CheckoutHTTP500s, fingerprint: fp-faultlab-500-001}
  targetRef: {kind: Deployment, name: faultlab}
  sourceStatus: firing
  severity: critical
status:
  phase: Resolved
  evidence: {hash: "sha256:...", counts: {MetricSeries: 8, LogExcerpt: 30}}
  diagnosis: {category: CheckoutFailure, reviewerVerdict: pass}
  proposal: {action: RestartWorkload, planDigest: "sha256:..."}
  timeline: [...]
```
