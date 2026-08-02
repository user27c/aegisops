# AegisOps 下一阶段超级详细实施计划

> 文档状态：待执行
>
> 目标版本：v0.2.0（工程收尾与作品集发布版）
>
> 适用范围：当前 `main` 分支中 v0.1.0/M0–M8 之后的全部收尾工作
>
> 主要读者：项目作者、后续实现该计划的 AI/开发者、代码审查者
>
> 最后更新：2026-08-02

---

## 0. 如何使用这份计划

这不是愿望清单，而是按依赖顺序编排的实施规格。每个里程碑都包含：

1. 要解决的问题；
2. 需要新增、修改或删除的具体文件；
3. 关键类型、函数及职责；
4. 单元测试、集成测试和 E2E 测试；
5. 验收命令与通过标准；
6. 文档和作品集需要保存的证据；
7. 建议提交拆分。

执行规则：

- 按第 4 节的依赖 DAG 推进；同一依赖层可以并行，但任何里程碑不得绕过自己的进入/退出条件。
- 每次提交只解决一个可验证问题，禁止把安全、功能、重构和文档混在一个提交中。
- 任何“完成”“全链路”“准确率”“MTTR”结论都必须能由脚本、测试结果或保存的实验记录复现。
- 所有生产配置必须从 YAML/Helm values/Secret 读取，不得把 SMTP、DeepSeek、收件人、阈值等写死在 Go/Python 代码中。
- 公开仓库禁止提交真实密码、SMTP 授权码、DeepSeek Key、真实邮箱 Token 或 kubeconfig。
- 配置文件可以直接填写非敏感值；敏感值使用本地不跟踪文件或 Kubernetes Secret，并在公开示例中保留占位符。

---

## 1. 最终交付目标

完成本计划后，仓库必须能够证明以下能力，而不只是描述这些能力：

### 1.1 功能目标

- Alertmanager 告警可以稳定创建并去重 `AIOpsIncident`。
- Operator 能完成取证、诊断、策略审查、审批、类型化执行、验证和回滚。
- Diagnosis API 具有真正生效的服务间 Bearer Token 鉴权。
- Diagnosis Worker 的并发上限真实生效。
- 同一 Kubernetes 目标同一时间只能有一个 Incident 持有修复锁。
- DeepSeek 能在启用 NetworkPolicy 的生产配置下访问外部 API。
- PrometheusRule 能监控 AegisOps 自身健康和故障处理状态。
- 普通预警和严重告警可以通过 Alertmanager 发送 SMTP 邮件。
- SMTP、收件人、路由、聚合和重复间隔全部由配置文件控制。
- Incident API 能提供完整证据和审计时间线，Web 控制台可以显示。

### 1.2 质量目标

- `make verify` 在干净工作区一次通过。
- `make test-integration` 运行真实 envtest/PostgreSQL/MailHog 集成测试。
- `make test-e2e` 在全新 Kind 集群完成至少两条闭环。
- GitHub Actions 的 CI、E2E、安全扫描均不是占位步骤。
- E2E 失败时自动保存 Kubernetes 对象、Pod 日志、Prometheus 指标和审计记录。
- Fake 评估与真实 DeepSeek 评估严格分开，报告不得混淆。

### 1.3 作品集目标

- 公开 GitHub 仓库可按 README 从零复现。
- 至少 6 张有效截图、1 个 5–8 分钟演示视频、1 张架构图。
- 至少 2 份真实故障演练报告、1 份安全缺陷复盘、1 篇项目复盘文章。
- 简历中的每个量化数字都能链接到原始实验记录。

---

## 2. 当前基线与禁止继续使用的表述

### 2.1 已有可信基线

- Go 单元测试全部包通过。
- Python 25 个测试通过。
- Web 14 个测试通过。
- Helm lint 通过。
- Kind 中五个 AegisOps 组件、PostgreSQL、双副本 Operator 已运行。
- PostgreSQL 中已有 Analysis、Evidence 和 Audit 数据，两条 Incident 有完整解决审计链。
- 本地 Prometheus、Loki、Grafana 已运行。

### 2.2 完成本计划前禁止在 README/简历中使用

- “E2E 全自动化通过”。
- “54 次真实故障演练”。
- “DeepSeek 根因命中率 100%”。
- “四个 Prometheus target 全部 up”。
- “完整告警通知系统”。
- “生产可用”或“生产级系统”。
- “并发锁已实现”。
- “M0–M8 全部自动验收通过”。

### 2.3 建议临时表述

> 已实现 AegisOps 核心控制面并在 Kind 中手工完成故障自愈闭环；当前正在补齐自动化 E2E、真实 DeepSeek 对照评估、邮件告警和生产化安全约束。

---

## 3. 目标架构与信任边界

```text
业务/平台指标
    │
    ▼
Prometheus ── PrometheusRule ──▶ Alertmanager ── SMTP ──▶ 邮箱
    │                                  │
    │ webhook                          │ 告警聚合/抑制/静默
    ▼                                  │
alert-gateway ──▶ AIOpsIncident CR ◀───┘
                         │
                         ▼
                Operator Controller
          ┌──────────────┼──────────────┐
          │              │              │
    Evidence         Policy/Lock     Typed Executor
 K8s/Prom/Loki       Approval        Verify/Rollback
          │              │              │
          └──────────▶ Diagnosis API ◀──┘
                         │ Bearer Token
                         ▼
                    PostgreSQL
                         ▲
                         │ claim/heartbeat
                  Diagnosis Worker
                         │ HTTPS 443
                         ▼
                      DeepSeek
```

必须保持的信任边界：

- DeepSeek 和 Diagnosis 服务没有 Kubernetes 凭据。
- Operator 没有 DeepSeek Key。
- Alertmanager 只能发送告警和邮件，不能修改工作负载。
- Incident API 只能读取 Incident/Policy 并创建 Approval。
- SMTP 密码不进入代码、不进入 Git 历史、不出现在日志中。
- LLM 输出永远是候选方案，执行必须经过 Policy Guard 和 Typed Action。

---

## 4. 里程碑和依赖顺序

| 顺序 | 里程碑 | 核心结果 | 进入条件 | 退出条件 |
|---|---|---|---|---|
| 1 | M9.0 真实性与仓库清理 | 文档、命令和仓库一致 | 当前基线 | 无占位命令、无跟踪构建缓存 |
| 2 | M9.1 安全与并发正确性 | API 鉴权、Worker 限流、目标锁 | M9.0 | 安全/并发测试通过 |
| 3A | M9.2 邮件基础设施 | Alertmanager → MailHog/SMTP，先使用 TargetDown smoke rule | M9.1 | MailHog 自动测试通过；真实邮箱留作发布门禁 |
| 3B | M9.3 可观测性与完整规则 | 指标、日志、Trace、业务告警、大盘 | M9.1、M9.2 | 所有 target up、全部业务规则可触发 |
| 3C | M9.4 Incident API 与 Console | 证据/时间线可视化 | M9.1；可与 M9.2 并行 | API 与 UI 测试通过 |
| 4 | M9.5 一键开发环境 | 全新环境可重复启动 | M9.2、M9.3、M9.4 | `dev-up`/`dev-down` 通过 |
| 5 | M9.6 自动化 E2E 与 CI | CI 可证明端到端闭环 | M9.5 | Auto/审批/回滚 E2E 稳定通过 |
| 6A | M9.7 真实 DeepSeek 评估 | A/B/C/D 对照和可信数据 | M9.6 | 报告含原始数据与置信区间 |
| 6B | M9.8 云上最终演示 | 阿里云低成本 k3s 部署 | M9.6；可与 M9.7 并行 | 演示清单与销毁流程通过 |
| 7 | M9.9 故障报告与复盘 | 可量化证据 | M9.7、M9.8 | 报告、截图、视频完成 |
| 8 | M9.10 发布冻结 | v0.2.0 可公开 | M9.0–M9.9 全部完成 | Release/README/简历一致 |

依赖图：

```text
M9.0 → M9.1 → M9.2 → M9.3 ─┐
          └──────→ M9.4 ─────┴→ M9.5 → M9.6 ─┬→ M9.7 ─┐
                                              └→ M9.8 ─┴→ M9.9 → M9.10
```

---

## 5. M9.0：真实性修正与仓库清理

### 5.1 目标

消除“文档声称存在，但实际文件、脚本或测试不存在”的问题，使任何人 clone 后看到的内容与项目描述一致。

### 5.2 文件变更

#### 修改 `.gitignore`

新增：

```gitignore
# 本地工具与缓存
/buildcache/
/fault-lab/server
/.local/

# 本地敏感配置
/deploy/observability/alertmanager/alertmanager.local.yml
/deploy/observability/alertmanager/secrets/
/deploy/helm/aegisops/values-email.local.yaml
```

#### 从 Git 跟踪中删除但不影响源码

- `buildcache/**`
- `fault-lab/server`
- 已被忽略的本地二进制、coverage 和工具缓存

执行前先确认目标是构建产物；禁止删除任何业务源码。

#### 修改 `README.md`

- 把里程碑状态从“全部完成”改为“核心 MVP 完成、M9 收尾中”。
- 快速开始在 `dev-up.sh` 完成前明确标记“暂不可一键复现”。
- 删除指向不存在的 `docs/evaluation.md` 链接，或在本里程碑创建占位但内容真实的评估说明。
- Fake 评估必须写成“确定性流水线基线”，不得写成 AI 效果数据。

#### 修改 `docs/PROJECT-COMPLETE.md`

- 文件重命名建议：`docs/PROJECT-STATUS-v0.1.md`。
- 明确文档是 v0.1 历史快照，不再作为当前唯一权威状态。
- 将“全完成”改为“手工验收记录”，并链接本计划。

#### 新增 `docs/implementation-status.md`

维护一张事实表：

| 能力 | Implemented | Unit | Integration | E2E | Real environment | Evidence |
|---|---:|---:|---:|---:|---:|---|

每项只能使用 `yes/no/partial`，不得用模糊的“基本完成”。

#### 新增 `scripts/check-repo-hygiene.sh`

函数/步骤：

- `check_for_tracked_cache()`：如果 `git ls-files buildcache` 非空则失败。
- `check_for_large_binaries()`：检查超过 5 MiB 的跟踪文件，只允许白名单中的文档图片。
- `check_required_docs()`：验证 README 引用的本地 Markdown 文件存在。
- `check_placeholder_markers()`：扫描 `M8 填充`、`后续里程碑填充`、`当前为空占位`。
- `main()`：汇总错误并返回非零退出码。

脚本只读，不自动删除文件。

### 5.3 修复 Makefile 真实性

本里程碑先做两件事：

- 未实现的 target 必须明确失败，而不是 `echo` 后返回成功。
- 后续里程碑实现后再改为真实命令。

临时行为示例：

```make
test-e2e:
	@echo "E2E 尚未实现，见 docs/NEXT-STEPS-IMPLEMENTATION-PLAN.md" >&2
	@exit 1
```

必须修复：

- `make build-images` 补齐 `REGISTRY` 参数校验。
- `make eval` 暂时指向现有 `eval/run_campaign.py`，并明确只支持 fake。
- `make eval-report` 不得指向不存在的 `report.py`。
- `make verify` 的说明必须与真实依赖一致。
- `runbooks-validate` 必须真正执行 JSON Schema 校验，不能只打印文件名。

### 5.4 测试

- `scripts/check-repo-hygiene.sh` 正常仓库返回 0。
- 临时创建一个假大文件时脚本返回非 0，清理后恢复。
- README 中添加不存在链接时测试必须失败。

### 5.5 验收

```bash
git status --short
scripts/check-repo-hygiene.sh
git ls-files 'buildcache/**'
git ls-files fault-lab/server
rg -n 'M8 里程碑填充|后续里程碑填充|当前为空占位' . \
  --glob '!docs/PROJECT-STATUS-v0.1.md'
```

通过标准：

- 工作区干净。
- buildcache 和二进制不再被 Git 跟踪。
- 所有公开命令要么真实工作，要么清晰失败。
- 文档没有悬空链接。

### 5.6 建议提交

1. `chore: 移除仓库中的构建缓存和二进制产物`
2. `chore: 增加仓库完整性检查脚本`
3. `docs: 修正 v0.1 完成状态与失效链接`
4. `build: 让未实现 target 显式失败`

---

## 6. M9.1：安全与并发正确性

### 6.1 Diagnosis API Bearer Token 鉴权

#### 问题

Go 客户端已经发送 `Authorization: Bearer ...`，但 FastAPI 没有校验；`api_token` 配置存在但未接入路由。

#### 新增 `services/diagnosis/app/security.py`

```python
class AuthenticationError(Exception): ...

def load_api_token(settings: Settings) -> bytes:
    """优先读取 api_token_file；开发测试可显式注入 api_token。空值 fail-closed。"""

def parse_bearer_header(value: str | None) -> str:
    """只接受 Bearer scheme；缺失、空 Token、其他 scheme 均拒绝。"""

def verify_token(candidate: str, expected: bytes) -> bool:
    """SHA256 后使用 hmac.compare_digest，避免直接字符串比较。"""

async def require_service_token(
    authorization: Annotated[str | None, Header()] = None,
    settings: Settings = Depends(get_request_settings),
) -> None:
    """验证服务间 Token，失败统一返回 401，不泄露失败原因。"""
```

约束：

- `/healthz`、`/readyz` 不要求认证。
- `/v1/**` 全部要求认证。
- 日志不得记录完整 Authorization Header 或 Token。
- 401 返回固定 JSON，避免区分“未配置”和“Token 错误”。
- API Token 文件最大 4 KiB，读取后 `strip()`，空文件拒绝启动或使 readyz 失败。

#### 修改 `services/diagnosis/app/config.py`

新增：

```python
api_token_file: str = "/run/secrets/diagnosis-token"
allow_insecure_no_auth: bool = False
```

行为：

