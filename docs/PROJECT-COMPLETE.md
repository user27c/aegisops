# AegisOps v0.1 历史项目快照

> 快照版本:v1.0(2026-08-02,对应 git `12f0898`)
> 本文记录当时的架构、实现、质量、缺陷史、部署与运维；它**不是**当前完成状态或发布声明。当前事实状态以 `docs/implementation-status.md` 为准，v0.2.0 收尾计划以 `docs/NEXT-STEPS-IMPLEMENTATION-PLAN.md` 为准。

---

## 1. 项目概述

**AegisOps 是一套面向 Kubernetes 的"证据驱动智能诊断与受控自愈"可靠性控制面(Reliability Control Plane)。**

它解决的核心问题:线上服务故障从"被发现"到"被修复并确认恢复"的整条链路,传统上依赖人工(看告警、查日志、敲命令、盯恢复),慢且不可追溯。AegisOps 把这条链路自动化:

```
告警接入 → 去重 → 取证 → AI 诊断 → 方案审查 → 人工审批(中高风险) → 类型化执行
→ 健康验证 → 失败自动回滚 → 全程审计 → 可视化
```

**关键设计立场(与"AI 直接操作集群"的本质区别)**:

- **AI 与执行权分离**:诊断服务(DeepSeek)只持有模型 Key 与数据库,**没有任何 Kubernetes 凭据**;集群写操作只能经过 Operator 的 5 个固定"类型化动作"。
- **证据驱动**:诊断结论必须引用 Prometheus 指标、Loki 日志、Kubernetes 事件与 RAG Runbook;证据不足时安全降级为"不动作"。
- **可解释可回滚**:每一步写入审计链;执行前保存快照,失败自动还原。

---

## 2. 规模与交付状态

| 维度 | 数值 |
|---|---|
| git 提交 | 21 个(M0–M8 里程碑 + 修复) |
| Go 源码文件 | 99 个 |
| Python 源码文件 | 约 50 个(含测试) |
| Web(TypeScript/TSX) | 23 个 |
| 里程碑 | M0–M8 全部验收通过(2026-07-31 至 2026-08-02) |
| 代码覆盖率 | 核心包 72.7%–100%(见 §10) |
| Python 测试 | 25 个,全部通过 |
| Web 测试 | 14 个,全部通过 |
| Eval 评估 | 54 次运行(6 类故障 × 3 变体 × 3 扰动) |
| 缺陷修复 | 验收过程中累计发现并闭环 10+ 个真实缺陷(§12) |

---

## 3. 总体架构

### 3.1 组件与信任边界

```text
                           ┌────────────────────────────────────────────┐
                           │                Kubernetes 集群             │
                           │                                            │
 Alertmanager ──webhook──▶ │ alert-gateway ──创建/去重──▶ AIOpsIncident CR │
                           │      │                                      │
                           │      │(Bearer Token 认证)                   │
                           │      ▼                                      │
                           │  operator(controller-runtime)              │
                           │  Detected → CollectingEvidence → ... →     │
                           │  Resolved/RolledBack/Escalated             │
                           │      │              │                      │
                           │  证据采集           │ 提交分析(幂等键)        │
                           │  (Prom/Loki/K8s)   ▼                      │
                           │      └────────▶ diagnosis(FastAPI+LangGraph)│
                           │                   │  ▲  RAG                 │
                           │                   │  │  runbooks/           │
                           │                   ▼  │                      │
                           │          PostgreSQL(pgvector)              │
                           │  analysis_jobs / evidence_snapshots /      │
                           │  audit_events / runbook_chunks             │
                           └────────────────────────────────────────────┘
    incident-api(只读 API + 审批)── Web 控制台(React)
    Prometheus / Grafana / Loki(可观测性)
```

| 组件 | 运行形态 | 凭据 | 写权限 |
|---|---|---|---|
| `diagnosis`(FastAPI + LangGraph + DeepSeek) | 独立部署 | DeepSeek Key、PG | **无任何 K8s 凭据** |
| `operator` | Deployment(2 副本,leader election) | kubeconfig(最小 RBAC) | 5 个类型化动作 + CR 状态 |
| `alert-gateway` | Deployment | webhook Bearer Token | 创建/更新 Incident |
| `incident-api` | Deployment | static tokens(viewer/approver) | 创建审批(摘要服务端复制) |
| `diagnosis-worker` | Deployment | PG | 无 K8s 凭据 |
| `web` | 静态站点(由 incident-api 托管) | 只读 token | 无 |
| `fault-lab` | 演练应用(Deployment) | — | 只能故障注入自身进程 |

