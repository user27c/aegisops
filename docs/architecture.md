# AegisOps 架构

## 组件与信任边界

```text
Alertmanager ──webhook──▶ alert-gateway ──创建/去重──▶ AIOpsIncident CR
                                                          │
                 ┌────────────────────────────────────────┘
                 ▼
            operator（controller-runtime）
   Detected → CollectingEvidence → Diagnosing → PolicyChecking
   → AwaitingApproval → Executing → Verifying → Resolved/RolledBack
                 │                        │
    证据采集(Prom/Loki/K8s Events)    diagnosis(FastAPI+LangGraph+DeepSeek)
                 │                        │  ▲
                 └──snapshot──▶ PG(pgvector) │  └─RAG←runbooks/
                                              └─proposal──▶ policy guard
```

| 组件 | 凭据 | 写权限 |
|---|---|---|
| diagnosis | DeepSeek Key、PG | 无 K8s 凭据 |
| operator | kubeconfig（最小 RBAC） | 5 个类型化动作 + CR status |
| alert-gateway | webhook token | 创建/更新 Incident |
| incident-api | static tokens | 创建审批（digest 服务端复制） |
| web | 只读 token | 无 |

## 数据流（一次自愈）

1. Alertmanager 推送 firing → gateway 指纹去重 → 创建/更新 Incident（Detected）。
2. operator 采集 30 分钟窗口证据：K8s 状态/事件（必需源）+ Prometheus 8 组 PromQL + Loki 日志（可选源，失败 partial）。
3. 证据快照（脱敏、截断、哈希）→ PG；摘要写 CR（hash + counts）。
4. diagnosis worker 领取任务：RAG 检索 runbook → DeepSeek 诊断 → 二次审查 → proposal（或 NoSafeAction）。
5. policy guard 校验 proposal（动作白名单/参数边界/风险分级）→ Auto/ApprovalRequired/Deny。
6. 审批（人工）绑定 planDigest → operator 执行（快照持久化 → Apply 幂等 → Verify 连续 2 次 → 超时 RollingBack）。
7. 审计事件 hash chain 全程落 PG；Grafana 展示阶段耗时与指标。

## 故障域

- diagnosis 宕机：Incident 停在 Diagnosing，ErrTransient 指数退避；不误执行。
- PG 宕机：证据快照/审计失败 → 执行前审计 Critical fail-closed（拒绝执行）。
- operator 崩溃：任一阶段恢复（M5/M6 验证：不重复 Apply、退避计数持久化）。
- Prometheus/Loki 宕机：证据 partial（K8s 源仍在），诊断降级。