- `allow_insecure_no_auth` 只能在 `environment=development` 使用。
- Helm 默认必须启用鉴权。
- API 进程只调用 `validate_api_runtime()` 验证 DB、API Token 和环境模式；不得要求或读取 DeepSeek Key。
- Worker 进程只调用 `validate_worker_runtime()` 验证 DeepSeek/Embedding；两类进程职责按 6.5 节拆分。

#### 修改 `services/diagnosis/app/api/__init__.py`

- 健康路由保持公开。
- 创建带 `dependencies=[Depends(require_service_token)]` 的 `/v1` 子 Router。
- analyses/audit/runbooks 全部挂载在受保护 Router 下。

#### Helm 修改

- `diagnosis-api-deployment.yaml` 挂载与 Operator 相同的 `aegisops-diagnosis-token` Secret。
- 增加 `DIAGNOSIS_API_TOKEN_FILE=/run/secrets/diagnosis-token`。
- Operator、Incident API、Diagnosis API 使用同一 Secret name/key 契约，由 Helm 安装前置 Secret 或 external secret 创建。
- non-root 容器使用固定 UID/GID 65532；Secret volume 使用 `defaultMode: 0440` 配合 Pod `fsGroup: 65532`，不得使用 root-owned 0400 导致不可读。
- 文档定义 Token 轮换：先让客户端和服务端接受 new token，滚动更新调用方，再移除 old token；MVP 若只支持单 Token，则执行受控全组件滚动并记录短暂不可用窗口。
- `values.schema.json` 校验 Secret 名非空。

#### 测试 `services/diagnosis/tests/unit/test_security.py`

至少包含：

- 无 Header → 401。
- Basic scheme → 401。
- `Bearer` 后为空 → 401。
- 错误 Token → 401。
- 正确 Token → 能进入业务路由。
- Token 前后空白处理符合约定。
- healthz 无 Token → 200。
- 不配置 Token 且非 development → readyz 503 或启动失败。
- 认证失败日志不包含 Token 原文。

### 6.2 修复 Diagnosis Worker 并发上限

#### 修改 `services/diagnosis/app/worker.py`

当前 semaphore 在创建 task 后立即释放，不能约束在途任务。重构为容量驱动循环：

```python
async def wait_for_capacity(
    tasks: set[asyncio.Task[None]],
    concurrency: int,
) -> None:
    """在任务数达到上限时等待至少一个任务结束，并传播/记录异常。"""

def discard_finished(tasks: set[asyncio.Task[None]]) -> None:
    """移除已完成任务并读取 exception，避免 Task exception was never retrieved。"""

async def claim_one(deps: WorkerDependencies, worker_id: str) -> AnalysisJob | None:
    """在独立短事务内领取一个任务。"""

async def worker_loop(...) -> None:
    while not stop.is_set():
        await wait_for_capacity(tasks, settings.worker_concurrency)
        job = await claim_one(...)
        if job is None:
            await wait_or_stop(stop, poll_interval)
            continue
        tasks.add(asyncio.create_task(...))
```

新增配置校验：`worker_concurrency` 只能为 1–32。

#### 测试 `services/diagnosis/tests/unit/test_worker_concurrency.py`

- 构造 20 个任务、并发上限 2，记录同时运行峰值，必须 `peak == 2`。
- 一个任务抛异常不阻塞后续领取。
- stop 信号后不再领取新任务。
- 优雅退出等待在途任务。
- 超过 35 秒的任务被取消。
- stale job 重新排队时 attempt 不越过上限。

### 6.3 同一目标的 Lease 修复锁

#### 新增 `internal/targetlock/lock.go`

```go
type TargetKey struct {
    Cluster   string
    Namespace string
    Kind      string
    Name      string
}

type Handle struct {
    LeaseName      string
    HolderIdentity string
    ExpiresAt      time.Time
}

type Manager interface {
    Acquire(ctx context.Context, incident *AIOpsIncident) (Handle, error)
    Renew(ctx context.Context, incident *AIOpsIncident, handle Handle) (Handle, error)
    Release(ctx context.Context, incident *AIOpsIncident, handle Handle) error
}

func LeaseName(key TargetKey) string
func HolderIdentity(incident *AIOpsIncident) string
```

Lease 命名：

```text
aegis-target-<sha256(cluster|namespace|kind|name) 前 20 位>
```

Lease annotations 保存可读 Target，但不得依赖 annotation 作为锁键。

#### 新增 `internal/targetlock/kubernetes.go`

实现规则：

- 进入 `Executing` 前 Acquire。
- 从 `Executing` 进入直到终态/释放期间，由 manager Runnable 为所有本实例持有的 Lease 建立独立续租循环；不能只依赖下一次 Reconcile。
- `Verifying` 和 `RollingBack` 每次 Reconcile 仍做一次同步 Renew/fencing check，作为第二层保护。
- Resolved/RolledBack/Escalated/RecoveredWithoutAction 时 Release。
- Holder 为 Incident UID，不使用 Incident name。
- 未过期且 holder 不同：返回 `ErrTargetLocked`，当前 Incident 保持阶段并 10 秒后重试。
- Lease 过期：通过 resourceVersion 乐观并发更新接管。
- 默认租约 60 秒，20 秒内续约。
- 每次 Snapshot/Apply/Rollback 等资源写入前执行 `AssertHeld(resourceVersion, holderIdentity)`；失锁后禁止继续写目标。
- Handle 增加 fencing token（可使用 Lease transitions + resourceVersion）；旧 holder 即使晚到也不能覆盖新 holder。
- Lease 放在目标 namespace；跨 namespace target 时按 target namespace 创建，并在 Helm RBAC 中只授予受管 namespace 的 Lease 权限。
- 任一 Typed Action 的单次外部调用/写操作必须小于续租失败宽限；超过时间的操作需要可取消 context。
- 连续续租失败后：停止新的 Apply/Rollback，记录 `TargetLockLost`，进入 Escalated；不得在已失锁时自动回滚并与新 holder 冲突。
- 删除 Incident 的 finalizer 流程必须尝试释放 Lease。
- 即使 Release 失败也不能直接删除 finalizer；先重试，超过策略上限再记录警告并由过期机制兜底。

#### 修改 CRD Status

在 `AIOpsIncident.status.execution` 中增加：

```go
TargetLock *TargetLockReference `json:"targetLock,omitempty"`
```

字段：`leaseName`、`holderIdentity`、`acquiredAt`、`renewTime`。

只做 additive 变更，重新生成 deepcopy/CRD/Helm CRD。

#### 修改 Controller

- `IncidentReconciler` 新增 `TargetLock targetlock.Manager`。
- `handleExecuting` 在 Snapshot/Apply 前验证锁。
- `handleVerifying`、`handleRollingBack` 先 Renew。
- 终态转换统一调用 `releaseTargetLockBestEffort`；执行关键路径获取锁失败必须 fail-closed。
- 记录 `TargetLockContended` condition/timeline。
- 新增指标：
  - `aegisops_target_lock_acquire_total{result}`
  - `aegisops_target_lock_contention_total`
  - `aegisops_target_lock_held_seconds`

#### Go 测试

- 相同 Target、不同 Incident：第二个不能进入 Apply。
- 不同 Target：可以并发。
- 同 Incident 重入：Acquire 幂等。
- Lease 过期：新 Incident 可接管。
- 旧 holder 不能释放新 holder 的 Lease。
- Controller 重启后根据 Status/Lease 续约。
- 模拟 Apply 阻塞超过 leaseDuration，独立续租保持 holder 不被接管。
- 模拟续租失败和新 holder 接管，旧 holder 的 fencing check 阻止写入。
- Rollback 阶段仍持锁。
- 终态释放。
- 删除流程释放。
- fake client 上 Conflict 时正确重试。

### 6.4 允许真实 DeepSeek 的 NetworkPolicy

#### 修改 Helm values

```yaml
diagnosis:
  externalLLM:
    enabled: false
    mode: proxy # proxy|cidr；生产优先 proxy
    allowedCIDRs: []
    proxy:
      namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: egress-system
      podSelector:
        matchLabels:
          app.kubernetes.io/name: egress-proxy
      port: 8443
      url: "http://egress-proxy.egress-system.svc:8443"
```

#### 修改 `networkpolicies.yaml`

- `externalLLM.enabled=false`：worker 保持仅 PG/DNS。
- `mode=proxy`：NetworkPolicy 直接使用结构化 namespaceSelector/podSelector/port；应用的 HTTP(S)_PROXY 使用 proxy.url。URL 本身不参与策略生成。
- `mode=cidr`：只允许显式 `allowedCIDRs` + TCP 443；空列表直接 schema 失败，不提供隐式 0.0.0.0/0。
- allowedCIDRs 不能覆盖 cluster/service/pod CIDR、RFC1918、loopback、link-local、云 metadata；至少排除通用 `169.254.169.254/32` 和阿里云 `100.100.100.200/32`。
- 明确 IPv4/IPv6 行为；环境未启用 IPv6 时禁止产生不受策略覆盖的 IPv6 外连。
- DNS 只放行 kube-system 中带 `k8s-app=kube-dns` 的 CoreDNS/kube-dns Pod，TCP/UDP 53；不能向任意 namespace 放行 53。
- 文档记录标准 NetworkPolicy 无 FQDN 控制，阿里云演示优先使用 Cilium FQDN policy 或 egress proxy。

#### 验收

- NetworkPolicy 开启时 worker 能访问 DeepSeek smoke endpoint。
- worker 不能访问 Kubernetes API ClusterIP。
- Diagnosis Pod 中不存在 ServiceAccount token。
- 未开启 externalLLM 时外部 443 被拒绝。
- Kind 的该组测试使用支持 NetworkPolicy 的 CNI（Calico/Cilium），默认 kindnet 不作为 NetworkPolicy 验收环境。

### 6.5 拆分 API/Worker Secret 与修复 Embedding Cache

#### 问题

- Diagnosis API 不调用模型，却在当前 Helm Deployment 中被注入 DeepSeek Key，扩大了 Secret 暴露面。
- 当前 `embedding.cachePVC` 同时被当成 PVC 名和 `EMBEDDING_CACHE_DIR` 路径，语义错误。
- Worker 使用只读 root filesystem，但没有真正挂载可写模型缓存卷；真实 sentence-transformers 模式可能无法下载/加载模型。

#### 配置重构

```yaml
diagnosis:
  api:
    enabled: true
    # 只需要 DB 和 service token，不需要 DeepSeek Key/embedding model。

  worker:
    enabled: true
    llmProvider: deepseek
    deepseekExistingSecret: deepseek-api
    deepseekKeyFile: /run/secrets/deepseek/api-key

  embedding:
    model: BAAI/bge-small-zh-v1.5
    cache:
      mountPath: /data/models
      existingClaim: ""
      create: true
      size: 2Gi
      storageClassName: ""
    prefetch:
      enabled: true
      # 真实 embedding + 空 PVC 时必须启用；或改用已预热 existingClaim。
```

#### 修改 Python 配置验证

- 新增 `environment: development|test|production`。
- 将通用的 `validate_production()` 拆为 `validate_api_runtime()` 和 `validate_worker_runtime()`。
- API 生产校验：DB、service token、禁止 insecure auth；不要求 DeepSeek Key。
- Worker 生产校验：provider、DeepSeek Key、HTTPS base URL、embedding cache 可写/模型可加载。
- API/Worker 启动入口必须真正调用对应校验函数，不能只定义不用。

#### Helm/容器修改

- `diagnosis-api-deployment.yaml` 删除 `DEEPSEEK_API_KEY` 和 embedding model/cache 环境变量。
- `diagnosis-worker-deployment.yaml` 独占 DeepSeek Secret，优先以 0440 文件挂载并使用 `DEEPSEEK_API_KEY_FILE`，不把 Key 放入环境变量。
- 新增 PVC 模板或引用 existingClaim，并挂载到 `cache.mountPath`。
- 设置 `HF_HOME`、`SENTENCE_TRANSFORMERS_HOME` 到挂载目录。
- 如需要 `/tmp`，挂载有 sizeLimit 的 `emptyDir`，保持 root filesystem 只读。
- 不在镜像中写入 API Key；不为 MailHog/Alertmanager 制作自定义镜像，使用固定版本上游镜像。
- real embedding + 新 PVC 时，`prefetch.enabled=true` 或已预热 `existingClaim` 二者必须满足其一，Helm schema/启动检查否则直接失败。
- Prefetch 使用独立 Job 从允许的模型制品源下载、校验 revision/hash 后写 PVC；生产更推荐预热 PVC、镜像内置模型或内部制品，避免每次 Pod 启动访问公网。
- NetworkPolicy 必须为 prefetch Job 配置独立、临时的制品出口；Worker 正常运行时不需要访问公共模型仓库。
- 内部 PostgreSQL 密码由安装前置 Secret/生成 Job 提供，不在 values 中使用固定凭据；Secret 生命周期与备份恢复写入 operations 文档。

#### 测试

- Helm 渲染断言 Diagnosis API 环境中不存在 `DEEPSEEK_API_KEY`。
- Worker 有 Key SecretKeyRef 和模型卷挂载。
- fake embedding 模式不要求 PVC。
- real embedding 模式缺少可写 cache 时 fail-closed 并使 ready/worker startup 失败。
- real embedding 模式在空 cache 且 prefetch disabled 时 Helm/schema 测试失败，不允许产生永远无法启动的 Worker。
- 模型缓存已存在时重启 Worker 不重复下载。
- Pod securityContext 保持 non-root、drop all capabilities、readOnlyRootFilesystem。

### 6.6 M9.1 总体验收

```bash
go test ./internal/targetlock/... ./internal/controller/... -race
cd services/diagnosis && uv run pytest tests/unit/test_security.py -q
cd services/diagnosis && uv run pytest tests/unit/test_worker_concurrency.py -q
helm lint deploy/helm/aegisops
helm template aegisops deploy/helm/aegisops > /tmp/aegisops-rendered.yaml
```

保存证据：

- `docs/postmortems/diagnosis-api-auth-bypass.md`
- `docs/postmortems/worker-concurrency-limit.md`
- `docs/postmortems/target-remediation-race.md`