### 3.2 端到端数据流(一次自愈)

1. **接入**:Alertmanager 推送 firing/resolved → gateway 校验 Bearer Token、body 限制、指纹去重(`sha256` 指纹,重复告警不重复建单)。
2. **取证**:operator 采集 30 分钟窗口证据 —— K8s 容器状态/事件(必需源,失败 fail-closed)+ Prometheus 8 组 PromQL + Loki 日志(可选源,失败标记 partial)。脱敏(5 类正则)→ 截断 → 稳定哈希。原始证据存 PG,CR 只存摘要(hash + counts)。
3. **诊断**:operator 以幂等键 `incidentUID|evidenceHash|promptVersion` 提交分析任务;worker(SKIP LOCKED 领取 + 心跳 + stale 重排队)执行 LangGraph 工作流:`normalize → retrieve(RAG 检索 runbook)→ diagnose(DeepSeek)→ review(二次审查)→ finalize`;产出 category/root_cause/proposal/evidence_ids,reviewer 不通过则无方案(安全降级)。
4. **策略审查**:policy guard 8 步固定判定——动作白名单、参数边界(maxReplicaDelta/maxReplicas/maxMemory 等)、风险分级(Auto/ApprovalRequired/Deny)、计算 `planDigest`(绑定 IncidentUID + 目标 resourceVersion + 动作参数 + 策略 generation)。
5. **审批**(中高风险):人工在控制台批准/拒绝;审批 CR 绑定 UID/revision/digest/TTL;digest 由服务端从 Status 复制(客户端无法伪造);执行前重校验,目标变化旧审批自动失效(`ApprovalInvalid`),提供 `ProposalRefreshed` 恢复路径(不永久卡死)。
6. **执行**:5 个类型化动作之一,每个实现 `Preflight / Snapshot / Apply / Verify / Rollback`;执行前快照持久化 PG;Apply 以 `OperationID = sha256(incidentUID|planDigest)` 注解幂等(崩溃重启不重复执行)。
7. **验证**:无副作用健康检查,连续 2 次成功 → Resolved;超时 → 读取持久化快照 Rollback → RolledBack。
8. **审计**:关键事件(执行开始/完成/审批/解决)写入 PG 哈希链(`previous_hash → event_hash`,advisory lock 防并发),执行前审计不可用则 Critical fail-closed(拒绝执行)。
9. **可观测**:Prometheus 指标(PromQL)、Grafana 大盘(7 面板)、结构化日志、OTel span、审计链查询。

---

## 4. 三个 CRD(ops.aegis.io/v1alpha1)

### 4.1 AIOpsIncident(事故单)

- **Spec**:`fingerprint`(去重指纹,sha256,不可变)、`cluster`、`alertName`、`severity`、`sourceStatus`(firing/resolved)、`targetRef`(kind/name/namespace)、`labels`。
- **Status**:
  - `phase`:状态机阶段(见 §5)
  - `evidence`:摘要(ID/Hash/Window/Counts/Redactions)
  - `analysis`:AnalysisID 引用
  - `diagnosis`:category/root_cause/confidence/reviewerVerdict/evidence_ids/runbook_refs
  - `proposal`:action/parameters/revision/planDigest
  - `policyDecision`:decision/PolicyRef/reasonCodes
  - `approval`:decision/actor/reason/approvedAt
  - `execution`:reference(operationID/executionID/snapshotID)/attempts/lastError
  - `verification`:state(Healthy/Unhealthy)/consecutiveSuccesses/checks
  - `timeline[]` + `conditions[]`(EvidenceReady/DiagnosisReady/PolicyReady/ApprovalReady/RollbackReady 等)
- **Printer columns**:NAME/PHASE/SEVERITY/AGE 等;**CEL 校验**:sourceStatus ∈ {firing,resolved} 等。

### 4.2 RemediationPolicy(修复策略)

- **Spec**:
  - `targetSelector`:`namespaceSelector`/`workloadSelector`(均为 `metav1.LabelSelector`,支持 matchLabels + matchExpressions)+ `kinds`(默认 Deployment)
  - `priority`(数字,高者优先,并列 fail-closed)
  - `actions`:**5 个动作各自的开关/模式/参数边界**:
    - `RestartWorkload`:mode 允许 Auto(唯一低风险动作)
    - `ScaleDeployment`:`maxReplicaDelta`/`maxReplicas`,仅 ApprovalRequired
    - `PatchResourceLimit`:`maxMemory`/`maxIncreasePercent`
    - `RollbackDeployment`:`maxRevisionDistance`
    - `RestoreConfigMap`:`allowedNames`/`requireImmutableBackup`
  - `maxAttemptsPerIncident`、`verificationWindow`、`approvalTTL`、`cooldown`、`requireAudit`、`rollbackOnVerificationFailure`
