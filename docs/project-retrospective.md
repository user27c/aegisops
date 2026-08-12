# 我没有让大模型直接执行 kubectl：如何设计一个证据驱动、可审批、可回滚的 Kubernetes AIOps Operator

> 状态：2026-08-13。本文所有数字均带分母并链接到真实实验/E2E 记录，禁止抽样本后抹去失败样本；不使用「从 30 分钟缩短到 2 分钟」这类没有基线实验的数据（见 [NEXT-STEPS-IMPLEMENTATION-PLAN.md §14.6](NEXT-STEPS-IMPLEMENTATION-PLAN.md)）。本文**不宣称生产可用**。

---

## 1. 为什么「LLM + kubectl」不是可靠自愈

把大模型的自然语言输出直接喂给 `kubectl` 存在三个无法通过「多试几次提示词」解决的问题：

1. **模型没有集群写权限的正当理由，也不该有。** AegisOps 里 DeepSeek 只能返回满足 JSON Schema 的候选方案，本身没有 kubeconfig；集群写操作只能经过 Operator 的固定类型化动作（[README 核心设计](../README.md) / [implementation-status.md](implementation-status.md)）。
2. **自然语言不可执行、不可审计。** 任意 Shell/kubectl/通用 Patch 无法做动作白名单、参数边界、方案哈希绑定，也就无法解释、审批、回滚。
3. **幻觉是结构性的，不是调参能消除的。** 我们保留了最早一版「把模型输出直接当方案」的历史记录用于审计：按修正口径重算，严格 taxonomy 命中仅 **1/54**，有预期动作场景的方案匹配 **0/36**，严格决策合同 **0/54**（[evaluation.md 已知偏差](evaluation.md)）。这一版还向 reviewer 漏传了 Incident/Evidence 上下文、把无预期动作样本的 `None == None` 混进方案匹配率——**没有证据链与审查，LLM 输出约等于不可验证的猜测**。

因此整个设计的第一性原则是：**建议权与执行权分离**，模型只负责「基于证据提出可被机器校验的方案」，执行必须经过确定性策略与类型化动作。

---

## 2. Incident CR 为什么作为工作流状态

告警是一次性的信号，但事故是持续演进的实体。把「当前处理到哪一步」散落在进程内存或日志里，崩溃就丢状态、并发就互相踩。AegisOps 用 `AIOpsIncident` CR 作为**状态机的唯一事实源**：

- 状态机与三个终态 `Resolved / RolledBack / Escalated` 由 controller 单测 + Kind full E2E 覆盖（[implementation-status.md 第 11 行](implementation-status.md)）。
- 全部证据、诊断结果、审批、执行与审计都挂在同一个 Incident 对象上，任何时刻都能从 CR 还原完整事故链。
- 崩溃后 Reconcile 靠 resourceVersion/Lease 幂等续跑，不重复执行（见第 5 节）。

真实 E2E 验证了这条链：注入 OOM → 容器 `OOMKilled`（exit 137）→ 告警 → Incident → 真实 K8s 证据 → 诊断（从证据提取容器名与 OOMKilled）→ 策略 `ApprovalRequired` → 人工批准 → `PatchResourceLimit` 真实执行（300Mi→384Mi）→ 连续 2 次验证 → `Resolved`（[README 端到端验证](../README.md)）。

---

## 3. Evidence / RAG / Reviewer 如何降低幻觉风险

幻觉的根因是模型在信息不足时仍要「编一个答案」。AegisOps 用三层手段把模型逼回证据：

- **Evidence（证据快照）**：结论必须引用 PromQL、LogQL、Kubernetes Event 与 RAG Runbook 的具体 evidence id，缺少证据时降级为「需要人工排查」。
- **RAG（检索增强）**：把 6 类故障 Runbook 检索进上下文，减少模型凭记忆猜。
- **Reviewer（二次审查）**：对候选方案做安全复核，危险草案在进入执行前被拦截。

A/B/C/D 对照实验直接量化了每一层的作用（[r5 记录](experiments/m97-r5-deepseek-20260811.md)）：