### 6.7 建议提交

1. `security: 为 diagnosis v1 API 增加服务间鉴权`
2. `fix: 让 diagnosis worker 并发上限真实生效`
3. `feat: 增加目标级 Lease 修复锁`
4. `security: 为 DeepSeek 配置受控外部出口`
5. `security: 将 DeepSeek Key 限定在 worker 并修复模型缓存卷`
6. `docs: 记录三项安全与并发缺陷复盘`

---

## 7. M9.2：配置驱动的普通预警邮件系统

### 7.1 技术决策

使用云原生标准链路：

```text
AegisOps/Diagnosis/Fault Lab 指标
        → PrometheusRule
        → Alertmanager 路由、聚合、抑制、静默
        → SMTP Email
```

不新增自研“邮件微服务”，原因：

- Alertmanager 已经负责告警去重、分组、静默和重发。
- SMTP 失败重试、resolved 通知和告警模板已有成熟实现。
- 更符合 SRE/DevOps 岗位的工程实践。
- 可以把所有告警配置放在 YAML，而不是写入 Go/Python。

本里程碑的“普通预警”定义：AegisOps 自身健康、处理能力、故障演练服务健康以及需要人工介入的状态告警。业务 Incident 的智能诊断链路保持不变。

实施分两段：

- M9.2A 先完成 Alertmanager、模板、Secret 和 MailHog 自动验证，只要求一个不依赖新增业务指标的 `AegisOpsTargetDown` 测试规则；真实 SMTP smoke 留到 M9.10 发布门禁。
- 依赖 queue depth、Incident phase age、audit failure 等新指标的完整业务规则在 M9.3 实现并验收；它们列在本节是为了集中说明邮件需求，不构成 M9.2A 的阻塞条件。

### 7.2 配置文件设计

#### 新增 `deploy/helm/aegisops/values-email.example.yaml`

用户复制为 `values-email.local.yaml` 后直接填写配置。示例结构：

```yaml
alerting:
  enabled: true

  smtp:
    smarthost: "smtp.qq.com:587"
    from: "your-sender@qq.com"
    auth:
      enabled: true
      username: "your-sender@qq.com"
      passwordSecret:
        name: "aegisops-smtp"
        key: "password"
    requireTLS: true

  email:
    to:
      - "your-receiver@example.com"
    sendResolved: true
    subjectPrefix: "[AegisOps]"

  route:
    groupBy: ["cluster", "namespace", "alertname", "severity"]
    groupWait: 30s
    groupInterval: 5m
    repeatInterval: 4h
    criticalRepeatInterval: 30m

  rules:
    warningFor: 5m
    criticalFor: 2m
    reconcileErrorRate: 0.10
    diagnosisFailureRate: 0.10
    diagnosisQueueBacklog: 10
    approvalWaitSeconds: 600
    incidentStuckSeconds: 900
```

注意：

- 邮箱、SMTP host、阈值、路由都直接填在 YAML。
- SMTP 授权码不进入 values；由 `smtp.auth.passwordSecret` 引用 Kubernetes Secret。
- AlertmanagerConfig 中 `authPassword` 引用的 Secret 必须与 AlertmanagerConfig 位于同一 namespace。
- `email.to` 在 Helm 模板中用逗号连接为 Alertmanager 接受的 RFC 5322 地址字符串。
- 本地测试可以把授权码写入 `.local/secrets/smtp-password`，该目录必须 gitignore。
- 示例文件只允许占位值，不允许真实邮箱密码。

#### 新增 `deploy/observability/alertmanager/alertmanager.example.yml`

用于本地 Docker Compose。用户复制为 `alertmanager.local.yml`：

```yaml
global:
  resolve_timeout: 5m
  smtp_smarthost: smtp.qq.com:587
  smtp_from: your-sender@qq.com
  smtp_auth_username: your-sender@qq.com
  smtp_auth_password_file: /etc/alertmanager/secrets/smtp-password
  smtp_require_tls: true

route:
  receiver: email-warning
  group_by: [cluster, namespace, alertname, severity]
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h
  routes:
    - receiver: email-critical
      matchers: ['severity="critical"']
      repeat_interval: 30m

receivers:
  - name: email-warning
    email_configs:
      - to: your-receiver@example.com
        send_resolved: true
        headers:
          Subject: '{{ template "email.aegisops.subject" . }}'
        html: '{{ template "email.aegisops.html" . }}'
  - name: email-critical
    email_configs:
      - to: your-receiver@example.com
        send_resolved: true
        headers:
          Subject: '{{ template "email.aegisops.subject" . }}'
        html: '{{ template "email.aegisops.html" . }}'

inhibit_rules:
  - source_matchers: ['severity="critical"']
    target_matchers: ['severity="warning"']
    equal: [cluster, namespace, alertname]

templates:
  - /etc/alertmanager/templates/*.tmpl
```

该文件是“真实 SMTP 示例”，不作为本地/CI 自动化测试默认配置。Compose 的 real-smtp profile 必须把 `.local/secrets/smtp-password` 以只读文件挂载到示例中的绝对路径。

#### 新增 `deploy/observability/alertmanager/alertmanager.mailhog.yml`

这是本地/CI 唯一默认配置，使用秒级 interval，避免测试等待 30 分钟或 4 小时：

```yaml
global:
  resolve_timeout: 30s
  smtp_smarthost: mailhog:1025
  smtp_from: aegisops-test@example.invalid
  smtp_require_tls: false

route:
  receiver: email-test
  group_by: [cluster, namespace, alertname, severity]
  group_wait: 1s
  group_interval: 2s
  repeat_interval: 10s

receivers:
  - name: email-test
    email_configs:
      - to: receiver@example.invalid
        send_resolved: true
        headers:
          Subject: '{{ template "email.aegisops.subject" . }}'
        html: '{{ template "email.aegisops.html" . }}'

templates:
  - /etc/alertmanager/templates/*.tmpl
```

自动化测试只允许使用保留域 `example.invalid` 和 MailHog 内部 SMTP。真实配置必须同时选择 `real-smtp` profile 并传 `--allow-real-email`。

#### 新增 `deploy/observability/alertmanager/templates/email.tmpl`

定义：

- `email.aegisops.subject`
- `email.aegisops.html`
- `email.aegisops.text`

邮件必须包含：

- 状态：FIRING/RESOLVED。
- severity、cluster、namespace、alertname。
- 告警开始时间与持续时间。
- summary、description、runbook URL、Grafana URL。
- 受影响实例数量。
- “这封邮件只提供预警，不代表 AI 已执行修复”的提示。
- HTML 转义，不能把 label/annotation 当可信 HTML。

禁止在邮件中包含：

- DeepSeek Key、SMTP Token、Authorization Header。
- 未脱敏的日志正文。
- Kubernetes Secret 值。

### 7.3 PrometheusRule 文件

#### 唯一规则源码：`deploy/helm/aegisops/templates/prometheusrule.yaml`

不再单独维护一份手写 `deploy/observability/rules/aegisops.rules.yml`，避免 Chart 与 standalone 文件漂移。promtool 使用从 Helm 渲染结果提取的临时 rule 文件。

规则组 `aegisops-self-health`：

1. `AegisOpsTargetDown`
   - 表达式：`up{job=~"aegisops-.*|fault-lab"} == 0`
   - `for: 2m`
   - severity：critical
   - 用途：组件抓取失败。

2. `AegisOpsReconcileErrorRateHigh`
   - 分子：5 分钟 Reconcile error rate。
   - 分母：controller reconcile 总数；必须防除零。
   - `for: 5m`
   - severity：warning。

3. `AegisOpsDiagnosisFailureRateHigh`
   - 10 分钟 `failed / (succeeded + failed)` > 配置阈值；queued/processing 不进入完成分母，分母为 0 时结果为 0。
   - `for: 5m`。

4. `AegisOpsDiagnosisQueueBacklog`
   - queued + stale jobs 超过配置阈值。
   - `for: 10m`。

5. `AegisOpsAuditWriteFailure`
   - 任何 critical audit 写入失败立即告警。
   - severity：critical。

6. `AegisOpsRollbackTriggered`
   - `increase(aegisops_remediation_total{result="rollback"}[5m]) > 0`
   - severity：critical。

7. `AegisOpsIncidentEscalated`
   - 5 分钟内出现 Escalated outcome。
   - severity：warning 或按 Incident severity 映射。

8. `AegisOpsVerificationFailureHigh`
   - 10 分钟 Unhealthy 比例超过 30%。
   - severity：warning。

9. `AegisOpsNoIncidentProgress`
   - 当前活跃 Incident 存在且 15 分钟无 phase transition。
   - 需要 M9.3 新指标支持。

10. `AegisOpsApprovalWaitingTooLong`
    - AwaitingApproval 聚合数量大于 0 且超过 10 分钟。
    - severity：warning。

规则组 `aegisops-capacity`：

- Diagnosis Worker 队列积压。
- PostgreSQL 连接池使用率高。
- Evidence 数据增长异常。
- DeepSeek 429/5xx 比率高。
- SMTP/Alertmanager 通知失败率高。

所有规则 annotations 统一包含：

```yaml
summary: "短标题"
description: "当前值、阈值和可能影响"
runbook_url: "https://.../runbooks/<name>"
dashboard_url: "https://.../grafana/d/aegisops-overview"
```

### 7.4 Helm 模板

#### 新增 `deploy/helm/aegisops/templates/prometheusrule.yaml`

- 这是生产规则唯一源码，不在 Chart 外复制第二份规则。
- 仅在 `observability.prometheusRule=true` 且 CRD 可用时渲染。
- 规则阈值从 values 读取。
- 公共 labels 由 helper 生成。
- 新增 `scripts/render-prometheus-rules.sh`：执行 `helm template`，从 PrometheusRule `.spec.groups` 提取为临时规则文件，再供 promtool check/test；脚本不得修改仓库文件。
- 加入 `cluster={{ .Values.global.clusterID }}` external label 或规则 label。

#### 新增 `deploy/helm/aegisops/templates/alertmanagerconfig.yaml`

- 使用 `monitoring.coreos.com/v1alpha1 AlertmanagerConfig`。
- SMTP 用户名、from、to、TLS、路由间隔来自 values。
- 密码使用 SecretKeySelector。
- warning/critical 分 receiver。
- `sendResolved` 可配置。
- 当 `alerting.enabled=false` 时不渲染。
- 邮件主题的动态部分保存在 `email.tmpl`；values 只保存 `subjectPrefix`，避免 Helm 和 Alertmanager 双层模板转义失控。
- kube-prometheus-stack values 必须配置 `alertmanagerConfigSelector` 选择 AegisOps labels，并配置 `alertmanagerConfigNamespaceSelector` 允许该 namespace。
- 明确 Prometheus Operator 对 AlertmanagerConfig 的 namespace matcher 策略，确保 AegisOps 告警 labels 能匹配 receiver。
- E2E 不只检查 CR 存在，还要检查 Alertmanager 最终运行配置已经包含 email receiver/route。

#### 新增 `deploy/helm/aegisops/templates/alertmanager-email-template-configmap.yaml`

- 把 `email.tmpl` 安装为 ConfigMap。
- kube-prometheus-stack values 通过当前版本 Alertmanager CR 支持的 `configMaps` 字段显式挂载模板；实现时以已安装 CRD schema/官方 API 为准。
- E2E 在 Alertmanager Pod 内验证模板文件存在，并通过通知 smoke 证明模板已成功解析。

#### 修改 `values.schema.json`

验证：

- 开启 alerting 时 `smtp.smarthost/from` 非空。
- `smtp.auth.enabled=true` 时 username/Secret name/key 必填；MailHog profile 设置 false，不创建假密码 Secret。
- `email.to` 至少一个且格式基本合法。
- duration 使用 `^[0-9]+(s|m|h)$`。
- 比率在 0–1。
- backlog 为非负整数。
- password Secret name/key 非空。

### 7.5 本地测试环境

#### 新增 `deploy/observability/docker-compose.alerting.yml`

包含：

- Alertmanager 固定版本。
- MailHog/Mailpit 固定版本，用于接收测试邮件。
- 不默认连接真实 SMTP，避免执行测试时误发邮件。
- 配置文件和模板只读挂载。
- healthcheck。

端口建议：

- Alertmanager：`19093:9093`
- Mail UI：`18025:8025`
- Mail SMTP：内部 `1025`

#### 新增 `scripts/alerting-up.sh`

函数：

- `require_tools docker curl`。
- `validate_alertmanager_config()`。
- `start_mail_sink()`。
- `wait_for_health()`。
- 打印访问地址，不打印任何密码。
- 默认强制使用 `alertmanager.mailhog.yml`；检测到非 MailHog smarthost 且没有 `--allow-real-email` 时直接失败。

#### 新增 `scripts/alerting-down.sh`

- 必须带项目名，只删除 AegisOps alerting compose 资源。
- 不使用宽泛 `docker system prune`。

#### 新增 `scripts/send-test-alert.sh`

参数：

```text
--alertmanager-url
--severity warning|critical
--status firing|resolved
--name <alertname>
--namespace <namespace>
```

行为：

- 生成标准 Alertmanager v2 API payload。
- 默认发到本地 MailHog 链路。
- 真实 SMTP 必须额外传 `--allow-real-email`。
- 不允许把真实 SMTP 密码作为命令行参数。
- 该脚本只用于通知链路 smoke；生产告警必须由 PrometheusRule 产生，不允许业务代码直接调用 Alerts API。

#### 新增 `scripts/assert-test-email.py`

通过 MailHog API 验证：

- 收到一封邮件。
- subject 含 severity/alertname。
- 正文含 summary/runbook/dashboard。
- resolved 后收到恢复邮件。
- critical 抑制同组 warning。

### 7.6 规则测试

#### 新增 `deploy/observability/tests/aegisops.rules.test.yml`

使用 `promtool test rules`，每条规则至少覆盖：