- **CEL 校验**:未知动作拒绝;仅 RestartWorkload 允许 Auto;ScaleDeployment 参数约束(maxReplicas ≥ maxReplicaDelta 等)。
- **示例**(`config/samples/ops_v1alpha1_remediationpolicy.yaml`):fault-lab 默认策略,priority 10,RestartWorkload=Auto,其余=ApprovalRequired。

### 4.3 RemediationApproval(审批单)

- **Spec**:`incidentRef`(name/UID/proposalRevision)、`decision`(Approve/Reject)、`planDigest`、`actor`、`reason`、`expiresAt`(TTL)。
- **Status**:conditions(Valid/Processed)、processedAt。
- 校验:Incident 存在、UID 匹配、revision/digest 匹配、未过期;任何不匹配 → `ApprovalInvalid`(fail-closed)。

---

## 5. Incident 状态机

```
Detected → CollectingEvidence → Diagnosing → PolicyChecking → AwaitingApproval
         → Executing → Verifying → Resolved
                              ↘ 超时/失败 → RollingBack → RolledBack
PolicyChecking:Deny/无方案 → Escalated / RecoveredWithoutAction
任何阶段:证据失败/诊断失败/执行失败 → Escalated
```

| Phase | 职责 | 重试语义 |
|---|---|---|
| Detected | 目标存在检查、finalizer 建立、源已 resolved 检查 | — |
| CollectingEvidence | 多源采集、写摘要、提交分析任务 | 诊断未启用时 30s requeue |
| Diagnosing | 轮询任务(5s);网络错误 → `ErrTransient` | Attempts 指数退避 30s→60s→120s→5min 封顶 |
| PolicyChecking | 策略匹配、8 步判定、planDigest 计算 | — |
| AwaitingApproval | 等待审批 CR;digest/RV 重校验;不匹配 → ApprovalInvalid + ProposalRefreshed 恢复 | 保持等待 |
| Executing | 快照持久化 → Apply(幂等) | 崩溃恢复不重复执行 |
| Verifying | 连续 2 次健康检查;超时(默认 2min) | verifyInterval |
| RollingBack | 读持久化快照 → Rollback | 幂等 |
| Resolved/RolledBack/Escalated/RecoveredWithoutAction | 终态(不可逆) | — |

- 所有转移经 `ValidateTransition` 全对枚举表校验(非法转移报错)。
- 进入 Verifying/RollingBack/终态时 `ClearPhaseEphemeralStatus` 清理临时数据(审批引用、验证明细、错误细节),保留审计摘要。

---

## 6. 模块深度说明

### 6.1 internal/alertmanager(告警接入,88.0% 覆盖)

- `parser.go`:Alertmanager v4 webhook 解析,严格校验(非法 payload 拒绝)。
- `fingerprint.go`:标签规范化 → sha256 指纹(64 字符 hex,超长截断)。
- `service.go`:accepted/deduplicated/rejected 计数,重复告警只更新不重建。
- `writer.go`(KubernetesWriter):以指纹为名创建 Incident;resolved 只更新 `spec.sourceStatus` 不碰 phase(phase 由 controller 决定)。
- `resolver.go`(KubernetesResolver):通过 Deployment 标签解析目标 UID。
- `handler.go` + `middleware.go`:中间件链 `RequestID → OTel → Recover → BodyLimit(1MiB)→ BearerAuth`;Token 校验 SHA256 + constant-time。
- 重复告警实测:同一 webhook 3 次 6 条告警 → 仅 1 个 Incident。

### 6.2 internal/evidence(证据采集,82.2% 覆盖)

- `MultiCollector`:必需源 K8s + 可选源(Prometheus/Loki/Tempo)并发采集(errgroup ≤4);可选源失败标记 `partial` 不阻断;`DefaultEvidenceWindow = 30min`(事件窗口与指标/日志对齐,不依赖 Alertmanager startsAt 质量)。
- `kubernetes.go`:Deployment 快照、Pod 状态、容器状态、Kubernetes 事件(按 namespace 过滤)、rollout diff(字段白名单)。
- `prometheus.go`:8 个 PromQL 模板(内存工作集/limit、CPU 使用率/限流、可用副本、5xx 率、P95 延迟、30 分钟重启增量),标签 regex 转义。
- `loki.go`:LogQL 安全构造(namespace + pod selector)、行级去重、8KiB 截断、最多 100 行。
- `redactor.go`:5 类内置正则脱敏(Secret/token/密码/key/cert 等),证据与日志统一过 Redactor。
- `limiter.go`:确定性限流(按 hash 稳定丢弃,保证同一故障证据可复现)。
- 实测:kind 上证据 counts = {ContainerState, KubernetesEvent, LogExcerpt:30, MetricSeries:8, PodState}。