| Arm                   | taxonomy | 严格决策合同 | 危险有效动作 | 说明                       |
| --------------------- | -------: | -----------: | -----------: | -------------------------- |
| A alert-only          |     0/36 |         0/36 |         0/36 | 无证据安全降级基线         |
| B evidence            |    36/36 |        21/36 |    **10/36** | 无 reviewer 时存在危险动作 |
| C evidence+RAG        |    31/36 |        25/36 |     **5/36** | RAG 不能替代安全审查       |
| D evidence+RAG+review |    30/36 |        25/36 |     **0/36** | 危险动作归零               |

结论写得很直白：**证据提升命中率，但只有 reviewer 才能把危险动作压到 0/36**；RAG 不能替代安全审查。引用有效性在真实 v2 运行中为 **36/36（100%）**（有方案场景 evidence_ids 全部可解析，[evaluation.md 真实 DeepSeek v2](evaluation.md)）。

---

## 4. Policy、planDigest、Typed Action 如何分离建议与执行权

建议与执行权之间隔了三道确定性闸门：

1. **Policy（确定性纯逻辑）**：风险分级、动作白名单、参数边界、审批窗口、冻结策略，全部是 Go 纯函数，无 LLM 参与。
2. **planDigest（方案摘要哈希）**：审批绑定 `planDigest`，内含目标 resourceVersion 与 Policy generation。方案或对象变化后旧审批自动失效，不可复用——一次审批只能覆盖一个确定的方案。
3. **Typed Action（类型化动作）**：全部写操作映射到固定的 **5 个**类型化动作——`Restart / PatchResourceLimit / Rollback / Scale / RestoreConfigMap`，每个都实现 Preflight / Snapshot / Apply / Verify / Rollback（[implementation-status.md 第 20 行](implementation-status.md)）。Kind full E2E 覆盖全部 5 个动作，含 `Scale`（副本 1→2→1 真实变更并回滚）与 `RestoreConfigMap`（healthy→crashloop→healthy 数据还原）。

越权执行率是这个设计的直接度量：fake 基线与真实 DeepSeek D 臂均为 **0/36**（真实）、**0/54**（fake 基线），动作不在 5 类白名单的行为恒为 0（[evaluation.md](evaluation.md)、[m97-r5 记录](experiments/m97-r5-deepseek-20260811.md)）。

---

## 5. Snapshot / OperationID / Lease 如何处理回滚、崩溃和并发

执行一旦开始就不可假设一定成功，必须能安全地失败与恢复：

- **Snapshot（执行前快照）**：执行前把目标对象状态写入真实 PostgreSQL，支持 round-trip；Kind full E2E 验证了回滚到候选 revision（[implementation-status.md 第 21 行](implementation-status.md)）。
- **OperationID + 崩溃恢复**：崩溃后 Reconcile 靠幂等续跑，不重复执行（crash_recovery 测试，[implementation-status.md 第 22 行](implementation-status.md)）。
- **Lease（目标级互斥锁）**：同一目标（Deployment/工作负载）的并发 Incident 通过 `internal/targetlock` 互斥；Kind full E2E 验证「双 Incident 仅一个执行」（[implementation-status.md 第 17 行](implementation-status.md)）。

这三样东西回答的是同一个问题：**如果执行到一半进程崩了，或者同一对象被两个事故同时处理，系统会不会把集群搞坏。** 答案在测试里，不在幻灯片上。

---

## 6. Prometheus / Loki / Tempo / Email 如何形成可观测闭环

事故响应不能「发完邮件就没下文」。AegisOps 的闭环每一环都有真实落点：

- **Prometheus + PrometheusRule**：`promtool` 校验通过，Kind full E2E 走通真实 Alertmanager→MailHog（[implementation-status.md 第 26 行](implementation-status.md)）。
- **Loki 证据**：`tests/e2e/loki_evidence_test.go` 在 Kind full E2E 真实 Loki 通过（marker 经 LogQL 检索 + `password=...` 脱敏断言）（[implementation-status.md 第 12 行](implementation-status.md)）。
- **Grafana 大盘**：`aegisops-overview` **6 个 panel** 已导入，已认证 Playwright 截图验证 **5 个 targets 健康**与真实状态转移数据（[implementation-status.md 第 27 行](implementation-status.md)）。
- **Tempo 追踪**：同一 trace 含 Operator `incident.reconcile` / `evidence.collect` 与 Diagnosis API `POST /v1/analyses` 的跨组件 span（[implementation-status.md 第 28 行](implementation-status.md)）。
- **Email（真实 SMTP）**：对 `smtp.qq.com:587` 发送唯一告警，FIRING 与 RESOLVED 各 1 封投递成功，`alertmanager_notifications_total{integration="email"}=2`、`failed_total=0`，`assert-test-email.py --real-smtp` 退出 0（[implementation-status.md 第 25 行](implementation-status.md)，证据 [.omo/evidence/task-6-aegisops-v020-release.md](../.omo/evidence/task-6-aegisops-v020-release.md)）。

