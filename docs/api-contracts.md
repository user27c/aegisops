# API 契约

## 1. alert-gateway（:18080）

| 端点                     | 方法 | 认证                    | 说明                                                                         |
| ------------------------ | ---- | ----------------------- | ---------------------------------------------------------------------------- |
| `/webhooks/alertmanager` | POST | Bearer（webhook token） | Alertmanager v4 webhook；body ≤1MiB；返回 `{accepted,deduplicated,rejected}` |
| `/healthz` `/readyz`     | GET  | 无                      | 健康检查                                                                     |
| `/metrics`               | GET  | 无                      | Prometheus                                                                   |

## 2. incident-api（:18081）

认证：`Authorization: Bearer <token>`，token 文件格式 `token:role1,role2`（viewer/approver），SHA256 + constant-time 校验。

| 端点                                                             | 方法 | 角色     | 说明                                                                                                                                                     |
| ---------------------------------------------------------------- | ---- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/api/v1/incidents?namespace=&phase=&severity=&limit=&continue=` | GET  | viewer   | 仅在 `WATCH_NAMESPACES` 中分页（不透明游标）+ 服务端过滤；过滤条件或授权范围变化时游标失效（400 `FILTER_CHANGED`），请求未授权 namespace 返回 403 `NAMESPACE_FORBIDDEN` |
| `/api/v1/incidents/{ns}/{name}`                                  | GET  | viewer   | 详情（timeline/evidence 摘要/诊断）                                                                                                                      |
| `/api/v1/incidents/{ns}/{name}/timeline`                         | GET  | viewer   | 时间线：优先返回诊断服务审计时间线（`source=audit`，含 actor/sequence/eventHash）；诊断不可用时回退 CR 时间线（`source=cr` + `detailsUnavailable=true`） |
| `/api/v1/incidents/{ns}/{name}/evidence`                         | GET  | viewer   | 脱敏证据详情（items 只含 summary/source/kind/timestamp）；无证据 400，诊断不可用降级 `detailsUnavailable=true`                                           |
| `/api/v1/incidents/{ns}/{name}/approval`                         | POST | approver | `{decision: Approve\|Reject, reason}`；digest 由服务端从 Status 复制                                                                                     |
| `/api/v1/policies`                                               | GET  | viewer   | 仅返回 `WATCH_NAMESPACES` 中的策略；请求未授权 namespace 返回 403                                                                                      |
| `/healthz` `/readyz` `/metrics`                                  | GET  | 无       | 健康/指标                                                                                                                                                |

错误码：`UNAUTHORIZED`、`FORBIDDEN`、`NOT_FOUND`、`CONFLICT`（阶段不允许审批）、`INVALID_ARGUMENT`、`INVALID_CURSOR`、`FILTER_CHANGED`、`INTERNAL`。

SPA：非 API 路径 fallback 到 `web/dist`（路径穿越防护）。

## 3. diagnosis（:8000）

| 端点                                     | 方法 | 说明                                                 |
| ---------------------------------------- | ---- | ---------------------------------------------------- |
| `/v1/analyses`                           | POST | 幂等提交（`Idempotency-Key` 头）；DTO `extra=forbid` |
| `/v1/analyses/{analysis_id}`             | GET  | 轮询结果（status/result/reviewer）                   |
| `/v1/audit-events`                       | POST | 追加审计事件（服务端计算 previous_hash/event_hash）  |
| `/v1/evidence/{evidence_id}`             | GET  | 证据快照（含 SHA256；仅脱敏字段）                    |
| `/v1/execution-snapshots`                | POST | 保存执行前快照（幂等键）                             |
| `/v1/execution-snapshots/{execution_id}` | GET  | 读取执行快照（回滚用；响应须校验 SHA256）            |
| `/v1/incidents/{incident_uid}/timeline`  | GET  | 合并审计事件为时间线（actor/sequence/event_hash）    |
| `/healthz` `/readyz`                     | GET  | 健康（readyz 依赖 DB）                               |

> 已从契约删除（未实现，勿复活）：`GET /v1/runbooks`、`GET /v1/audit-events`。

## CRD 示例

```yaml
apiVersion: ops.aegis.io/v1alpha1
kind: AIOpsIncident
metadata: { name: containeroomkilled-f1e5b, namespace: fault-lab }
spec:
  fingerprint: "sha256:..."
  alertName: ContainerOOMKilled
  cluster: local-k3s
  severity: critical
  sourceStatus: firing
  targetRef:
    {
      apiVersion: apps/v1,
      kind: Deployment,
      namespace: fault-lab,
      name: faultlab,
    }
  startedAt: "2026-08-01T10:00:00Z"
status:
  phase: Resolved
  evidence:
    {
      id: "…",
      hash: "sha256:...",
      counts: { ContainerState: 1, KubernetesEvent: 3 },
    }
  diagnosis: { category: OOMKilled, reviewerVerdict: pass, confidence: 0.9 }
  proposal:
    {
      action: PatchResourceLimit,
      planDigest: "sha256:...",
      parameters: { container: faultlab, memoryLimit: "384Mi" },
    }
  timeline: [...]
```