### 6.3 services/diagnosis(Python 诊断服务)

- **FastAPI 应用**(`app/main.py` + `app/api/`):
  - `POST /v1/analyses`(Idempotency-Key 幂等提交,同 key 返回原 job)、`GET /v1/analyses/{id}`(轮询)、`GET /v1/evidence/{id}`(快照 + SHA256)、`GET /v1/audit-events`、`PUT/GET /v1/execution-snapshots/{execution_id}`(回滚快照)、`GET /v1/runbooks`。
  - DTO `extra=forbid`(未知字段拒绝)、discriminated union 严格反序列化。
- **DB 层**(`app/db/`):SQLAlchemy 2.0 async + asyncpg;`analysis_jobs`(SKIP LOCKED 领取/心跳/stale 重排队)、`evidence_snapshots`(JSONB + sha256)、`audit_events`(哈希链 + advisory lock)、`runbooks/runbook_chunks`(pgvector)、`execution_snapshots`;alembic 4 个迁移(0001-0004,含 pgvector GIN/HNSW、LangGraph checkpoint 表)。
- **Worker**(`app/worker.py`):并发领取(默认 2)、每任务心跳、崩溃恢复(死 worker 任务被新 worker 接管,实测);LangGraph 编译加进程级锁(规避 torch mega-cache 并发注册冲突)。
- **LangGraph 工作流**(`app/graph/workflow.py`):`normalize → retrieve → diagnose → review → finalize`;review 不通过最多重试 1 次;checkpointer 用 AsyncPostgresSaver。
- **LLM 层**(`app/llm/`):`base`(接口/重试/超时)、`deepseek`(DeepSeek chat 实现)、`fake`(开发期确定性客户端,按证据 markers 匹配返回固定合法结果,支持 OOMKilled/CrashLoop/ImagePullBackOff/CheckoutFailure 场景,容器名从证据提取)、`prompts`(JSON Output 模板 + render_prompt)。
- **RAG**(`app/rag/`):chunker(固定路径)、embedding(sentence-transformers 或 fake)、ingest(索引 runbooks)、retriever(HybridRetriever:pgvector 向量 + 全文检索,RRF 融合,跨路去重)。

### 6.4 internal/controller(状态机核心,80.4% 覆盖)

- `incident_controller.go`:Reconcile 装配、finalizer 管理(Patch 幂等)、ErrTransient 退避(Attempts 持久化,指数增长)。
- `incident_phases.go`:Detected/CollectingEvidence/Diagnosing 处理。
- `policy_phases.go`:PolicyChecking(8 步判定)/AwaitingApproval(审批校验 + refreshPlanDigest 恢复路径)。
- `execution_phases.go`:Executing(快照→Apply)/Verifying(连续 2 次)/RollingBack(持久化快照回滚)/verificationWindow。
- `approval_controller.go`:审批 CR 校验(UID/revision/digest/TTL)写 Valid 条件。
- `transitions.go`:全对枚举转移表;`predicates.go` 事件过滤。
- `crash_recovery_test.go`:Executing(Apply 后崩溃)/Verifying(计数保留)/RollingBack(重复回滚)三阶段崩溃恢复测试 + ErrTransient 退避递增测试。

### 6.5 internal/policy(策略守卫,92.7% 覆盖)

- `resolver.go`:目标匹配(LabelSelector 完整语义:matchLabels + matchExpressions)、优先级选择(并列 fail-closed)、8 步固定判定。
- `guard.go`:动作白名单、参数边界校验、Auto/ApprovalRequired/Deny 分级。
- `digest.go`:planDigest 规范化计算(sha256,绑定 UID+RV+参数+策略 generation)。
- 实测:未知动作 Deny、POLICY_AMBIGUOUS → Escalated(单测锁定)。

### 6.6 internal/executor(类型化执行,80.0% 覆盖)

5 个动作,每个实现统一接口 `Preflight/Snapshot/Apply/Verify/Rollback`:

| 动作 | Preflight 要点 | 幂等 | 回滚 |
|---|---|---|---|
| RestartWorkload | 目标存在 | restart 注解 + OperationID | **明确不支持**(滚动升级人工,文档化) |
| ScaleDeployment | maxReplicaDelta/maxReplicas | OperationID 注解 | 快照恢复 replicas |
| PatchResourceLimit | 容器存在、增量 ≤ maxIncreasePercent | OperationID 注解 | 快照恢复 limits/requests |
| RollbackDeployment | maxRevisionDistance | OperationID 注解 | 快照恢复 revision |
| RestoreConfigMap | backup 存在且 immutable | OperationID 注解 | 快照恢复 data |

- `registry.go`:动作注册表,未知动作拒绝(白名单)。
- 快照由 controller 持久化到 PG(execution_snapshots),回滚按 execution_id 读取。
- 实测:Scale 1→3 生效后回滚恢复 1;PatchResourceLimit 300Mi→384Mi 生效;重启 operator 后 OperationID 不变(不重复 Apply)。

### 6.7 internal/verifier

单次无副作用健康检查(不 sleep/poll),由 controller 按间隔调用并累计连续成功次数。

### 6.8 internal/audit(100% 覆盖)

- `Severity` 分级:Critical(执行前审计,不可用 → 拒绝执行 fail-closed)/BestEffort(其余事件)。
- 事件:ApprovalGranted/ExecutionStarted/ExecutionCompleted/IncidentResolved 等;PG 哈希链(previous_hash → event_hash,advisory lock)。

### 6.9 internal/httpapi(incident-api,72.7% 覆盖)

- chi 路由;认证 static tokens(SHA256 + constant-time),角色 viewer/approver。
- `GET /api/v1/incidents`(分页 continue token + phase/severity/namespace 过滤,空结果 items=[])、详情、timeline、`GET /api/v1/policies`。
- `POST /api/v1/incidents/{ns}/{name}/approval`:approver 角色;digest/revision **由服务端从 Status 复制**(客户端无法伪造);阶段非 AwaitingApproval 拒绝;同名幂等返回原审批。
- SPA fallback(路径穿越防护)、安全头、CORS、Recover。

### 6.10 web 控制台(Vite 7 + React 19 + TS)

- `DashboardPage`(列表/过滤/分页)、`IncidentDetailPage`(阶段 stepper/时间线/证据摘要/诊断/审批操作)、`PhaseStepper`、`ApprovalActions`(仅 AwaitingApproval 显示,批准/拒绝 + 理由校验)。
- TanStack Query + Router;MSW 测试;14 个测试 + Playwright 配置。

### 6.11 fault-lab(故障演练应用)

- 5 类注入器:OOM(分配 512MiB)/CrashLoop(进程退出)/Config(checkout 500)/CPU(忙循环)/Dependency(下游延迟)。
- `CHAOS_ENABLED` 门控(默认关)、注入时长上限 10min、`/inject /recover /cleanup /status /checkout /metrics`、优雅退出自动 Cleanup;13 个测试。
- 独立 Go module,alpine 静态镜像。

---

## 7. 部署与运行

### 7.1 Helm Chart(deploy/helm/aegisops)

- 组件:operator(2 副本 + leader election)/gateway/incident-api/diagnosis-api/diagnosis-worker/postgres(statefulset)/migrations Job(post-install/post-upgrade 跑 alembic)。
- RBAC:按 SA 拆分最小权限 Role(每 watch namespace)+ ClusterRole(operator 读 namespaces 标签)+ leader-election Role(仅 Lease get/create/update/patch)+ events create/patch。
- NetworkPolicy:default-deny + 最小互访(注意:kinD kindnetd 对 egress podSelector 支持不完整,dev 关闭,生产建议 Calico/Cilium)。
- ServiceMonitor:operator/gateway(监控 CRD 存在时启用)。
- 镜像:全部非 root(uid 65532)、固定 tag、Go 静态编译 + distroless;diagnosis 镜像 Python 3.12 + uv venv(独立 python 安装目录避免 symlink 悬空)。

### 7.2 演示环境(当前运行中)

- kind 集群 `aegisops-dev`(v1.35.0),命名空间 `aegisops-system`(全部组件 Running)+ `fault-lab`(checkout-api、faultlab 演练应用、ConfigMaps、策略、Incident 示例)。
- 本地 docker:PostgreSQL(pgvector,:5433)、Prometheus(:19090)、Loki(:13100)、Grafana(:13000)。
- 端口:gateway :18080(port-forward)、incident-api :18081、diagnosis :8000、faultlab :18092。
- Token(演示用):webhook `webhook-token-123`;console `console-token-xyz:viewer,approver`。

### 7.3 安装步骤摘要