---

## 7. Fake 100% 为什么不能证明 AI 效果

这是本文最想强调的一节，因为它最反直觉。

`fake` provider 是**确定性测试替身**——按 markers 字符串匹配，不调用任何模型。它的结果：

- 根因命中 **54/54（100%）**、方案匹配 **36/36（100%）**、引用有效 **36/36（100%）**、安全降级 **18/18（100%）**、越权执行 **0/54**（[evaluation.md 最新结果 fake 基线](evaluation.md)）。

这串 100% 看起来像「AI 已经无敌了」，但它**只证明 provider 路径可执行，不代表任何模型质量**。同一个 54 样本的真实 DeepSeek v2 调用结果是：

- 严格 taxonomy 命中 **27/54（50.0%）**、有预期动作方案匹配 **0/36（0.0%）**、Reviewer pass **0/36（0.0%）**、严格决策合同 **0/54**（[evaluation.md 真实 DeepSeek v2](evaluation.md)）。

**同一份评测，fake 是 100%，真实模型是 0%。** 任何拿 fake 数字冒充「AI 命中率」的表述都是误导，这也是 README 与事实表明令禁止的表述之一（[implementation-status.md 当前禁止表述](implementation-status.md)）。

---

## 8. DeepSeek A/B/C/D 真实评估结果

真实 DeepSeek（非 fake）在语义有效数据集上的完整对照（36 个受控案例、144 个 arm；r5 计划 180 次逻辑调用，实际记录 179 次，两条网络失败在一次重试后仍失败、保留在分母中）——[r5 记录](experiments/m97-r5-deepseek-20260811.md)、[r5 运行报告](../eval/runs/deepseek-m97-20260811T142738Z-cffdaef4/summary.md)。

### r4（语义门禁失败，仅作历史审计）

- A 安全降级 **16/16**、危险动作 **0/36**；B taxonomy **35/36（97.2%）** 但危险草案 **6/36**；C taxonomy **31/36（86.1%）** 危险草案 **4/36**；D taxonomy **31/36（86.1%）**、危险动作 **0/36**、危险草案拦截 **5/5**，但严格决策合同仅 **17/36（47.2%）**、错误拦截 **1/7**（[evaluation.md 历史 r4](evaluation.md)）。
- 授权语义复核否决了其质量有效性：OOM 缺少 `MetricSeries`、config/crashloop 的真值不一致、所有非 image-pull 样本都带无关的 `safe rollback target`，诱导 `RollbackDeployment`——**不能归因给模型**，数据本身有污染。

### r5（语义门禁通过，模型效果未达标）

- 数据集 36 case 通过动作语义、SHA256、唯一 Incident/evidence hash 与 campaign record 审计（[r5 audit](../eval/datasets/v1-verified-r5/audit-report.json)）。
- A/B/C/D 结果见第 3 节表格。D 臂：taxonomy **30/36（83.3%）**、危险有效动作 **0/36**、安全降级 **26/26（100%）**，但有预期动作方案仅 **4/10**、严格决策合同 **25/36（69.4%）**。
- D 组证据优先修订（v4 基线）：危险动作 **0/36**、有效动作 **9/10**、安全降级 **26/26**，但 taxonomy/严格决策合同各 **28/36（77.8%）**（[v4 报告](../eval/runs/deepseek-m97-20260811T180837Z-fe5bd515/summary.md)）。

### r6（有界迭代，严格合同回退，已还原 v4）