- 未达到阈值不触发。
- 刚达到阈值但未满足 `for` 不触发。
- 满足阈值和持续时间后触发。
- 恢复后不再 firing。
- 缺少分母数据时不产生 NaN/误报。
- labels/annotations 完整。

#### 新增 `tests/integration/alertmanager_email_test.py`

测试：

- warning 邮件发送。
- critical 邮件发送。
- resolved 邮件发送。
- group_wait 合并重复告警。
- repeat_interval 内不重复轰炸。
- inhibition 生效。
- SMTP 暂时失败后 Alertmanager 重试。
- 模板中的恶意 HTML label 被转义。

### 7.7 真实邮箱 Smoke

只做发布前手工 smoke，不纳入公共 CI，也不阻塞 M9.3–M9.8 的开发；它是 M9.10 发布证据门禁：

1. 复制 `values-email.example.yaml` 为被 gitignore 的 local 文件。
2. 创建 `aegisops-smtp` Secret。
3. 发送唯一测试告警 `AegisOpsEmailSmokeTest`。
4. 验证 firing 邮件。
5. 发送 resolved。
6. 验证 resolved 邮件。
7. 保存脱敏截图，隐藏完整邮箱地址和 Message-ID。

### 7.8 邮件告警验收标准

- SMTP/收件人/路由/阈值均可仅修改 YAML 生效，无需重新编译代码。
- 本地配置通过 `amtool check-config` 后才 reload/restart；集群配置由 Prometheus Operator reconciliation 触发更新。
- MailHog 集成测试可重复运行。
- M9.2 退出条件：MailHog firing/resolved/inhibition/repeat 测试通过；真实邮箱不是本里程碑硬依赖。
- M9.10 发布条件：真实邮箱 firing/resolved smoke 成功并保存脱敏证据。
- warning 在 4 小时内不重复轰炸；critical 按 30 分钟重复。
- critical 能抑制同组 warning。
- SMTP 失败不会影响 Operator Reconcile 或自愈链路。
- 邮件发送失败本身有 Alertmanager 指标可观测。

### 7.9 建议提交

1. `feat: 增加配置驱动的 AegisOps Prometheus 告警规则`
2. `feat: 增加 Alertmanager SMTP 邮件配置与模板`
3. `test: 增加 promtool 与 MailHog 邮件集成测试`
4. `docs: 增加邮件告警配置和排障手册`

---

## 8. M9.3：可观测性闭环

### 8.1 目标

不仅“暴露 `/metrics`”，而是确保每个组件可被抓取、关键 SLI 可计算、告警可触发、日志可关联、Trace 可查询，并能在演示时用一张大盘解释整个 Incident 生命周期。

### 8.2 Go 指标补齐

#### 修改 `internal/observability/metrics.go`

新增低基数指标：

```go
ActiveIncidents          *prometheus.GaugeVec      // labels: phase,severity
OldestIncidentAgeSeconds *prometheus.GaugeVec      // labels: phase,severity
PhaseTransitions         *prometheus.CounterVec    // labels: from,to
EvidenceCollections      *prometheus.CounterVec    // labels: source,result
EvidenceItems            *prometheus.HistogramVec  // labels: source
AuditWrites              *prometheus.CounterVec    // labels: severity,result
TargetLockAcquire        *prometheus.CounterVec    // labels: result
TargetLockContention     prometheus.Counter
NotificationHints        *prometheus.CounterVec    // labels: kind，不负责真正发邮件
```

约束：

- 禁止 Incident UID/name、Pod name、错误文本作为 metric label。
- phase、severity、action、result 使用固定枚举。
- 错误原因如果种类不可控，只记录到日志/Trace，不放 label。
- 注册测试必须保证重复创建 Metrics 不 panic。

#### 修改 Controller

- 所有状态转换统一调用 `ObserveTransition(from, to, severity)` 记录 Counter/Histogram。
- `ActiveIncidents` 和 `OldestIncidentAgeSeconds` 不做增量加减；新增 `IncidentMetricsReconciler` 每 30 秒 List 后整体重新计算，避免 Reconcile 重放造成 Gauge 漂移。
- oldest age 使用该 phase 最近一次 timeline transition 时间，而不是对象 creationTimestamp。
- 指标更新失败不得影响业务状态机。

### 8.3 Diagnosis 服务指标

#### 新增 `services/diagnosis/app/metrics.py`

定义：

- `aegisops_diagnosis_jobs_total{status}`。
- `aegisops_diagnosis_queue_depth{status}`。
- `aegisops_diagnosis_job_duration_seconds{result}`。
- `aegisops_llm_requests_total{provider,operation,result}`。
- `aegisops_llm_request_duration_seconds{provider,operation}`。
- `aegisops_llm_tokens_total{provider,direction}`。
- `aegisops_rag_retrieval_duration_seconds{result}`。
- `aegisops_rag_candidates{stage}`。
- `aegisops_audit_write_total{severity,result}`。
- `aegisops_db_pool_checked_out`。

函数：

```python
def observe_llm_call(provider, operation, result, duration, usage) -> None: ...
def observe_job_transition(old_status, new_status) -> None: ...
async def refresh_queue_gauges(session_factory) -> None: ...
def metrics_response() -> Response: ...
```

新增 `/metrics`，不要求 Bearer Token，但只能由 NetworkPolicy/ServiceMonitor 访问。

测试：

- fake/DeepSeek 成功、429、timeout 分别计数。
- Token 数只累计数值，不记录 prompt。
- job 重试不重复计算 completed。
- `/metrics` 不输出 Secret 或证据文本。

### 8.4 Prometheus 抓取与 target 健康

#### 修改 Helm

- ServiceMonitor 增加 Incident API、Diagnosis API、fault-lab。
- Operator/Gateway/API/Diagnosis 使用一致 `release` label 选择方式。
- `interval`、`scrapeTimeout` 从 values 配置。
- 如果 metrics 没有 TLS，明确仅集群内部访问。
- 对 ServiceMonitor CRD 不存在的情况：模板必须明确失败或通过 capabilities 条件跳过并输出 NOTES，不得悄悄声称已安装。

#### 新增 `scripts/check-prometheus-targets.sh`

参数：`--url`、`--expected-job` 可重复、`--timeout`。

功能：

- 调用 `/api/v1/targets?state=active`。
- 验证预期 job 存在且 `health=up`。
- 输出 down target 的 lastError。
- 返回非零状态，供 E2E/CI 使用。

最低预期 job：

- `aegisops-operator`
- `aegisops-gateway`
- `aegisops-api`
- `aegisops-diagnosis`
- `fault-lab`

### 8.5 Loki 日志

#### 统一结构化字段

Go/Python 日志至少包含：

- `timestamp`
- `level`
- `component`
- `request_id`
- `trace_id`（存在时）
- `incident_uid`（仅日志允许）
- `phase`
- `operation_id`
- `error_code`

不得包含：

- Authorization Header。
- DeepSeek/SMTP Token。
- 原始证据正文。
- 可能含 Secret 的整个 Kubernetes 对象。

#### 新增 `deploy/observability/loki/recording-rules.yml`

可选记录：

- 每组件 error rate。
- Incident phase transition 日志速率。
- DeepSeek timeout/429 日志速率。

#### 新增 `scripts/check-loki-evidence.sh`

- 写入唯一 marker 的受控 fault-lab 日志。
- 通过 LogQL 查询。
- 验证 marker 被采集。
- 验证包含 `password=...` 的测试文本被脱敏。

### 8.6 OpenTelemetry 与 Tempo

#### Go

- 保留现有 HTTP 中间件。
- Controller 每次 Reconcile 创建 span：`incident.reconcile`。
- 子 span：`evidence.collect`、`diagnosis.submit`、`policy.evaluate`、`executor.apply`、`verifier.check`、`executor.rollback`。
- span attributes 使用低风险元数据，不保存证据内容。
- HTTP 客户端加入 propagation，Diagnosis API 能收到父 trace context。

#### Python

- FastAPI 自动/手动插桩。
- asyncpg/SQLAlchemy span。
- DeepSeek HTTP span，不记录 request body/Authorization。
- LangGraph node span：normalize/retrieve/diagnose/review/finalize。

#### 部署

新增：

- `deploy/observability/otel/collector.yaml`
- `deploy/observability/tempo/values.yaml`
- `deploy/observability/grafana/datasources.yaml`

Collector 流水线：

```text
OTLP receiver → memory_limiter → batch → Tempo exporter
```

必须配置 memory limiter、retry/backoff 和 sending queue；禁止 logging exporter 在生产日志中打印完整 span attribute。

### 8.7 Grafana Dashboard

更新 `deploy/observability/grafana/aegisops-dashboard.json`：

1. 当前活跃 Incident：按 phase/severity。
2. 告警接入速率与去重率。
3. 每阶段 P50/P95/P99。
4. Diagnosis queue depth、成功率、P95 延迟。
5. LLM 429/5xx/timeout、Token 使用量。
6. Evidence source 成功率与 partial 比例。
7. Policy Auto/ApprovalRequired/Deny 分布。
8. Typed Action 成功、失败、rollback。
9. Verification 健康率。
10. MTTR 分布。
11. Audit critical failure。
12. Target lock contention。
13. Alertmanager firing 告警数和通知失败率。
14. Logs 面板，按 trace_id/incident_uid 跳转 Loki。
15. Trace 链接，从 exemplar/trace_id 跳 Tempo。

Dashboard 变量：cluster、namespace、severity、category、action、time range。

不得使用 Incident UID 作为常驻高基数 Prometheus label；详情通过日志/审计 API 查询。

### 8.8 可观测性验收

```bash
scripts/render-prometheus-rules.sh --output /tmp/aegisops.rules.yml
promtool check rules /tmp/aegisops.rules.yml
scripts/test-prometheus-rules.sh \
  --rules /tmp/aegisops.rules.yml \
  --tests deploy/observability/tests/aegisops.rules.test.yml
scripts/check-prometheus-targets.sh --url http://localhost:19090 \
  --expected-job aegisops-operator \
  --expected-job aegisops-gateway \
  --expected-job aegisops-api \
  --expected-job aegisops-diagnosis \
  --expected-job fault-lab
scripts/check-loki-evidence.sh --url http://localhost:13100
```

通过标准：

- 五个 target 连续 5 分钟 up。
- 人为停止 Diagnosis Worker 后产生告警并收到邮件。
- 恢复 Worker 后收到 resolved 邮件。
- 一次完整 Incident 能在 Tempo 中看到跨 Go/Python的 Trace。
- Grafana 所有面板无 datasource/error，截图中包含真实数据。

### 8.9 建议提交

1. `feat: 补齐 operator 与 diagnosis 业务指标`
2. `feat: 完善 ServiceMonitor 与 target 健康检查`
3. `feat: 接入 Loki 结构化日志与脱敏验证`
4. `feat: 接入 OTel Collector 和 Tempo`
5. `feat: 升级 AegisOps Grafana 总览大盘`

---

## 9. M9.4：Incident API 与 Web Console 完整化

### 9.1 Diagnosis 只读客户端

#### 新增 `internal/httpapi/diagnosis_client.go`

```go
type DiagnosisReader interface {
    GetEvidence(ctx context.Context, id string) (EvidenceDetailDTO, error)
    GetTimeline(ctx context.Context, incidentUID string) ([]TimelineEntryDTO, error)
}

type HTTPDiagnosisReader struct {
    BaseURL      string
    TokenSource  analysisclient.TokenSource
    HTTPClient   *http.Client
}

func (c *HTTPDiagnosisReader) GetEvidence(...) (..., error)
func (c *HTTPDiagnosisReader) GetTimeline(...) (..., error)
```

要求：

- 复用服务间 Token。
- timeout 3 秒。
- 响应体上限 1 MiB。
- Diagnosis 不可用时 Incident 详情仍返回 CR 摘要，并标记 `detailsUnavailable`，不能让整个页面 500。

### 9.2 Incident API 路由

新增：

- `GET /api/v1/incidents/{namespace}/{name}/timeline`
- `GET /api/v1/incidents/{namespace}/{name}/evidence`

权限：

- viewer 可读脱敏证据和时间线。
- approver 继承 viewer。
- 未认证 401、角色不足 403。

同时对齐 Diagnosis API 契约：

- 如果 Console 需要浏览 Runbook，新增受认证的 `GET /v1/runbooks` 和按 document/version 查询的只读端点。
- 如果 v0.2.0 不提供 Runbook 浏览功能，就从 `docs/api-contracts.md` 删除该端点声明，只保留诊断结果中的 `runbook_refs`。
- `GET /v1/audit-events` 同理：已有按 Incident timeline 查询就删除泛化列表声明；不要保留不存在的路由。
- 新增测试从 FastAPI OpenAPI 和 chi 路由清单生成/核对 `docs/api-contracts.md` 中的路径，防止再次漂移。

#### DTO

`EvidenceDetailDTO`：

- id/hash/window/partial/missingSources/redactions。
- items 只返回脱敏 summary、source、kind、timestamp。
- 不返回数据库内部字段或 Prompt。

`TimelineEntryDTO`：

- time/type/reason/message/actor/sequence/eventHash。
- eventHash 可以完整复制，但 UI 默认显示前 12 位。

### 9.3 修复分页过滤语义

当前 phase/severity 过滤发生在 Kubernetes 分页之后，可能出现当前页为空但后续页有匹配数据。

v0.2.0 方案：

- 先按 namespace List。
- 在服务端过滤 phase/severity。
- 对过滤后的结果应用自定义 opaque cursor。
- 限制单次最大扫描 2000 个 Incident。
- cursor 包含过滤条件摘要，条件改变后旧 cursor 返回 400。

后续数据量更大时再使用 informer cache + field index。

必须测试：第一页全被过滤、第二页存在匹配项时，API 仍返回正确结果。

### 9.4 Web 组件

新增：

- `web/src/components/EvidencePanel.tsx`
- `web/src/components/DiagnosisCard.tsx`
- `web/src/components/AuditTimeline.tsx`
- `web/src/components/PolicyDecisionCard.tsx`
- `web/src/components/ExecutionCard.tsx`
- `web/src/components/AlertBanner.tsx`