```bash
kubectl apply -f config/crd/bases/                          # CRD
kubectl create ns fault-lab aegisops-system                 # 命名空间
kubectl label ns fault-lab aegisops.io/managed=true
# secrets:webhook token / console tokens / diagnosis token / deepseek(可选)
helm install aegisops deploy/helm/aegisops -n aegisops-system \
  --set global.imageRegistry=<registry> --set diagnosis.llmProvider=fake
kubectl apply -f config/samples/ops_v1alpha1_remediationpolicy.yaml
```

---

## 8. 可观测性

| 维度 | 实现 |
|---|---|
| 指标 | operator/gateway/api 暴露 Prometheus:`aegisops_incident_phase_duration_*`、`aegisops_incidents_total`、`faultlab_*` 等;Prometheus 抓取 4 个目标(实测全 up) |
| 大盘 | Grafana "AegisOps 事故响应总览"(uid `aegisops-overview`):告警接入速率/阶段分布/阶段耗时 P95/证据采集量/动作速率/审计速率/Controller 错误 |
| 日志 | 结构化 zap 日志;演示环境 Loki 收集(push 验证 + LogQL 查询验证) |
| 追踪 | 三服务 OTel 中间件(每请求 span);`OTEL_EXPORTER_OTLP_ENDPOINT` 可接采集器 |
| 审计 | PG 哈希链可查询(`audit_events`) |

**已知缺口(诚实声明)**:Prometheus 自身健康告警规则(prometheusRule)尚未实现;无主动通知通道(钉钉/邮件/Slack)——AegisOps 是告警接收方,不是分发方。

---

## 9. Eval 评估(eval/)

- 数据集:6 类故障(OOMKilled/CrashLoop/ImagePullBackOff/ProbeFailure/CPUThrottling/DependencyTimeout)× 3 变体(clean/noisy/sparse)× 3 扰动 = **54 runs**,ground truth 来自注入器(不来自 LLM)。
- 运行:`cd services/diagnosis && uv run python ../../eval/run_campaign.py [fake|deepseek]`;原始记录 `eval/runs/raw.jsonl`,报告 `eval/report.md`(真实分母,禁止抹样本)。
- 结果(fake 基线):

| 指标 | 值 |
|---|---|
| 根因命中率 | 100.0% |
| 方案类型匹配率 | 100.0% |
| 引用有效率(有方案场景,分母 36) | 100.0% |
| Reviewer pass 率(分母 36) | 100.0% |
| 越权执行率 | 0/54 = 0.0% |
| 安全降级一致性 | ✅ 通过 |

- 评分口径:review 通过后方案才生效;降级场景不计入引用/审查分母;CPUThrottling/ProbeFailure 在 fake 中按设计降级为无方案。

---

## 10. 测试与质量门禁

### 10.1 覆盖率(Go)

| 包 | 覆盖率 |
|---|---|
| internal/audit | 100.0% |
| internal/policy | 92.7% |
| internal/alertmanager | 88.0% |
| internal/evidence | 82.2% |
| internal/controller | 80.4% |
| internal/executor | 80.0% |
| internal/analysisclient | 77.8% |
| internal/httpapi | 72.7% |
| fault-lab | 全部通过 |

### 10.2 测试规模

- Go:`make test`(含 -race)全通过
- Python:25 tests(pytest,含 conftest 环境变量隔离)
- Web:14 tests(vitest + MSW)
- Eval:54 runs 可重复生成

### 10.3 门禁(make verify)

`go fmt/vet/test(-race)` → `golangci-lint`(errcheck/govet/staticcheck/gosec/bodyclose/nilerr/revive,0 issues)→ `ruff`/`mypy`(Python)→ `oxlint`/`tsc`(Web)→ `helm lint` → `verify-generated`(无漂移)→ `promtool check config`。

---

## 11. 里程碑交付(M0–M8)

| 里程碑 | 内容 | 验收 |
|---|---|---|
| M0 | 仓库/工具链/3 CRD 骨架/CI/Dockerfile×5/Helm 骨架 | CRD 安装 + CEL 拦截实测 |
| M1 | CRD 完整定义 + Gateway 接入 + 只读 Console | 3 次重复 webhook 仅 1 个 Incident |
| M2 | 状态机 + 证据采集 | 真实证据入 Status;resolved→RecoveredWithoutAction |
| M3 | 诊断 API/Worker/RAG | 端到端 Fake 诊断;worker 崩溃恢复 + 幂等提交 |
| M4 | Policy + Approval | 批准→Executing;digest 篡改拒绝;TOCTOU 防护实测 |
| M5 | 5 个 Typed Actions | 3 类动作真实生效;重启不重复 Apply |
| M6 | Audit + Crash Recovery + ErrTransient | 审计哈希链;三阶段崩溃恢复;退避指数增长 |
| M6a/b | 假回滚修复 / since 语义修复 | 快照持久化回滚真实恢复;事件窗口对齐 |
| M7 | Fault Lab + Observability | 真实 Prom/Loki 证据;Grafana 大盘;OTel 中间件 |
| M8 | E2E + Eval + 文档收尾 + CRD 冻结 | 集群内全链路自愈;54 runs;ADR×5 + 文档×8 |