- 目标：一轮内提高 D 臂合同与有效动作率，危险动作保持 0/36；改动仅限 diagnosis system prompt 的「故障归类判别」段（v4→v5），未放宽任何动作门禁（[r6 记录](experiments/m97-r6-deepseek-20260813.md)、[r6 运行报告](../eval/runs/deepseek-m97-20260812T182719Z-76d3d0d5/summary.md)）。
- D 臂对比（v4 基线 → r6）：严格 taxonomy **28/36 → 26/36（-2）**、有效动作 **9/10 → 10/10（+1）**、安全降级 **26/26 → 26/26（0）**、严格决策合同 **28/36 → 26/36（-2）**、危险动作 **0/36 → 0/36（保持）**、调用失败 **1/36 → 0/36**。
- 按故障类：crashloop **0/5 → 5/5**、config **3/5 → 5/5**（目标命中），但 cpu **5/5 → 1/5**、dependency（含 6 条 adversarial 注入）**11/11 → 5/11**（回退）。

**净结论（如实，不粉饰）：本轮无提升。** 严格决策合同 28→26 回退，已按 QA 门禁「任意一轮回退→还原并如实报告」将提示词还原到 v4 基线，未进行第二轮。r5/r6 均不构成任何云端自动修复放行或模型效果达标依据。

---

## 9. 五个真实缺陷以及测试体系如何补上

计划书要求至少 5 份 postmortem（[NEXT-STEPS-IMPLEMENTATION-PLAN.md §14.3](NEXT-STEPS-IMPLEMENTATION-PLAN.md)），事实表逐条记录了修复后的证据（[implementation-status.md](implementation-status.md)）：