交互：

- Evidence 默认展示摘要，可展开单项。
- 明确显示 `partial` 和 missing source。
- Diagnosis 展示 root cause/confidence/evidence refs/runbook refs/reviewer verdict。
- Proposal 展示 action、参数、风险、policy reason、planDigest 前 12 位。
- 审批前弹窗再次显示 Target/Action/参数/风险。
- Escalated/RolledBack 使用明显但不刺眼的状态颜色。
- API 部分不可用时显示降级提示，不空白崩溃。

### 9.5 Web 测试

- Evidence partial 状态。
- 无证据详情时降级。
- reviewer fail 时无执行按钮。
- approval digest 刷新后旧页面提交返回冲突并提示刷新。
- timeline 排序。
- 401 自动回到登录/Token 输入。
- 移动端宽度渲染。
- Playwright：Dashboard → Incident Detail → Approve → Phase 更新。

### 9.6 验收

- Incident 详情能显示真实 evidence 和数据库 audit timeline。
- Diagnosis API 暂停时页面仍显示 CR 摘要。
- viewer 不能审批。
- approver 批准时服务端复制 digest，前端无法提交自定义 digest。
- `pnpm lint && pnpm typecheck && pnpm test && pnpm e2e` 全部通过。

### 9.7 建议提交

1. `feat: 为 incident-api 接入证据与审计只读接口`
2. `fix: 修正 Incident 列表过滤与分页语义`
3. `feat: 完善事故详情、证据和审计时间线界面`
4. `test: 增加 Console Playwright 关键路径`

---

## 10. M9.5：可重复的一键开发环境

### 10.1 `scripts/dev-up.sh` 完整实现

参数：

```text
--context <required>
--profile minimal|full
--registry <local registry/name>
--tag <required, 禁止 latest>
--values <optional local values>
--observability
--mailhog
--skip-build
--yes
```

函数：

```bash
validate_context()
check_context_is_safe()
ensure_namespaces()
create_dev_secrets()
build_images()
load_images_into_kind()
install_observability()
install_aegisops()
install_fault_lab()
index_runbooks()
wait_for_rollouts()
start_port_forwards()
run_smoke_checks()
write_environment_manifest()
```

安全要求：

- context 必填并二次显示。
- 默认只允许名称匹配 `kind-*` 或 `k3d-*`；其他 context 要 `--allow-nonlocal`。
- 不自动打印 Secret。
- 不使用 `kubectl delete namespace --all` 等宽泛命令。
- 所有资源带 `app.kubernetes.io/part-of=aegisops`。
- 失败时输出诊断，不自动销毁，方便排查。

### 10.2 `scripts/dev-down.sh`

- 显式 context。
- 只卸载 Helm release、fault-lab 和本项目 port-forward PID。
- `--purge-data` 才删除 PVC。
- 删除前列出目标并确认。
- 不删除用户的 Kind 集群，除非单独 `--delete-kind-cluster`。

### 10.3 `scripts/build-images.sh`

修复：

- registry 可选：本地默认 `aegisops.local`，push 时必填真实 registry。
- 空 `--push` 参数不得作为空字符串传给 buildx。
- 五个镜像全部构建，Release workflow 也必须包含 fault-lab。
- 输出 image digest 到 `dist/images-<tag>.json`。
- 支持 SBOM：Syft 或 `docker buildx --sbom`。
- 禁止 latest。

### 10.4 本地配置目录

`.local/**` 不跟踪，仅提供生成说明：

```text
.local/
├── values.yaml
├── values-email.local.yaml
├── secrets/
│   ├── diagnosis-token
│   ├── console-tokens
│   ├── webhook-token
│   ├── smtp-password
│   └── deepseek-api-key
├── pids/
└── environment.json
```

新增 `scripts/init-local-config.sh`：

- 从 example 文件生成本地模板。
- 随机 Token 直接写文件并设为 0600。
- 不把 Token 打印到终端。
- 已有文件默认不覆盖；必须传 `--force` 才可重新生成，并先备份。

### 10.5 Makefile

目标必须真实可用：

- `make dev-up CONTEXT=kind-aegisops-dev PROFILE=full`
- `make dev-down CONTEXT=kind-aegisops-dev`
- `make alerting-up`
- `make alerting-down`
- `make smoke`
- `make test-rules`
- `make test-integration`
- `make test-e2e`
- `make eval-fake`
- `make eval-deepseek`
- `make verify`
- `make verify-all`

区分：

- `verify`：开发机快速门禁，不启动集群。
- `verify-all`：包含 integration/E2E，耗时但完整。

### 10.6 验收

在一个新 Kind 集群执行：

```bash
kind create cluster --name aegisops-clean
scripts/init-local-config.sh
scripts/dev-up.sh --context kind-aegisops-clean --profile full --mailhog --yes
make smoke CONTEXT=kind-aegisops-clean
scripts/dev-down.sh --context kind-aegisops-clean --yes
```

通过标准：

- 不需要手工 patch YAML。
- 所有 Pod Ready。
- 五个 Prometheus target up。
- Alertmanager/MailHog smoke 成功。
- 重复运行 dev-up 幂等。
- dev-down 后不残留项目 port-forward。

### 10.7 建议提交

1. `build: 完成幂等且受 context 保护的 dev-up`
2. `build: 完成可恢复的 dev-down 与本地配置初始化`
3. `build: 修复多镜像构建与 digest 记录`
4. `docs: 更新从零安装和配置说明`

---

## 11. M9.6：真实 Integration、E2E 与 CI

### 11.1 测试分层定义

| 层级 | 允许的依赖 | 目的 | 运行位置 |
|---|---|---|---|
| Unit | fake client、内存对象 | 锁定纯逻辑和边界 | 每次 push/PR |
| envtest | kube-apiserver + etcd | 验证 CRD、status、controller 行为；不声称验证 RBAC 授权 | CI |
| Integration | PostgreSQL、Alertmanager、MailHog、HTTP 服务 | 验证真实协议和持久化 | CI |
| E2E | Kind + Helm + 全组件 | 验证实际部署和闭环 | main/手工触发 |
| Cloud smoke | 阿里云 k3s | 验证最终演示环境 | 发布前手工 |

### 11.2 真正的 envtest

#### 新增 `tests/integration/controller/envtest_suite_test.go`

职责：

- 启动 `envtest.Environment`。
- 加载 `config/crd/bases`。
- 创建 manager 和真实 controller。
- 使用独立 context/cancel 管理生命周期。
- 测试结束关闭 control plane。

关键函数：

```go
func TestMain(m *testing.M)
func startTestEnvironment(t *testing.T) *TestEnvironment
func (e *TestEnvironment) StartManager(t *testing.T, setup func(ctrl.Manager) error)
func (e *TestEnvironment) Stop(t *testing.T)
func waitForCondition(ctx context.Context, c client.Client, key client.ObjectKey, fn Predicate) error
```

#### 测试文件

- `incident_state_machine_test.go`：真实 API server 上的 status/finalizer/transition。
- `approval_validation_test.go`：UID/revision/digest/TTL。
- `target_lock_test.go`：Lease 竞争和过期接管。
- `crd_validation_test.go`：CEL 对非法 policy/action/selector 的拒绝。
- `leader_election_rbac_test.go`：只验证渲染权限中包含 Lease；envtest 默认管理员客户端不能证明 RBAC 授权。
- 真正 RBAC 行为放 Kind E2E：使用组件 ServiceAccount Token 或 `kubectl auth can-i --as=system:serviceaccount:...` 验证允许/拒绝矩阵。

### 11.3 PostgreSQL/Diagnosis Integration

#### 新增 `tests/integration/diagnosis/`

使用 Docker Compose 或 testcontainers 启动 pgvector PostgreSQL，执行 Alembic migrations。

测试：

- 同 Idempotency-Key 两次提交只创建一个 Job。
- 两个 Worker 使用 `FOR UPDATE SKIP LOCKED` 不领取同一任务。
- heartbeat stale 后任务重新排队。
- Evidence 相同 hash 去重。
- execution snapshot 保存/读取/hash 校验。
- audit advisory lock 下并发写入 sequence 连续、hash chain 可验证。
- Token 鉴权覆盖 analyses/evidence/audit/snapshot/timeline。
- Worker 并发上限在真实数据库任务上生效。

新增 `services/diagnosis/app/audit_verify.py`：

```python
async def verify_incident_chain(repo, incident_uid: str) -> AuditVerificationReport:
    """验证 sequence、previous_hash 和 event_hash；返回首个断点。"""
```

提供 CLI：

```bash
uv run aegis-audit verify --incident-uid <uid>
```

### 11.4 E2E 测试包

#### 新增 `tests/e2e/suite_test.go`

```go
type Environment struct {
    Context          string
    Namespace        string
    SystemNamespace  string
    K8s              client.Client
    GatewayURL       string
    IncidentAPIURL   string
    FaultLabURL      string
    PrometheusURL    string
    MailHogURL       string
    ViewerToken      string
    ApproverToken    string
    WebhookToken     string
}

func LoadEnvironment() (*Environment, error)
func RequireSafeContext(context string) error
func TestMain(m *testing.M)
```

保护：

- `AEGISOPS_E2E=1` 才运行。
- context 必须由 `AEGISOPS_E2E_CONTEXT` 显式提供。
- 默认只接受 `kind-aegisops-e2e`。
- run ID 和 namespace 由 `e2e-up.sh` 在 Helm 安装前生成，例如 `aegisops-e2e-<runid>`，并写入 environment.json。
- Helm 安装时把该 namespace 注入 `global.watchNamespaces`；Go 测试只能读取既有 environment.json，不能启动后再创建一个 Operator 未 watch 的 namespace。

#### 新增 `tests/e2e/helpers.go`

```go
func PostAlert(ctx context.Context, env *Environment, fixture string) (GatewayResponse, error)
func WaitIncidentPhase(ctx context.Context, c client.Client, key client.ObjectKey, phase Phase) (*AIOpsIncident, error)
func ApproveIncident(ctx context.Context, env *Environment, incident *AIOpsIncident) error
func InjectFault(ctx context.Context, env *Environment, kind string, duration time.Duration) error
func RecoverFault(ctx context.Context, env *Environment) error
func DeploymentSnapshot(ctx context.Context, c client.Client, key client.ObjectKey) (DeploymentState, error)
func QueryAuditTimeline(ctx context.Context, env *Environment, incident *AIOpsIncident) ([]TimelineEntry, error)
func AssertEmailReceived(ctx context.Context, env *Environment, alertName string) error
func DumpDiagnostics(ctx context.Context, env *Environment, dir string) error
```

所有 wait 使用 context deadline 和 1–2 秒 polling，不使用无上限 sleep。

#### 场景 A：`auto_restart_test.go`

流程：

1. 部署 checkout/fault-lab 和 Auto RestartWorkload policy。
2. 调用 `/inject?type=config`，确认 `/checkout` 返回 500。
3. 发送 CheckoutHTTP500s firing 告警。
4. 等待 Incident 出现。
5. 只轮询最终/等待型 phase；快速中间阶段通过 CR timeline 和 audit event 断言按 CollectingEvidence→Diagnosing→PolicyChecking 顺序出现，避免轮询漏掉瞬态状态。
6. 断言 action=`RestartWorkload`、decision=`Auto`。
7. 等待 Resolved。
8. 断言 Deployment restart annotation 改变且 OperationID 存在。
9. 断言 `/checkout` 恢复 200。
10. 断言审计链至少包含 ExecutionStarted/ExecutionCompleted/IncidentResolved。
11. 再发相同告警，断言不会创建第二个 Incident。
12. 重启 Operator，断言不重复 Apply。

Fixture 契约：`type=config` 的故障状态只保存在故障 Pod 进程内存中，不写共享 ConfigMap/PVC；滚动重启新 Pod 后状态自然清除。该行为必须由 fault-lab 单元测试和 E2E 前置断言锁定，否则 RestartWorkload 不能作为此场景的恢复动作。

#### 场景 B：`approval_patch_memory_test.go`

流程：

1. 部署 memory limit=300Mi 的工作负载和 ApprovalRequired policy。
2. 注入 OOM 或使用确定性 OOM fixture。
3. 发送 OOMKilled 告警。
4. 等待 AwaitingApproval。
5. 保存 proposal revision/digest 和原 Deployment resourceVersion。
6. 使用 approver Token 批准。
7. 等待 Executing/Verifying/Resolved。
8. 断言 memory limit 从 300Mi 变为预期值，例如 384Mi。
9. 断言 operation-id annotation 与 status reference 一致。
10. 断言 Snapshot 在 PostgreSQL 可读取且 hash 一致。
11. 断言完整四事件审计链。

#### 场景 C：`rollback_test.go`

流程：

1. ScaleDeployment 从 1 扩到 3。
2. 让 verifier 确定性失败，可通过测试专用 unhealthy endpoint/fixture，不手工篡改 Status。
3. 等待 RollingBack → RolledBack。
4. 断言 replicas 恢复 1。
5. 断言回滚使用的是 Apply 前持久化快照。
6. 断言目标 Lease 在终态释放。
7. 断言收到 critical rollback 邮件。

#### 场景 D：`security_boundaries_test.go`

- 无 Token 访问 Diagnosis `/v1/evidence` → 401。
- 错 Token → 401。
- Diagnosis Pod 内不存在 ServiceAccount Token。
- viewer 调审批 → 403。
- 提交自定义 digest 被 API 忽略或拒绝。
- 非白名单动作被 CRD/Policy 拒绝。
- 同目标两个 Incident 只有一个进入 Executing。

#### 场景 E：`alert_email_test.go`

- 停止/隔离一个可恢复测试 target。
- `AegisOpsTargetDown` 触发 critical。
- MailHog 收到 firing 邮件。
- 恢复 target。
- MailHog 收到 resolved 邮件。
- 邮件正文不包含 fixture 中的测试 Secret。
- warning 路由通过独立的短时测试规则或 Alerts API smoke 验证，不把 TargetDown 的 severity 改成 warning。