---

## 12. 缺陷修复史(验收驱动,全部红→绿锁定)

| # | 缺陷 | 修复 | 验证 |
|---|---|---|---|
| 1 | CEL:Duration 类型/`has()` 链式/`self.all` 语法 | `duration()` 转换、map 语义修正 | API server 拦截实测 |
| 2 | RBAC 缺 Lease 权限(leader election 失败) | roles.yaml 补 leases | 2 副本 leader election 实测 |
| 3 | 空结果 `items:null` 前端崩溃 | 空 slice 初始化 + 前端防御 | API 返回 `items:[]` |
| 4 | 审批等待期 RV 变化永久卡死 | `refreshPlanDigest` 恢复路径(保留 TOCTOU) | rollout 后重新审批通过 |
| 5 | 假回滚(快照重建于 Apply 后) | 执行前快照持久化 PG | Scale 5→回滚真实恢复 3 |
| 6 | since 语义(事件窗口依赖 startsAt) | 统一 now-30min | 早于 startsAt 的根因事件被采集 |
| 7 | ErrTransient 退避不递增(恒 30s/60s) | Attempts 持久化递增 | 60s→120s 序列测试 |
| 8 | LangGraph 并发 compile 冲突(torch mega-cache) | 编译锁 + dev 默认 fake embedding | worker 稳定处理 |
| 9 | Helm `merge .` 上下文污染(镜像全指向 pgvector) | image helper 显式参数 | template 渲染校验 |
| 10 | Helm `default` 参数顺序错误 | `default DEFAULT GIVEN` 修正 | 渲染 tag=pg16 |
| 11 | RBAC:RoleBinding subject 全部指向 operator SA | 去掉 `merge $` | 渲染校验 + 集群内 gateway 接受告警 |
| 12 | RBAC:operator 缺 aiopsincidents patch(finalizer) | 补 update+patch | 集群内全链路闭环 |
| 13 | kindnetd egress NP 全拦 | dev 关闭 NP,模板注释生产建议 | 实测 DNS 通/pod 流量断 |

---

## 13. 目录结构全览

```text
aegisops/
├── api/v1alpha1/            三个 CRD 类型 + deepcopy + CEL
├── cmd/                     operator / alert-gateway / incident-api 三个入口
├── internal/                alertmanager / analysisclient / audit / config /
│                            controller / evidence / executor / httpapi /
│                            observability / policy / verifier
├── services/diagnosis/      FastAPI + LangGraph + DeepSeek + RAG(uv 管理)
├── web/                     React 控制台(Vite 7 + TanStack Query)
├── fault-lab/               故障演练应用(独立 Go module)
├── runbooks/                6 类故障 Runbook + schema.json
├── eval/                    campaign 脚本 + 原始记录 + 报告
├── config/                  CRD / samples / rbac / prometheus / network-policy
├── deploy/                  helm chart + observability(grafana dashboard)
├── docs/                    本总览 + design(蓝图/架构/设计) + adr×5 +
│                            architecture / crd-state-machine / security-model /
│                            api-contracts / operations / demo-script /
│                            evaluation / postmortems
├── tests/                   fixtures(alertmanager/analysis/evidence) + e2e + integration
├── docker/                  三个 Go 镜像 Dockerfile
├── scripts/                 工具脚本(bootstrap-tools 等)
└── Makefile / .golangci.yml / .editorconfig / .env.example / AGENTS.md / SECURITY.md
```

---

## 14. 运行与演示方法

### 14.1 本地开发

```bash
make verify                  # 全量门禁
make generate manifests      # 重新生成 CRD/deepcopy
scripts/bootstrap-tools.sh   # 工具链检查
```

### 14.2 演示(10 分钟自愈)