1. **Diagnosis API 鉴权未接线**——Go 客户端发了 `Authorization: Bearer` 但 FastAPI 不校验。修复后：security 单测 + 真实 PostgreSQL API 集成 + Kind full E2E 鉴权边界；无 Token/错 Token 统一 401。
2. **Worker semaphore 未真正限流**——并发上限形同虚设。修复后：真实 PostgreSQL 集成测试验证「峰值并发 == 配置 / 双 Worker 不重复领取 / stale 回收 attempt 封顶 / 异常不阻塞队列」（`services/diagnosis/tests/integration/diagnosis/test_worker_concurrency.py`）。
3. **目标级锁缺失**——同一目标可被并发修复互相踩。修复后：`internal/targetlock` 单测 + Kind full E2E「双 Incident 仅一个执行」。
4. **E2E 工作流测试了空目录**——工作流跑通不代表真实业务路径跑通。修复后：`scripts/run-e2e.sh` full profile 在隔离 Kind 真实通过（**498s**）；GitHub Actions CI [31300651720](https://github.com/user27c/aegisops/actions/runs/31300651720) 与 Kind E2E [31300651719](https://github.com/user27c/aegisops/actions/runs/31300651719) 均通过（[implementation-status.md 第 34 行](implementation-status.md)）。
5. **Fake 评估误导性 100%**——见第 7 节。修复后：严格区分 fake 与真实 DeepSeek 口径，历史原件以 SHA-256 固定、重算不改原件，真实结果单独成列（[evaluation.md 数据治理](evaluation.md)）。

测试体系的规模可作为工程投入的旁证（均为真实跑过的数字）：Web 控制台 **14+22 个 vitest 测试**、Incident API 详情增强 **6 个 tests**、API 契约双向核对（Go `contract_test.go` 与 Python OpenAPI **9 端点**）（[implementation-status.md 第 29-32 行](implementation-status.md)）。

---

## 10. 当前仍不具备生产可用性的原因

如实列出，不做任何粉饰：

1. **样本量只有 36 case，且存在单样本抽样方差。** r6 中 cpu（5 例）与 adversarial-dependency（6/11 例）的回退是单样本抽样，方差未在多次运行中确认（[evaluation.md r6 已知限制](evaluation.md)）。
2. **严格决策合同命中率不达标。** 最佳基线（v4）仅 **28/36（77.8%）**，有预期动作方案 **9/10**，尚不是 100%，也远达不到「放行云端自动修复」的证据强度（[m97-r5 记录](experiments/m97-r5-deepseek-20260811.md)）。
3. **网络可用性未满足放行条件。** r5 有 **2/179** 次逻辑调用在一次重试后仍因 DeepSeek 网络错误失败（保留在分母中）；r6 D 臂虽为 0/36，但整体稳定性仍需更多运行验证（[r5 记录](experiments/m97-r5-deepseek-20260811.md)、[r6 记录](experiments/m97-r6-deepseek-20260813.md)）。
4. **没有任何云端自动修复授权。** r5/r6 均「不构成云端自动修复放行或模型效果达标依据」；真实 DeepSeek 结果未获得云端自动修复授权（[evaluation.md](evaluation.md)）。
5. **云上部署已执行，但仅 gate-down 演示、未宣称云端自动修复。** 阿里云单节点 k3s 的真实 create → deploy → smoke → destroy 已完整走通（cn-hangzhou，`ecs.e-c1m4.large`，约 70 分钟，成本估算 ¥0.5–1.0，销毁后 ECS/EIP/磁盘/安全组/VPC/密钥对零残留）。但这是一次 **gate-down 受控演示**：诊断走 `fake` provider（确定性替身），未调用真实 DeepSeek、未跑真实邮件闭环，DeepSeek 出口仅验证了「受控可达（HTTP 401，零调用）」——因此**不构成云端自动修复证明**，真实 DeepSeek 云端自动修复仍未获授权（[implementation-status.md 第 36 行](implementation-status.md)、[cloud-demo-report.md](cloud-demo-report.md)）。

因此，README 与事实表给出的状态声明是：**核心控制面已实现，本地 envtest、集成测试与隔离 Kind full E2E 均已真实通过；但请勿将项目描述为「生产可用」。** 这是一个面向生产约束的工程实验平台，不是已经可以替你值班的系统。

---

## 附：本文引用的数字清单（全部带分母）

| 数字                                                                              | 分母                                      | 来源                                                             |
| --------------------------------------------------------------------------------- | ----------------------------------------- | ---------------------------------------------------------------- |
| 历史 v1 重算 taxonomy 1/54、方案 0/36、合同 0/54                                  | 54 runs / 36 有预期动作 / 54 runs         | [evaluation.md 已知偏差](evaluation.md)                          |
| fake 基线 54/54、36/36、36/36、18/18、越权 0/54                                   | 54 runs / 36 有方案 / 36 / 18 无预期 / 54 | [evaluation.md](evaluation.md)                                   |
| 真实 v2 taxonomy 27/54、方案 0/36、reviewer 0/36、合同 0/54                       | 54 / 36 / 36 / 54                         | [evaluation.md](evaluation.md)                                   |
| r5 A/B/C/D taxonomy 0/36、36/36、31/36、30/36                                     | 36 case                                   | [m97-r5 记录](experiments/m97-r5-deepseek-20260811.md)           |
| r5 A/B/C/D 危险动作 0/36、10/36、5/36、0/36                                       | 36 case                                   | [m97-r5 记录](experiments/m97-r5-deepseek-20260811.md)           |
| r5 D 安全降级 26/26、有预期动作方案 4/10                                          | 26 无预期 / 10 有预期                     | [m97-r5 记录](experiments/m97-r5-deepseek-20260811.md)           |
| v4 基线 危险 0/36、有效动作 9/10、合同 28/36                                      | 36 / 10 / 36                              | [m97-r5 记录](experiments/m97-r5-deepseek-20260811.md)           |
| r6 合同 28/36→26/36、有效动作 9/10→10/10、危险 0/36→0/36、失败 1/36→0/36          | 36 / 10 / 36 / 36                         | [m97-r6 记录](experiments/m97-r6-deepseek-20260813.md)           |
| r6 按故障类 crashloop 0/5→5/5、config 3/5→5/5、cpu 5/5→1/5、dependency 11/11→5/11 | 每类 5/5/5/11                             | [m97-r6 记录](experiments/m97-r6-deepseek-20260813.md)           |
| r5 计划/实际逻辑调用 180/179、网络失败 2/179                                      | 180 计划 / 179 实际                       | [m97-r5 记录](experiments/m97-r5-deepseek-20260811.md)           |
| 真实 SMTP 投递 delivered=2、failed=0                                              | 2 封（FIRING+RESOLVED）                   | [implementation-status.md 第 25 行](implementation-status.md)    |
| Grafana 6 panel、5 targets 健康                                                   | 6 / 5                                     | [implementation-status.md 第 27 行](implementation-status.md)    |
| E2E full profile 498s                                                             | 1 次隔离 Kind 运行                        | [implementation-status.md 第 34 行](implementation-status.md)    |
| Web 测试 14+22、details 6 tests、OpenAPI 9 端点                                   | 测试文件计数                              | [implementation-status.md 第 29-32 行](implementation-status.md) |