### 11.5 E2E 脚本

#### 新增 `scripts/e2e-up.sh`

步骤：

1. 创建固定 Kind cluster。
2. 构建并加载五个镜像。
3. 安装 kube-prometheus-stack、Loki、Tempo、MailHog。
4. 配置无认证 MailHog smarthost、AlertmanagerConfig selector/namespace selector、模板 ConfigMap 挂载和秒级测试 route；不创建无意义的 SMTP Secret。
5. 使用预先生成的 run namespace 安装 AegisOps Helm Chart，并同步设置 Operator watchNamespaces。
6. 安装 fault-lab/fixtures。
7. 等待所有 rollout，并确认 Alertmanager 最终配置包含 AegisOps receiver。
8. 建立 port-forward 并保存 PID。
9. 输出 `.local/e2e/environment.json`。

#### 新增 `scripts/run-e2e.sh`

- 设置 context/URL，不把 Token 打到日志。
- 运行 `go test ./tests/e2e/... -count=1 -timeout=30m`。
- 失败时自动调用 `scripts/collect-e2e-artifacts.sh`。

#### 新增 `scripts/collect-e2e-artifacts.sh`

保存到 `artifacts/e2e/<runid>/`：

- `kubectl get all/events/crd/incident/policy/approval -o yaml`。
- 所有 AegisOps Pod 最近 500 行日志。
- describe Pod/Deployment。
- Prometheus active targets、rules、alerts。
- Alertmanager alerts/status。
- MailHog 邮件 JSON，正文先脱敏。
- PostgreSQL audit timeline 和 job 状态，不导出 Secret。
- Helm values 使用 `helm get values` 后对敏感字段脱敏。
- 环境版本：Git SHA、镜像 digest、Kubernetes/Helm 版本。
- 上传前对整个 artifact 目录运行统一 Secret/PII scanner；命中时 CI 禁止上传原 artifact，只输出命中类型和本地隔离路径。
- 增加负向测试：故意放入 canary token/email，scanner 必须阻止上传。

### 11.6 GitHub Actions CI 修复

#### `.github/workflows/ci.yml`

- `go`：race + coverage。
- `envtest`：运行真正的 `tests/integration/controller`，不是重复 unit test。
- `python`：ruff/mypy/pytest/coverage。
- `web`：lint/typecheck/vitest/build。
- `manifests`：helm lint、template、kubeconform、promtool。
- `docker-build`：构建五个镜像。
- `repo-hygiene`：运行仓库完整性脚本。
- 缓存只缓存 dependency，不把 buildcache 提交。

#### `.github/workflows/e2e.yml`

- `pull_request` 默认不跑全量 E2E，可用 label/手工触发。
- main push 或 workflow_dispatch 运行 Fake LLM E2E。
- 使用 `scripts/e2e-up.sh` 安装完整依赖。
- 运行真实测试包。
- 失败 artifact 上传并保留至少 14 天。
- always 执行受控 cleanup。

#### `.github/workflows/security.yml`

修复：

- Trivy 之前必须先构建待扫描镜像。
- 高危/严重漏洞应失败；仅允许有到期时间和说明的 ignore。
- kubeconform 不再 `|| true`。
- gitleaks、govulncheck、pip-audit、pnpm audit 失败不能被吞掉。
- 增加 Helm/manifest 的 Checkov 或 Kubescape，可先 report，再逐步设门禁。
- 生成并上传 SBOM。

#### `.github/workflows/release.yml`

- 构建并推送 fault-lab 镜像。
- 镜像 tag 同时包含 semver 和 Git SHA。
- Chart 的 appVersion/image tag 一致。
- 可选 cosign keyless 签名和 provenance。
- Release 附带 SBOM、Chart、checksums、实验报告摘要。

### 11.7 覆盖率门槛

建议初始门槛：

- Go 总体 ≥75%，核心 controller/policy/executor ≥80%。
- Python 总体 ≥75%，security/worker/graph ≥85%。
- Web 关键组件 ≥70%。
- 覆盖率不得通过删除测试对象、排除核心文件来提高。

### 11.8 E2E 稳定性标准

- 本地连续运行 5 次全部通过。
- GitHub Actions 连续 3 次通过。
- 单次失败必须保留通过 Secret/PII 扫描的脱敏 artifact；如果原始 artifact 命中敏感项，则只保留隔离记录和安全摘要，不能为满足“保留”要求而上传敏感文件。
- 测试 duration 有统计，异常增长 50% 需要解释。
- 禁止在测试中直接修改 Incident Status 绕过 Controller。

### 11.9 建议提交

1. `test: 增加真实 controller envtest 套件`
2. `test: 增加 PostgreSQL 与 diagnosis 集成测试`
3. `test: 增加 Auto、审批、回滚 E2E 闭环`
4. `test: 增加安全边界与邮件告警 E2E`
5. `ci: 让 E2E 工作流安装并验证完整系统`
6. `ci: 修复安全扫描和发布产物门禁`

---

## 12. M9.7：真实 DeepSeek 与 RAG 对照评估

### 12.1 评估原则

- FakeClient 只用于流程回归，不用于证明 AI 能力。
- DeepSeek 的失败、拒答、无方案必须保留在分母中。
- ground truth 来自注入器、人工复核后的事件记录，不来自模型输出。
- 评估脚本不得按 ground truth marker 直接生成答案。
- 每次实验记录 Git SHA、模型名、Prompt version、Runbook version、数据集 version。
- 禁止为了提高准确率删除失败样本。
- 报告同时展示样本数、分母、均值和置信区间。

### 12.2 目录重构

```text
eval/
├── README.md
├── datasets/
│   ├── v1/
│   │   ├── incidents.jsonl
│   │   ├── evidence/
│   │   ├── manifest.yaml
│   │   └── SHA256SUMS
├── configs/
│   ├── a-alert-only.yaml
│   ├── b-evidence.yaml
│   ├── c-evidence-rag.yaml
│   ├── d-evidence-rag-review.yaml
│   └── fake-regression.yaml
├── aegis_eval/
│   ├── __init__.py
│   ├── dataset.py
│   ├── providers.py
│   ├── experiment.py
│   ├── scoring.py
│   ├── bootstrap.py
│   ├── report.py
│   ├── redaction.py
│   └── cli.py
├── tests/
│   ├── test_dataset.py
│   ├── test_scoring.py
│   ├── test_resume.py
│   └── test_report.py
├── runs/
│   └── .gitkeep
└── reports/
    └── .gitkeep
```

### 12.3 数据集 Schema

每行至少包含：

```json
{
  "case_id": "oom-001",
  "dataset_version": "v1",
  "fault_type": "oomkilled",
  "variant": "clean",
  "incident": {},
  "evidence_path": "evidence/oom-001.json",
  "ground_truth": {
    "category": "OOMKilled",
    "root_cause_keywords": ["memory limit", "exit code 137"],
    "acceptable_actions": ["PatchResourceLimit"],
    "must_not_actions": ["RestoreConfigMap"],
    "should_degrade": false
  },
  "provenance": {
    "source": "fault-lab",
    "campaign_run_id": "...",
    "captured_at": "...",
    "reviewed_by": "human"
  }
}
```

数据集最低要求：

- 6 类故障。
- 每类至少 5 个真实采集样本。
- clean/noisy/sparse 至少各一个。
- 至少 6 个无充分证据、应降级的负样本。
- 至少 6 个含 prompt injection 文本的安全样本。
- 至少 6 个多故障/干扰证据样本。
- v1 硬门槛为 36 个真实样本：6 类×至少 5 个=30，再加至少 6 个负样本；prompt injection 和多故障样本可以与前述集合重叠。推荐扩展到 48，但验收口径固定为至少 36。

### 12.4 从真实 Campaign 导出

#### 新增 `scripts/export-eval-case.sh`

参数：

- `--incident namespace/name`
- `--fault-type`
- `--variant`
- `--run-id`
- `--output eval/datasets/v1`

步骤：

- 从 Incident API 拉取脱敏 evidence。
- 从 CR 拉取 Incident/diagnosis/proposal 摘要，但生成模型输入时删除旧 diagnosis/proposal，避免答案泄漏。
- 从 fault injection manifest 获取 ground truth action/category。
- 人工确认后填写 reviewer 字段。
- 计算文件 hash。
- 运行 Secret 扫描。

#### 新增 `eval/aegis_eval/redaction.py`

- 复用/镜像生产 Redactor 测试向量。
- 扫描邮箱、Token、私网地址、Authorization、证书。
- 发现未脱敏敏感项时拒绝写入公开数据集。

### 12.5 Provider 抽象

#### `providers.py`

```python
class Provider(Protocol):
    async def diagnose(self, request: ExperimentRequest) -> ModelResult: ...

class FakeProvider: ...
class DeepSeekProvider: ...

def create_provider(config: ExperimentConfig, settings: EvalSettings) -> Provider
```

修复当前错误：CLI 的 `--provider deepseek` 必须真正实例化 DeepSeekProvider，绝不能无条件使用 FakeClient。

DeepSeek 运行约束：

- API Key 只从环境变量/本地 secret 文件读取。
- temperature 固定并记录。
- timeout/retry 次数记录。
- 429/5xx/timeout 作为结果保存。
- 不把整份证据或 Prompt 写普通运行日志。
- 评估 artifact 必须保存经过再次脱敏的规范化输入、rendered prompt、模型原始响应、规范化结果、实际 API 返回 model、request ID、时间、重试和错误；同时计算 prompt/input/output hash，上传前运行 Secret/PII scanner。
- 支持 `--resume`，按 case/config/model/prompt hash 跳过已完成项。
- 支持预算保护：`--max-calls`、`--max-input-tokens`、`--max-output-tokens`。

### 12.6 A/B/C/D 配置

#### A：`a-alert-only.yaml`

- 输入：alert name、labels、target。
- 不输入 K8s/Prom/Loki 证据。
- 不启用 RAG。
- 不启用 Reviewer。
- 用途：建立最低基线。

#### B：`b-evidence.yaml`

- 输入：Alert + 脱敏多源 Evidence。
- 不启用 RAG。
- 不启用 Reviewer。
- 用途：衡量证据本身的收益。

#### C：`c-evidence-rag.yaml`

- 输入：Alert + Evidence。
- 启用 Hybrid RAG。
- 不启用 Reviewer。
- 用途：单独衡量 RAG 增益。

#### D：`d-evidence-rag-review.yaml`

- 输入：Alert + Evidence。
- 启用 Hybrid RAG。
- 启用 Reviewer 和一次修订。
- 用途：衡量 Reviewer 在相同 RAG 输入上的增益和误杀。

#### Fake：`fake-regression.yaml`

- 只验证 JSON schema、评分和报告流水线。
- 报告必须带明显水印：`DETERMINISTIC TEST DOUBLE — NOT MODEL QUALITY`。

### 12.7 评分函数

#### `scoring.py`

```python
def score_category(predicted, ground_truth) -> Score
def score_action(predicted, acceptable, forbidden) -> Score
def score_evidence_references(result, evidence) -> ReferenceScore
def score_runbook_references(result, indexed_versions) -> ReferenceScore
def score_degradation(result, should_degrade) -> Score
def score_policy_safety(result, whitelist, parameter_bounds) -> SafetyScore
def score_root_cause_semantic(result, rubric) -> Score
```

指标：

- Category exact accuracy。
- Root cause rubric/人工复核准确率。
- Action acceptable rate。
- Dangerous action rate。
- Evidence citation validity。
- Runbook Hit@K、MRR。
- Reviewer 拦截危险方案率。
- Reviewer 错误拦截率。
- 正确安全降级率。
- 不必要降级率。
- JSON/schema failure rate。
- P50/P95 latency。
- input/output tokens。
- 每次成功诊断估算成本。

### 12.8 统计与报告

#### `bootstrap.py`

- 对 accuracy/rate 计算 bootstrap 95% CI。
- seed 固定并记录。
- 小样本同时显示 raw numerator/denominator。
- A/B/C/D 对同一 case 做配对比较；实验按 case 分块并随机/交错运行，避免服务时段变化只影响某一实验臂。
- 置信区间按 case/故障簇 bootstrap，不能把同一 case 的重复请求当独立样本放大样本量。
- root cause 人工语义评分采用盲评 rubric；至少两名评分者或“一名评分者 + 预定义裁决规则”，记录分歧和最终裁决。

#### `report.py`

生成：

- `reports/<runid>/summary.md`
- `reports/<runid>/results.csv`
- `reports/<runid>/per_case.jsonl`
- `reports/<runid>/confusion-matrix.png`
- `reports/<runid>/latency.png`
- `reports/<runid>/cost.png`
- `reports/<runid>/manifest.json`

报告必须单列：

- provider/model/version。
- 数据集 hash。
- 成功、失败、超时、拒答数量。
- A/B/C/D 差异，并分别解释 Evidence、RAG、Reviewer 的边际变化。
- 每类故障结果。
- 安全样本结果。
- 已知偏差和外部有效性限制。

### 12.9 真实故障效果指标

模型评估之外，Campaign 还要记录系统效果：

- MTTD：故障注入 → Incident 创建。
- Evidence latency：Incident 创建 → evidence ready。
- Diagnosis latency。
- Approval wait：单独列出，不混入系统处理速度。
- Execute latency。
- Verification latency。
- System MTTR：不含人工审批等待和包含审批等待各一份。
- Remediation success rate。
- Rollback success rate。
- False remediation rate。
- Duplicate incident suppression rate。

所有阶段时间从 CR timeline/audit event 计算，不允许手填。

### 12.10 验收

- `fake` 和 `deepseek` 的代码路径可被测试证明不同。
- 至少 36 个真实采集样本完成 DeepSeek A/B/C/D 配对实验。
- 失败样本保留在分母。
- 报告有 95% CI、Token 和成本。
- 任何 100% 都必须同时显示具体分子/分母并解释样本规模。
- `docs/evaluation.md` 链接最新真实报告，并清晰分开 fake regression。

### 12.11 建议提交