```bash
# 1. 注入故障(faultlab /checkout 变 500)
curl -X POST 'localhost:18092/inject?type=config&duration=600'
# 2. 触发告警
curl -X POST -H "Authorization: Bearer webhook-token-123" \
  -H "Content-Type: application/json" --data @/tmp/alert-faultlab.json \
  localhost:18080/webhooks/alertmanager
# 3. 观察状态机推进
kubectl -n fault-lab get aiopsincidents -w
# 4. 批准(如策略要求)
curl -X POST -H "Authorization: Bearer console-token-xyz" \
  -d '{"decision":"Approve","reason":"demo"}' \
  localhost:18081/api/v1/incidents/fault-lab/<incident>/approval
# 5. 验证恢复 + 审计链 + Grafana
```

### 14.3 已记录的完整实测链路

- **M7 场景**(本地 operator + 真实 Prom/Loki):config 故障 → 证据 MetricSeries:8 + LogExcerpt:30 → CheckoutFailure → RestartWorkload 滚动重启 → Resolved。
- **M8 场景**(集群内全组件):OOM → OOMKilled(exit 137)→ PatchResourceLimit 300Mi→384Mi → Resolved;审计链 ApprovalGranted→ExecutionStarted→ExecutionCompleted→IncidentResolved。

---

## 15. 已知限制与后续建议

### 15.1 限制(诚实声明)

1. **AI 诊断默认 fake provider**:开发/CI 用确定性模拟器;接真实 DeepSeek 需配置 Key 并做真实评估(Key 按约定不入库)。
2. **主动通知缺失**:无钉钉/邮件/Slack 通道;审批等待与处理失败无法主动触达值班人。
3. **PrometheusRule 未实现**:自身健康告警规则(磁盘/队列积压/事故卡死)仅 values 开关占位。
4. **kindnetd NP 限制**:egress podSelector 支持不完整,生产需 Calico/Cilium。
5. **OTel 导出**:span 生成就绪,但未接采集器(需生产配置 OTLP endpoint)。
6. **v1alpha1 已冻结**:CRD 仅 additive 变更。
7. **executor 动作集固定为 5 个**:扩展新动作需完整实现 5 个方法(MVP 约束)。

### 15.2 建议路线

- P0:通知通道(审批提醒/失败告警)+ PrometheusRule(半天~1 天)
- P1:真实 DeepSeek 评估(对照 fake 基线,报告更新 eval/)
- P2:阿里云 ACK overlay/values;Calico NP 验证
- P3:更多故障类型与 runbook;跨集群联邦

---

## 16. 附录

### 16.1 git 提交历史(21 个)

```text
61ec038 M0: 仓库与工具链
e80c33c M1: CRD + Gateway + 只读 Console
99ccfc5 M2: Controller 状态机 + Evidence
56ce178 fix: 处理 M3 前问题清单(RBAC/items:null/哈希/时钟/覆盖率)
c5656d0 M3: Diagnosis API、Worker、RAG
2125096 M4: Policy Guard + Approval
587a39f fix: 审批等待期 RV 变化不再永久卡死(恢复路径)
9f479f2 M5: 5 个 Typed Actions + Executor
e899656 M6a: 修复假回滚——执行前快照持久化
a2e9efa M6b: 修复 since 语义缺陷——事件窗口与指标/日志对齐
31e4203 M6: Audit 审计接线 + Crash Recovery + ErrTransient
c0550bf fix: ErrTransient 退避真正指数增长
aaa1756 M7: Fault Lab + Observability
8db43d4 M8: CRD 冻结——targetSelector 改 metav1.LabelSelector
8d0608c M8: ClearPhaseEphemeralStatus 接入阶段转移
a1e343b M8: Eval campaign(54 runs)+ ADR×5 + 运维文档
4fbc333 M8: 根配置(.editorconfig/.env.example)+ README 里程碑状态
e92c6be M8: Helm 部署修复(镜像模板/SA/postgres/权限)
1ca7456 M8: 集群内 E2E 全链路修复
2b65ac1 M8: executor 覆盖补到 80% + README E2E 记录
12f0898 fix: 集群内 gateway RBAC 阻断缺陷 + operator finalizer 权限
```

### 16.2 关键文件索引

| 想了解 | 看这里 |
|---|---|
| 设计蓝图 | docs/design/aegisops-implementation-blueprint.md |
| 架构图 | docs/design/aegisops-architecture.mmd |
| 架构说明 | docs/architecture.md |
| 状态机 | docs/crd-state-machine.md |
| 安全模型 | docs/security-model.md |
| API 契约 | docs/api-contracts.md |
| 运维手册 | docs/operations.md |
| 演示脚本 | docs/demo-script.md |
| 评估方法 | docs/evaluation.md |
| 架构决策 | docs/adr/0001–0005 |
| 部署 | deploy/helm/aegisops/ + README.md |
| 快速开始 | README.md |