1. `refactor: 重建可复现的 eval 数据与 provider 抽象`
2. `feat: 从真实故障 Campaign 导出脱敏数据集`
3. `feat: 增加 DeepSeek A/B/C/D 对照实验`
4. `feat: 增加置信区间、成本和安全评分报告`
5. `docs: 发布真实评估方法与限制`

---

## 13. M9.8：阿里云低成本最终演示环境

### 13.1 技术选择

为了控制预算，默认不使用长期运行的 ACK 托管集群。建议：

- 单台按量付费 ECS。
- 2 vCPU/4–8 GiB，实际规格通过变量配置。
- Ubuntu LTS/Alibaba Cloud Linux。
- 单节点 k3s。
- 系统盘 40–80 GiB。
- 仅演示期间创建，结束后 Terraform destroy。
- PostgreSQL、Prometheus、Loki、Grafana 使用集群内小规格部署；不做高可用声明。

如果已有免费额度或希望展示 ACK，再增加可选 overlay，不作为 v0.2.0 必需项。

### 13.2 Terraform 目录

```text
infra/terraform/aliyun/
├── versions.tf
├── providers.tf
├── variables.tf
├── locals.tf
├── network.tf
├── security_group.tf
├── ecs.tf
├── cloud_init.tf
├── outputs.tf
├── budget.auto.tfvars.example
├── README.md
├── modules/
│   ├── network/
│   └── k3s-node/
└── tests/
    ├── terraform_validate.sh
    └── policy.rego
```

### 13.3 Terraform 变量

必须配置化：

- region/zone。
- instance type。
- image ID 或 image family。
- system disk category/size。
- bandwidth charging type/max outbound bandwidth。
- SSH public key path。
- allowed admin CIDRs。
- project name/environment/tags。
- auto release time（若云 API/资源支持）。

禁止：

- AccessKey 写入 tfvars/Git。
- SSH 密码登录。
- 0.0.0.0/0 开放 SSH、Kubernetes API、Grafana 管理端口。
- Terraform output 输出 Secret。

### 13.4 网络与安全组

- SSH 22：仅个人公网 IP/CIDR。
- HTTP/HTTPS：如需公开演示，只开放 80/443。
- k3s API 6443：仅管理 IP，或完全不公网开放，通过 SSH tunnel。
- Grafana/Prometheus/Loki/Incident API 默认不直接公网暴露。
- 使用 Ingress + TLS 或 SSH tunnel 演示。
- 云 metadata 访问通过 NetworkPolicy/主机规则限制。

### 13.5 cloud-init

`cloud_init.tf` 只负责：

- 安装基础依赖。
- 安装固定版本 k3s。
- 配置 swap/内核参数。
- 创建非 root 运维用户。
- 启用基础防火墙和时间同步。
- 输出不含 Token 的完成标记。

不得在 Terraform state 中嵌入：

- DeepSeek Key。
- SMTP 密码。
- Console/Webhook/Diagnosis Token。

这些 Secret 在部署阶段通过本地文件或 sealed/external secret 注入。

### 13.6 云上 Helm values

新增 `deploy/helm/aegisops/values-aliyun-demo.yaml`：

- `global.clusterID: aliyun-demo`
- 小规格 requests/limits。
- external LLM egress enabled。
- NetworkPolicy enabled。
- internal PostgreSQL 小磁盘。
- SMTP 配置引用 Secret。
- Ingress host/TLS 通过单独 local values 覆盖。
- 不写真实域名、邮箱和 Secret。

新增 `deploy/observability/values-aliyun-demo.yaml`：

- Prometheus retention 1–3 天。
- Loki retention 1–3 天。
- Tempo retention 1 天。
- 单副本、资源限制。
- Grafana anonymous access 关闭。

### 13.7 部署脚本

#### 新增 `scripts/cloud-deploy.sh`

- 参数：context、tag、values、domain。
- context 非 kind 时必须输入完整确认词，例如 `deploy aliyun-demo`。
- 应用 CRD、observability、AegisOps、fault-lab。
- 等待 rollout。
- 运行 cloud smoke。
- 输出 URL，不输出 Token。

#### 新增 `scripts/cloud-smoke.sh`

检查：

- Node Ready。
- 全部 Pod Ready。
- 五个 target up。
- NetworkPolicy 开启。
- Diagnosis 能访问 DeepSeek。
- viewer/approver 权限边界。
- Mail smoke。
- 一条 Auto RestartWorkload Incident Resolved。

#### 新增 `scripts/cloud-destroy-checklist.sh`

销毁前：

- 导出脱敏 audit/eval/artifacts。
- 保存截图和视频。
- 列出 ECS/EIP/磁盘等会计费资源。
- Terraform plan destroy。
- 要求用户确认，不自动直接 destroy。

销毁后：

- `terraform state list` 为空或 workspace 删除。
- 阿里云 CLI 查询没有残留 EIP/磁盘/实例。
- 记录实际花费和运行时长。

### 13.8 成本控制

- 所有资源加 `Project=AegisOps`、`Environment=Demo`、`Owner` 标签。
- 文档列出预估资源，但发布前按当时价格重新核对。
- 每次演示记录 create/destroy 时间。
- 不让日志/指标长期保留。
- 避免公网固定 EIP 闲置。
- 设置云预算提醒；该提醒属于云账号治理，不由 AegisOps SMTP 规则替代。

### 13.9 验收

- `terraform fmt -check && terraform validate`。
- tfsec/checkov 无 High/Critical 未解释问题。
- 全新 ECS 能从零部署。
- 云上完成一条真实 DeepSeek + 邮件 + 自愈闭环。
- 演示结束后资源完全销毁。
- `docs/cloud-demo-report.md` 记录版本、资源、成本、结果和限制。

### 13.10 建议提交

1. `infra: 增加阿里云单节点 k3s 演示环境`
2. `deploy: 增加低资源云上 Helm overlay`
3. `test: 增加云环境 smoke 与销毁检查`
4. `docs: 记录阿里云演示和成本数据`

---

## 14. M9.9：故障报告、截图、视频与复盘文章

### 14.1 截图清单

截图必须使用真实数据，统一放在：

```text
docs/assets/screenshots/
```

至少包含：

1. `01-dashboard-overview.png`：Grafana 总览，展示 phase、MTTR、动作、失败率。
2. `02-incident-evidence.png`：Console 证据与 Diagnosis。
3. `03-approval-policy.png`：Policy decision、planDigest、审批界面。
4. `04-execution-resolved.png`：执行、验证、Resolved 时间线。
5. `05-rollback-audit.png`：Rollback 和审计链。
6. `06-email-warning.png`：普通 warning 邮件。
7. `07-email-resolved.png`：恢复邮件。
8. `08-tempo-trace.png`：跨 Operator/Diagnosis 的 Trace。
9. `09-ci-e2e.png`：GitHub Actions E2E 通过。
10. `10-deepseek-eval.png`：A/B/C/D 结果图。

截图处理：

- 隐藏 Token、完整邮箱、真实公网 IP、云账号 ID。
- 保留时间、指标和上下文，不能裁剪到无法判断真实性。
- PNG 压缩但保证文字可读。
- 每张图有 Markdown alt text 和说明。

### 14.2 故障演练报告

目录：

```text
docs/experiments/
├── 001-oom-patch-memory.md
├── 002-config-auto-restart.md
├── 003-verification-failure-rollback.md
└── data/
```

每份报告必须包含：

- 目标与假设。
- 环境版本/Git SHA/image digest。
- 故障注入方法和 ground truth。
- Policy 配置。
- 时间线：注入、检测、取证、诊断、审批、执行、验证、终态。
- Evidence 摘要。
- DeepSeek 输出摘要及引用，不公开敏感 Prompt 数据。
- 实际资源变更 before/after。
- Audit chain。
- 邮件通知时间。
- MTTD/Diagnosis latency/System MTTR/总 MTTR。
- 结果是否成功。
- 意外现象和限制。
- 原始 artifact 路径/hash。
- 可复现命令。

### 14.3 Postmortem

至少完成：

- Diagnosis API 鉴权未接线。
- Worker semaphore 未真正限流。
- 目标级锁缺失。
- E2E 工作流测试了空目录。
- Fake 评估误导性 100%。

Postmortem 不是自我批评文章，而是展示：

- Detection。
- Impact。
- Root cause。
- Contributing factors。
- Why tests missed it。
- Corrective action。
- Regression test。
- Preventive control。

### 14.4 演示视频

目标 5–8 分钟：

1. 30 秒：问题与架构。
2. 45 秒：CRD、Policy、安全边界。
3. 60 秒：注入故障和 firing 邮件。
4. 90 秒：Evidence、DeepSeek、RAG、Reviewer。
5. 60 秒：审批和 Typed Action。
6. 60 秒：Resolved/rollback、恢复邮件。
7. 60 秒：Grafana/Loki/Tempo/Audit。
8. 30 秒：真实评估结果和限制。

要求：

- 预先准备 `docs/demo-script.md`，不要现场临时敲大量命令。
- 录制前执行 cloud smoke。
- 视频中显示 Git SHA/版本。
- 不展示 Secret、浏览器密码管理器、云账号敏感信息。
- 保留失败备用路线，但不要伪造成功画面。

### 14.5 项目复盘文章

建议标题：

> 我没有让大模型直接执行 kubectl：如何设计一个证据驱动、可审批、可回滚的 Kubernetes AIOps Operator

文章结构：

1. 为什么“LLM + kubectl”不是可靠自愈。
2. Incident CR 为什么作为工作流状态。
3. Evidence/RAG/Reviewer 如何降低幻觉风险。
4. Policy、planDigest、Typed Action 如何分离建议与执行权。
5. Snapshot/OperationID/Lease 如何处理回滚、崩溃和并发。
6. Prometheus/Loki/Tempo/Email 如何形成可观测闭环。
7. Fake 100% 为什么不能证明 AI 效果。
8. DeepSeek A/B/C/D 真实评估结果。
9. 五个真实缺陷以及测试体系如何补上。
10. 当前仍不具备生产可用性的原因。

### 14.6 简历数据准备

只从最终报告提取以下数字：

- 故障场景数和真实样本数。
- DeepSeek A/B/C/D 根因命中率及分母。
- 危险方案拦截率。
- 引用有效率。
- 修复成功率、回滚成功率。
- System MTTR P50/P95。
- 告警邮件送达耗时。
- 单次诊断 P95 延迟和 Token 成本。
- 测试数量和核心覆盖率。

不再使用“从 30 分钟缩短到 2 分钟”这类没有基线实验的数据。

### 14.7 验收

- 所有截图都能对应实验记录。
- 视频脚本可以在新环境完整执行。
- 至少两份故障报告含原始数据。
- 至少一篇 postmortem 有对应 regression test。
- 文章明确限制，不夸大生产能力。

### 14.8 建议提交

1. `docs: 发布三份真实故障演练报告`
2. `docs: 发布安全与测试缺陷复盘`
3. `docs: 增加完整演示脚本与截图`
4. `docs: 发布 AegisOps 项目复盘文章`

---

## 15. M9.10：v0.2.0 发布冻结

### 15.1 发布前冻结项

- CRD v1alpha1 只允许 additive 修改。
- API contract 生成 OpenAPI 或至少与实际路由对照检查。
- Helm values schema 覆盖所有新增 alerting/externalLLM 配置。
- 配置示例无真实 Secret。
- README、PROJECT STATUS、博客和简历数字一致。
- 发布 tag 对应的 E2E artifact 和评估报告不可修改；后续修改发新版本。

### 15.2 `make release-check`

新增 target，顺序执行：

```text
repo hygiene
generated files drift
Go fmt/vet/lint/test/race/coverage
Python ruff/mypy/test/coverage
Web lint/typecheck/test/build
Helm lint/template/schema
kubeconform
promtool rules/config/tests
Docker build
Trivy
SBOM
Kind E2E
artifact secret scan
documentation link check
```

任何步骤失败都不得继续发布。

### 15.3 发布产物

- 五个 OCI image。
- Helm Chart `.tgz`。
- SHA256 checksums。
- SBOM。
- Source archive。
- E2E summary。
- DeepSeek evaluation summary，原始明细放仓库或 Release artifact。
- Upgrade notes 和 known limitations。

### 15.4 README 最终结构

1. 一句话价值主张。
2. 90 秒架构概览。
3. 安全边界。
4. GIF/视频链接。
5. 可复现 Quick Start。
6. 一次完整 Incident 截图。
7. 实验结果表，带链接和分母。
8. 测试/CI badge。
9. 文档索引。
10. Known limitations。
11. Roadmap。
12. License。

### 15.5 最终状态表

`docs/implementation-status.md` 中以下项目必须全部为 yes：

- Repository hygiene。
- Diagnosis API auth。
- Worker concurrency bound。
- Target lock。
- SMTP email warning/resolved。
- PrometheusRule tests。
- All targets up。
- Evidence/timeline API。
- Web approval E2E。
- Kind Auto/Approval/Rollback E2E。
- Real DeepSeek evaluation。
- Cloud smoke。
- Fault reports/screenshots/video/article。

### 15.6 正式版本声明

可以声明：

> AegisOps v0.2.0 是面向学习、演示和工程验证的 Kubernetes 可靠性控制面原型，具备可复现的证据驱动诊断、受控执行、回滚、审计、邮件告警和可观测性闭环；它展示了生产约束设计，但不宣称可直接用于生产。

不能声明：

- 无人值守生产自愈平台。
- 生产 SLA。
- 在大规模多集群验证。
- AI 能替代 SRE。

### 15.7 建议提交/Tag

1. `docs: 冻结 v0.2.0 API、配置与交付状态`
2. `release: 准备 AegisOps v0.2.0`
3. Tag：`v0.2.0`

---

## 16. 完整文件级变更索引

### 16.1 需要新增

```text
docs/NEXT-STEPS-IMPLEMENTATION-PLAN.md
docs/implementation-status.md
docs/evaluation.md
docs/cloud-demo-report.md
docs/experiments/*.md
docs/postmortems/*.md
docs/assets/screenshots/*

internal/targetlock/lock.go
internal/targetlock/kubernetes.go
internal/targetlock/lock_test.go
internal/httpapi/diagnosis_client.go
internal/httpapi/diagnosis_client_test.go

services/diagnosis/app/security.py
services/diagnosis/app/metrics.py
services/diagnosis/app/audit_verify.py
services/diagnosis/tests/unit/test_security.py
services/diagnosis/tests/unit/test_worker_concurrency.py

web/src/components/EvidencePanel.tsx
web/src/components/DiagnosisCard.tsx
web/src/components/AuditTimeline.tsx
web/src/components/PolicyDecisionCard.tsx
web/src/components/ExecutionCard.tsx
web/src/components/AlertBanner.tsx

deploy/observability/tests/aegisops.rules.test.yml
deploy/observability/alertmanager/alertmanager.example.yml
deploy/observability/alertmanager/alertmanager.mailhog.yml
deploy/observability/alertmanager/templates/email.tmpl
deploy/observability/docker-compose.alerting.yml
deploy/observability/otel/collector.yaml
deploy/observability/tempo/values.yaml
deploy/observability/grafana/datasources.yaml

deploy/helm/aegisops/values-email.example.yaml
deploy/helm/aegisops/values-aliyun-demo.yaml
deploy/helm/aegisops/templates/prometheusrule.yaml
deploy/helm/aegisops/templates/alertmanagerconfig.yaml
deploy/helm/aegisops/templates/alertmanager-email-template-configmap.yaml
deploy/helm/aegisops/templates/embedding-cache-pvc.yaml

tests/integration/controller/*.go
tests/integration/diagnosis/*.py
tests/integration/alertmanager_email_test.py
tests/e2e/suite_test.go
tests/e2e/helpers.go
tests/e2e/auto_restart_test.go
tests/e2e/approval_patch_memory_test.go
tests/e2e/rollback_test.go
tests/e2e/security_boundaries_test.go
tests/e2e/alert_email_test.go

scripts/check-repo-hygiene.sh
scripts/check-prometheus-targets.sh
scripts/check-loki-evidence.sh
scripts/render-prometheus-rules.sh
scripts/test-prometheus-rules.sh
scripts/init-local-config.sh
scripts/alerting-up.sh
scripts/alerting-down.sh
scripts/send-test-alert.sh
scripts/assert-test-email.py
scripts/e2e-up.sh
scripts/run-e2e.sh
scripts/collect-e2e-artifacts.sh
scripts/export-eval-case.sh
scripts/cloud-deploy.sh
scripts/cloud-smoke.sh
scripts/cloud-destroy-checklist.sh

eval/configs/*.yaml
eval/aegis_eval/*.py
eval/tests/*.py
eval/datasets/v1/*

infra/terraform/aliyun/*.tf
infra/terraform/aliyun/modules/network/*
infra/terraform/aliyun/modules/k3s-node/*
infra/terraform/aliyun/tests/*
```

### 16.2 需要重点修改

```text
.gitignore
README.md
Makefile
.github/workflows/ci.yml
.github/workflows/e2e.yml
.github/workflows/security.yml
.github/workflows/release.yml

api/v1alpha1/common_types.go
internal/controller/incident_controller.go
internal/controller/execution_phases.go
internal/controller/status.go
internal/observability/metrics.go
internal/httpapi/server.go
internal/httpapi/incidents.go
cmd/operator/main.go
cmd/incident-api/main.go

services/diagnosis/app/config.py
services/diagnosis/app/main.py
services/diagnosis/app/worker.py
services/diagnosis/app/api/__init__.py
services/diagnosis/app/llm/deepseek.py

web/src/api/client.ts
web/src/api/types.ts
web/src/pages/IncidentDetailPage.tsx

deploy/helm/aegisops/values.yaml
deploy/helm/aegisops/values.schema.json
deploy/helm/aegisops/templates/networkpolicies.yaml
deploy/helm/aegisops/templates/diagnosis-api-deployment.yaml
deploy/helm/aegisops/templates/diagnosis-worker-deployment.yaml
deploy/helm/aegisops/templates/servicemonitors.yaml

scripts/dev-up.sh
scripts/dev-down.sh
scripts/build-images.sh
docs/architecture.md
docs/security-model.md
docs/api-contracts.md
docs/operations.md
docs/demo-script.md
```

### 16.3 需要删除或归档

- `buildcache/**`：从 Git 跟踪删除。
- `fault-lab/server`：从 Git 跟踪删除。
- `eval/run_campaign.py`：在新 eval CLI 完成后归档或删除，避免两个入口产生不同口径。
- `eval/README.md` 的空占位内容：由真实说明替换。
- 空的 `tests/e2e/.gitkeep`、`tests/integration/.gitkeep`：目录有真实文件后删除。
- 与实际 API 不一致的旧文档段落。

---

## 17. 全局测试矩阵

| 能力 | Unit | Integration | E2E | Cloud smoke |
|---|---|---|---|---|
| API Token | 比较/解析 | FastAPI 全路由 | 无/错/正确 Token | 正确 Token |
| Worker 并发 | peak 计数 | PG 真实 jobs | burst incidents | DeepSeek burst |
| Target Lease | fake client | envtest Lease | 同目标双 Incident | 可选 |
| Typed Action | fake K8s | envtest | Auto/审批/回滚 | 一条真实闭环 |
| Audit Chain | hash 函数 | PG 并发 | 事件顺序 | verify CLI |
| Email | template | MailHog | firing/resolved | 真实邮箱 smoke |
| PrometheusRule | promtool | Alertmanager | target down | 组件 down/up |
| Evidence | collector unit | API/PG | K8s/Prom/Loki | 真实环境 |
| RAG | chunk/RRF | pgvector | fake graph | DeepSeek A/B/C/D |
| Console | Vitest | API fake/real | Playwright approve | 视频演示 |
| NetworkPolicy | template | Kind CNI | auth/egress | DeepSeek 443 |
| Terraform | validate | policy test | 不适用 | create/smoke/destroy |

---

## 18. 配置管理规范

### 18.1 可以直接写入并提交的配置

- SMTP smarthost。
- 发件人/收件人示例地址，必须是占位地址。
- group_wait/group_interval/repeat_interval。
- warning/critical 阈值。
- PromQL/LogQL。
- Runbook/Grafana URL 模板。
- Helm resources/replicas。
- DeepSeek model 名称、base URL。

### 18.2 只能写入本地配置或 Secret

- SMTP 授权码/密码。
- DeepSeek API Key。
- Webhook Token。
- Diagnosis service Token。
- Console viewer/approver Token。
- 阿里云 AccessKey。
- kubeconfig。

### 18.3 配置优先级

建议统一为：

```text
程序默认安全值
  < Helm values/base config
  < 环境 overlay YAML
  < Kubernetes Secret/本地 secret file
  < 测试显式注入
```

非敏感业务参数禁止通过散落的环境变量覆盖，优先集中在 values/YAML；Secret 通过文件挂载，而不是命令行参数。

### 18.4 配置变更验证

- JSON Schema/Helm schema。
- promtool/amtool。
- `helm template` golden test。
- 配置文件不存在、字段错误、Secret 缺失时 fail-closed。
- 日志只记录“使用了哪个配置文件/Secret 名”，不记录值。

---

## 19. 风险清单与处理策略

| 风险 | 影响 | 处理 |
|---|---|---|
| 邮箱供应商拒绝/限流 | 收不到告警 | MailHog 测试 + Alertmanager notification failure 告警 + 重试 |
| SMTP 配置泄密 | 账号风险 | Secret file/K8s Secret、gitleaks、截图脱敏 |
| 邮件风暴 | 收件箱轰炸 | group/inhibit/repeat/for，规则测试 |
| DeepSeek API 波动 | 诊断失败 | timeout/retry/安全降级/告警 |
| NetworkPolicy 阻断 DeepSeek | 真实模式不可用 | egress proxy/443 配置与 smoke |
| 同目标并发修复 | 资源互相覆盖 | Lease 锁 + 状态引用 + E2E |
| Worker burst | OOM/限流 | 真正有界并发 + queue alert |
| E2E 偶发失败 | CI 不可信 | 确定性 fixture、artifact、连续运行标准 |
| Eval 数据泄漏答案 | 结果虚高 | 导出时移除旧 diagnosis/proposal，hash 和人工审核 |
| 云资源忘记销毁 | 产生费用 | 标签、预算提醒、destroy checklist |
| 项目广而作者掌握不足 | 面试风险 | 按第 21 节复习，不继续无边界加功能 |

---

## 20. 最终 Definition of Done

只有以下全部满足，才能把 v0.2.0 标为完成：

### 20.1 代码与安全

- [ ] Diagnosis `/v1/**` Bearer Token 校验生效。
- [ ] Worker 最大并发测试证明不超过配置。
- [ ] 同目标 Incident Lease 竞争测试通过。
- [ ] DeepSeek 网络出口受控且可用。
- [ ] 无真实 Secret 被 Git 跟踪。

### 20.2 邮件和可观测性

- [ ] PrometheusRule 文件与 promtool tests 存在。
- [ ] Alertmanager 配置和邮件模板由 YAML 驱动。
- [ ] warning firing 邮件收到。
- [ ] critical firing 邮件收到。
- [ ] resolved 邮件收到。
- [ ] inhibition/repeat interval 测试通过。
- [ ] 发布前真实 SMTP firing/resolved smoke 完成并保存脱敏截图；该项不进入公共 CI。
- [ ] 五个 Prometheus target 连续 up。
- [ ] Loki 查询和脱敏验证通过。
- [ ] Tempo 可查询跨组件 Trace。
- [ ] Grafana 大盘全部面板有真实数据。

### 20.3 测试与 CI

- [ ] Unit/envtest/integration/E2E 分层真实存在。
- [ ] Auto RestartWorkload E2E 通过。
- [ ] Approval + PatchResourceLimit E2E 通过。
- [ ] Verification failure + rollback E2E 通过。
- [ ] Security boundary E2E 通过。
- [ ] Email E2E 通过。
- [ ] E2E artifact Secret/PII scanner 负向测试能阻止含 canary secret 的上传。
- [ ] 本地 E2E 连续 5 次通过。
- [ ] GitHub Actions E2E 连续 3 次通过。
- [ ] 失败 artifact 可用。

### 20.4 Eval

- [ ] Fake 报告明确标为测试替身。
- [ ] `--provider deepseek` 真正调用 DeepSeek。
- [ ] 至少 36 个真实样本完成 A/B/C/D 配对实验。
- [ ] 失败和降级在分母中。
- [ ] 报告包含 CI、延迟、Token、成本和限制。

### 20.5 部署与作品集

- [ ] 新 Kind 环境一键安装/卸载。
- [ ] 阿里云 k3s create/smoke/destroy 完成。
- [ ] 资源无计费残留。
- [ ] 至少 6 张截图。
- [ ] 5–8 分钟视频。
- [ ] 至少 2 份实验报告。
- [ ] 至少 3 份关键缺陷 postmortem。
- [ ] 项目复盘文章。
- [ ] README/博客/简历数字一致且有证据链接。

---

## 21. 面试掌握清单

项目完成并不等于能在面试中讲清楚。至少能够白板解释：

1. 为什么用 CRD Status 保存工作流状态，而不是只存在 PostgreSQL。
2. Reconcile 的幂等性来源和崩溃恢复路径。
3. `planDigest` 绑定哪些字段，如何防审批后的 TOCTOU。
4. `OperationID` 与 Target Lease 分别解决什么问题，为什么不能互相替代。
5. Snapshot 为什么必须在 Apply 前持久化。
6. 为什么 LLM 无 Kubernetes 凭据仍然不等于绝对安全。
7. Evidence partial/fail-closed 的取舍。
8. RAG 的向量检索、全文检索和 RRF 融合。
9. Reviewer 能防什么、不能防什么。
10. PrometheusRule、Alertmanager route、receiver、inhibit、silence 的职责。
11. 为什么邮件配置放 YAML，而密码仍然必须放 Secret。
12. Prometheus 高基数 label 的风险。
13. Worker `SKIP LOCKED`、heartbeat、stale retry 的语义。
14. Fake 100% 为什么不代表 DeepSeek 质量。
15. MTTD、系统 MTTR、总 MTTR 如何计算。
16. Kind、k3s、ACK 的取舍和本项目为什么选择低成本 ECS+k3s。

每一项都要能指向代码、测试或实验报告，不能只背概念。

---

## 22. 推荐执行起点

第一批只执行以下四个提交，不要先做 UI 或云部署：

1. 清理 `buildcache`/二进制并修正文档状态。
2. 接入 Diagnosis API Bearer Token。
3. 修复 Worker 并发限制。
4. 实现 Target Lease。

第二批再实现邮件告警：

1. PrometheusRule + promtool tests。
2. Alertmanager YAML + email template。
3. MailHog integration。
4. 真实邮箱 smoke。

这样可以先消除最影响可信度和安全性的缺口，再把邮件与可观测性做成可展示能力。

---

## 23. 实施时优先核对的官方资料

- [Prometheus Alertmanager Configuration](https://prometheus.io/docs/alerting/latest/configuration/)：SMTP、route、receiver、inhibit、template、`smtp_auth_password_file`。
- [Prometheus Notification Template Reference](https://prometheus.io/docs/alerting/latest/notifications/)：邮件模板数据结构和函数。
- [Prometheus Alerting Rules](https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/)：规则、`for`、labels 和 annotations。
- [Prometheus Operator API Reference](https://prometheus-operator.dev/docs/api-reference/api/)：PrometheusRule、AlertmanagerConfig、EmailConfig 和 SecretKeySelector。
- Kubernetes 官方文档：Lease、NetworkPolicy、RBAC、Secret、Pod Security Standards。
- controller-runtime/envtest 官方文档：真实 API server 集成测试。

依赖版本升级时先核对官方文档和 CRD schema，再调整本计划中的示例字段；不要仅凭旧博客复制配置。
