# AegisOps v0.2.0 项目完成报告

> 完成时间：2026-08-13。本报告是 v0.2.0 交付的最终冻结记录，是本次发布的权威完成文档。
>
> 所有数字均带分母并链接到真实证据；所有限制如实陈述。**本报告不宣称"生产可用"，也不把 fake 诊断与真实 DeepSeek 数字混为一谈。**

---

## 0. 发布前修订记录（2026-08-13）

v0.2.0 最初在本地冻结于 `4f89b60` 但**未公开发布**（无远端 tag、无 GitHub Release、镜像未推送）。
发布审查发现并修复了以下问题，最终 `v0.2.0` tag 指向包含全部修复的最终提交：

1. **approvalTTL 真正受 Policy 控制**（最高优先级）：此前 Incident API 硬编码 10 分钟，Policy 配置的 `approvalTTL` 不生效。已把 TTL 冻结进 `PolicyDecisionStatus`、审批对象据此计算 `ExpiresAt`，并在 evaluator 增加有效期重校验；补单测与 E2E（提交 `98badcb`）。
2. **GitHub Trivy 非阻断门禁**：`security.yml` 未传 `--exit-code 1` 且只扫 operator 一个镜像。已加阻断、pin 版本、扫全 5 镜像（提交 `730c114`）。
3. **CI 两处失败**：golangci-lint 预编译二进制（go1.25 构建）低于 go.mod 目标 1.26.5 而拒绝运行；Python `ruff` import 排序错误。已修复（提交 `730c114`）。
4. **LICENSE 缺失**：README 声明 Apache 2.0 但根目录无 LICENSE，已补标准 Apache-2.0（提交 `984bcb8`）。
5. **完成报告事实校准**：37 行→29 项、SHA 冻结不再漂移、digest→本地 image ID、r5 四臂与 D-only v4 拆分、生产不可用原因扩充（提交 `dc78041`）。
6. **公开脱敏证据包**：新增 `docs/releases/v0.2.0/evidence/`，外部读者可复核发布门禁、真实 SMTP、云端演示与 DeepSeek 评估；发布清单中原指向 gitignored `.omo/` 的门禁证据链接已改指该公开证据包（提交 `dc78041`），仅存于内部记录、无公开对应物的链接仍保留 `.omo/` 并标注。
7. **发布后文档一致性修正**：证据包 README 区分「门禁执行时点（本地未推送）」与「公开发布最终状态（tag `a9dbace`、镜像已推送）」；删除完成报告与发布清单中两处「镜像未推送」残留；统一 tag SHA 指代（`v0.2.0` = `a9dbace`）。

---

## 1. 概述

AegisOps 是一个面向 Kubernetes 的证据驱动智能诊断与受控自愈 Operator：Alertmanager 告警 → 指纹去重 → 多源证据快照 → RAG 检索 → DeepSeek 诊断 → 二次审查 → 确定性策略校验 → 人工审批或低风险自动放行 → Operator 类型化执行 → 健康验证 → 失败回滚 → 事故报告。

v0.2.0 把项目从「核心 MVP 已完成」推进到「可发布」：补齐了事实表里剩余的验证缺口（Scale/RestoreConfigMap 真实 E2E、真实 Loki 证据、Worker 并发与 stale 回收、跨组件链路追踪），跑完了真实 DeepSeek 的对照评估并如实记录，用真实邮箱与阿里云账号各做了一次真实 SMTP smoke 与一次云端 create/smoke/destroy 演示，最后产出作品集（postmortem、截图、复盘、演练报告、发布清单）。

核心完成事实（全部来自执行记录，不粉饰）：

- **19 项实现任务全部完成**：17 项计划任务（T1–T17）+ 2 项追加任务（T18 NetworkPolicy 缺口修复、T19 剩余工作提交与提交组织）。终审波 **F1–F4 全部 APPROVE**。证据见执行计划 [`.omo/plans/aegisops-v020-release.md`](../.omo/plans/aegisops-v020-release.md)（维护者内部记录，仓库内不可见）的 Todos 与 Final verification wave（每项均 `[x]`）。
- 发布门禁 `scripts/release-check.sh --with-integration-e2e` **exit 0**（全绿）。执行记录公开脱敏版见 [`docs/releases/v0.2.0/evidence/release-gates.md`](releases/v0.2.0/evidence/release-gates.md)（原始未脱敏记录 `.omo/evidence/task-14-aegisops-v020-release.md` 仅供维护者核对）。
- 真实 DeepSeek 评估结论：**不足以放行云端自动修复**（详见第 7 节诚实边界）。

安全边界贯穿始终：DeepSeek 与诊断服务没有 Kubernetes 写权限；模型只产出满足 JSON Schema 的候选方案，集群写操作只能经 Operator 的 5 个固定类型化动作；中风险动作必须审批且绑定不可复用的 `planDigest`；每个动作都实现 Preflight / Snapshot / Apply / Verify / Rollback。

---

## 2. 完成度总览

里程碑状态（来源：[`README.md`](../README.md) 里程碑表与 [`docs/implementation-status.md`](implementation-status.md)）：

| 里程碑 | 内容                                     | 状态                                                  |
| ------ | ---------------------------------------- | ----------------------------------------------------- |
| M0     | 仓库与工具链                             | ✅                                                    |
| M1     | CRD + Gateway + 只读 Console             | ✅                                                    |
| M2     | Controller 状态机 + Evidence             | ✅                                                    |
| M3     | Diagnosis API、Worker、RAG               | ✅                                                    |
| M4     | Policy + Approval                        | ✅                                                    |
| M5     | 5 个 Typed Actions                       | ✅                                                    |
| M6     | Verification、Audit、Crash Recovery      | ✅                                                    |
| M7     | Fault Lab + Observability                | ✅                                                    |
| M8     | E2E、Eval、文档收尾                      | ✅                                                    |
| M9.x   | v0.2.0 收尾（鉴权/锁/邮件/E2E/真实评估） | ✅ 完成（发布门禁全绿；真实评估与云端自动修复未达标） |

实施状态事实表 [`docs/implementation-status.md`](implementation-status.md) 是权威逐项事实表，**29 项能力条目**（26 yes / 3 partial）中每项只允许 `yes / no / partial`。关键条目最终状态：Alertmanager webhook 接入与去重 yes、CRD schema/CEL 校验 yes、Incident 状态机 yes、多源证据采集（K8s/Prom/Loki）yes、真实 SMTP yes、OTel 追踪 yes、真实 DeepSeek partial、云上部署 partial（gate-down 演示）。注：事实表里 3 个 partial 分别为「5 个类型化动作」「真实 DeepSeek 评估」「云上部署」，其 partial 指向**真实环境验证**或**模型效果/云端自动修复放行**尚未达成，而非实现缺失；具体口径见各条目「证据」列。

Git 发布快照（冻结后不再漂移）：代码冻结点 `bd9b93a`（[`docs/release/v0.2.0-checklist.md`](release/v0.2.0-checklist.md) 记录），其上是文档冻结提交 `4f89b60`；`v0.2.0` tag 指向包含第 0 节全部发布前修复的最终提交（本节编写于该提交内）。

---

## 3. 交付清单

### 3.1 五个 OCI 镜像（tag `0.2.0`，registry `ghcr.io/user27c`，已由 Release workflow 推送）

> 重要：下表第三列是**本地 image ID**（`docker image inspect --format '{{.Id}}'` 的输出，
> 属本地 image/config ID），**不是**可从 GHCR 拉取的 OCI manifest digest。registry digest
> 见 GHCR（`docker manifest inspect ghcr.io/user27c/<image>:0.2.0`）。镜像由 `.github/workflows/release.yml`
> 在 tag push 时以 buildx 多架构（amd64/arm64）构建并推送到 GHCR。

| 镜像                                            | 本地 image ID（sha256）                                                  |
| ----------------------------------------------- | ------------------------------------------------------------------------- |
| `ghcr.io/user27c/aegisops-operator:0.2.0`      | `sha256:22b8baa1c2a5d812e0fc8ffcd5cac76f044fccd9b6d53e5674d9bf56caf77e50` |
| `ghcr.io/user27c/aegisops-alert-gateway:0.2.0` | `sha256:bf68070f34f92aa02d30bf1a9bf898f51605818271d4248c37a6bfb6afa5e925` |
| `ghcr.io/user27c/aegisops-incident-api:0.2.0`  | `sha256:a7b043e021b39b8d6b554d6b324e6f2cfddd8487f7dabba3ebd80c35ce03e6cf` |
| `ghcr.io/user27c/aegisops-diagnosis:0.2.0`     | `sha256:ded811d0b61b9b667370b467b580bf5b23dce765f6768cc3b0a1f49fea287827` |
| `ghcr.io/user27c/fault-lab:0.2.0`              | `sha256:97cdf19f7a2c543f7cda603cbca30d5699c19b8ad488e4c95d4dd513bb802239` |

镜像 tag 固定为 `0.2.0`（Release workflow 由 git tag `v0.2.0` 去掉 `v` 前缀），禁止 `latest`；本地构建脚本仍可用 `scripts/build-images.sh --registry ghcr.io/user27c --tag v0.2.0`。

### 3.2 Helm Chart

- 包：`dist/aegisops-0.2.0.tgz`（`helm package deploy/helm/aegisops/ -d dist/`）
- 版本：`version 0.2.0` / `appVersion 0.2.0`
- SHA256：`5b0da51db1920c548db9b15e7cf0b32d6fa177378eced8392459cf096ad9e341`

### 3.3 SBOM

- 目录：`dist/sbom-v0.2.0/`，SPDX JSON（syft 1.51.0），**5 份**（5 个镜像各 1 份）。

完整清单见 [`docs/release/v0.2.0-checklist.md`](release/v0.2.0-checklist.md)（含升级说明与已知限制）。

---

## 4. 质量门禁

`scripts/release-check.sh --with-integration-e2e --artifact-dir artifacts/release-v020` 最终 **exit 0**，最后一行输出 `[INFO] release-check 全部通过`（[`.omo/evidence/task-14-aegisops-v020-release.md`](../.omo/evidence/task-14-aegisops-v020-release.md)）。

| 门禁项                             | 结果                   | 关键数字                                                                            |
| ---------------------------------- | ---------------------- | ----------------------------------------------------------------------------------- |
| 工具链                             | ✅                     | promtool 3.13.2 / kubeconform 0.8.0 / syft 1.51.0 / trivy 0.73.0                    |
| check-repo-hygiene.sh              | ✅ exit 0              | 无跟踪缓存/敏感文件                                                                 |
| make verify-generated              | ✅ exit 0              | 生成文件无漂移                                                                      |
| make verify                        | ✅ exit 0              | golangci-lint 0 issues                                                              |
| 单元测试覆盖率                     | ✅                     | controller **80.2%** / executor **80.0%** / policy **92.7%**（Go 核心包 ≥80% 门槛） |
| make test-envtest                  | ✅ exit 0              | envtest 1.35.0                                                                      |
| make test-integration              | ✅ exit 0              | 8 pytest + Alertmanager/MailHog 邮件链路                                            |
| terraform fmt/validate             | ✅ exit 0              | 阿里云 Terraform 配置有效                                                           |
| kubeconform -strict                | ✅ exit 0              | 41 资源 Valid 37 / Invalid 0 / Skipped 4（第三方 CRD）                              |
| promtool check/test rules          | ✅ exit 0              | 1 rules + test SUCCESS                                                              |
| build-images（5 镜像）             | ✅                     | Go 1.26.5 + alpine 3.22 + python 3.12-slim                                          |
| trivy HIGH/CRITICAL                | ✅ **5/5 镜像 0 漏洞** | 无 HIGH/CRITICAL                                                                    |
| Kind E2E（full）                   | ✅ exit 0              | 9 个顶层用例，**901.6s** 全部通过                                                   |
| sanitize-e2e-artifacts --scan-only | ✅ exit 0              | 无敏感信息残留                                                                      |

### 4.1 Kind full E2E 用例明细（最后一次全绿运行）

| 用例                                       | 结果 | 时长                  |
| ------------------------------------------ | ---- | --------------------- |
| TestE2EAlertEmail                          | ✅   | 282.50s               |
| TestE2EApprovalPatchMemory                 | ✅   | 58.13s                |
| TestE2EApprovalScaleCPUFailClosed          | ✅   | 333.32s（1→2→1 回滚） |
| TestE2EAutoRestart                         | ✅   | 28.42s                |
| TestE2ELokiEvidenceInPack                  | ✅   | 0.22s                 |
| TestE2ELokiFailureStaysPartial             | ✅   | 0.03s                 |
| TestE2ERestoreConfigMapFromImmutableBackup | ✅   | 110.59s               |
| TestE2ERollbackDeployment                  | ✅   | 56.35s                |
| TestE2ESecurityBoundaries                  | ✅   | 32.02s（5 个子用例）  |

合计 **9 个顶层 `TestE2E` 函数，总时长 901.6s**，覆盖告警到邮件、审批补丁、ScaleCPU fail-closed、Auto Restart、真实 Loki 证据、RestoreConfigMap、回滚、安全边界。

注：发布门禁证据 task-14 原文记「11 用例」，经与代码核对为 9 个顶层 `TestE2E` 函数，901.6s 为这 9 个用例时长之和（算术吻合），已在此与发布清单中统一口径。

---

## 5. 真实证据

### 5.1 真实 SMTP（smtp.qq.com）

对 `smtp.qq.com:587`（发件 = 收件，均脱敏）发送唯一告警：**FIRING 与 RESOLVED 各 1 封投递成功**，`alertmanager_notifications_total{integration="email"}=2`、`failed_total=0`，`assert-test-email.py --real-smtp` 退出 0。证据见 [`.omo/evidence/task-6-aegisops-v020-release.md`](../.omo/evidence/task-6-aegisops-v020-release.md) 与 [`docs/implementation-status.md`](implementation-status.md) 第 25 行。

### 5.2 阿里云 k3s 云端 create → smoke → destroy

真实执行（2026-08-13，cn-hangzhou 单节点 k3s，`ecs.e-c1m4.large` 2 vCPU / 8 GiB），证据见 [`docs/cloud-demo-report.md`](cloud-demo-report.md) 与 [`.omo/evidence/task-7-aegisops-v020-release.md`](../.omo/evidence/task-7-aegisops-v020-release.md)：

- Terraform fmt / validate / plan / apply / destroy 全部真实通过（8 resources added，8 destroyed）。
- 总运行约 **70 分钟**（约 1.17 计费小时），成本估算 **¥0.5–1.0**（单价 ¥0.4635/小时，按小时向上取整可能计 2 小时；为估算，非精确账单）。
- 销毁后 ECS / EIP / 磁盘 / 安全组 / VPC / 密钥对**零计费残留**（aliyun CLI 逐项查询 `TotalCount 0`）。
- **这是 gate-down 受控演示**：诊断走 `fake` provider，未调用真实 DeepSeek、未跑真实邮件闭环；DeepSeek 出口仅验证「受控可达（HTTP 401 零调用）」。不构成云端自动修复证明。

### 5.3 真实 DeepSeek 评估（r5 / r6）

数据集 `v1-verified-r5`（36 case，6 类故障，144 arm，语义有效）。实验记录见 [`docs/experiments/m97-r5-deepseek-20260811.md`](experiments/m97-r5-deepseek-20260811.md) 与 [`docs/experiments/m97-r6-deepseek-20260813.md`](experiments/m97-r6-deepseek-20260813.md)。

**r5（v4 基线，D 组证据优先）**：

- 严格决策合同 **28/36（77.8%）**
- 危险动作 **0/36**
- 有效动作 **9/10**
- 安全降级 **26/26**
- 计划 180 次逻辑调用，实际记录 179 次；2 条网络失败在一次重试后仍失败，保留在分母中

> 口径说明：上述「r5 v4 基线 9/10」是**后续单独运行的 D-only prompt v4 修订**，
> 与下方「初始四臂 A/B/C/D」不属于同一次运行。初始四臂的 D 臂（evidence+RAG+review）
> 危险有效动作虽为 0/36，但有效动作仅 **4/10**；此后针对 D 臂迭代 prompt 得到 v4 修订
> 才把有效动作提升到 9/10。两者并列列出，避免把后续修订误读成初始四臂的一部分。

**r6（有界迭代，diagnosis-v5，已还原 v4）**：

- 严格决策合同 **28/36 → 26/36（回退 -2）**
- 有效动作 9/10 → 10/10（+1）
- 危险动作 0/36 → 0/36（保持）
- 调用失败 1/36 → 0/36
- 已按 QA 门禁「任意一轮回退 → 还原并如实报告」将 diagnosis 提示词还原到 v4 基线，未进行第二轮

对照实验（r5 全 A/B/C/D，**初始四臂单次运行**）直接量化了每一层的作用：

| Arm                   | taxonomy | 严格决策合同 | 危险有效动作 |
| --------------------- | -------: | -----------: | -----------: |
| A alert-only          |     0/36 |         0/36 |         0/36 |
| B evidence            |    36/36 |        21/36 |        10/36 |
| C evidence+RAG        |    31/36 |        25/36 |         5/36 |
| D evidence+RAG+review |    30/36 |        25/36 |     **0/36** |

> D 臂（初始四臂）有预期动作方案仅 **4/10**；后续 D-only prompt v4 修订才把有效动作提升到 9/10（见上文 r5 v4 基线）。

结论：**证据提升命中率，但只有 reviewer 才能把危险动作压到 0/36**；RAG 不能替代安全审查。

fake 基线（确定性测试替身，不代表模型质量）为对照：根因命中 54/54（100%）、方案匹配 36/36、越权执行 0/54。**这些 100% 只证明 provider 路径可执行，不代表任何模型质量，严禁与真实 DeepSeek 数字混为一谈。**

---

## 6. 关键修复

发布门禁期修复了 9 个问题（9 次提交，均含针对性 git add，未 push），加上发布收敛期的安全修复与 NetworkPolicy 修复：

### 6.1 安全：审计 actor 不再落原始 token

`StaticTokenAuthenticator` 的 `Principal.Subject` 此前直接存原始 token，会经时间线接口与截图泄漏凭证。修复为派生稳定的 SHA256 前 8 字节十六进制标识 `token-<hex16>`，并新增回归测试断言 Subject 永不为原始 token。提交 `3c4b155`。

### 6.2 正确性：verifier PromQL 修复 cAdvisor 双计越界

`sum by (pod)` 把 cAdvisor 的 pod 级聚合序列（无 container 标签）与 container 级序列重复相加，限流比例得 2（越界），使 fail-closed 验证误判。修复为查询加 `container!=""` 过滤。提交 `201be30`。

### 6.3 安全：3 处 chart NetworkPolicy 缺口

(1) `aegisops-postgres` NP ingress 未放行 `component=migrations`，alembic 迁移卡在 `wait-postgres`；(2) Prometheus 无法抓取指标（gateway NP 误写 `monitoring` 命名空间，其余组件无指标 ingress）；(3) 组件缺 API server 出站（default-deny 阻断 leader election）。已在仓库 chart 修复（提交 `27cfe37`，任务 T18），云端演示时先以运行时 patch 绕过并记录。

### 6.4 稳定性：多处 E2E 竞态

- verifier 指标双计（见 6.2）。
- 故障注入时序：ScaleCPU 注入前未等待 faultlab 稳定；RestoreConfigMap 单次探测命中 crashloop 重启存活窗口。提交 `dfda4fb`。
- 滚动更新竞态：旧 Pod 终止前 Service 可能把 /inject 路由到旧 Pod，随后旧 Pod 终止导致 CPU 故障被清除。提交 `bd9b93a`。
- 审批 TOCTOU：目标 resourceVersion 变化使 planDigest 刷新、旧审批失效。提交 `442438e`。
- 发布门禁侧：promtool 误对 LogQL 跑 promtool、kubeconform 缺 `-ignore-missing-schemas`（`fcde76d`）、SBOM 写入触发 PII 扫描（`01306c6`）、门禁先停止 e2e 遗留 port-forward 避免端口冲突（`03d6afc`）、Go 1.26.5 + 依赖升级修复镜像 HIGH/CRITICAL（`720be32`）、补 metrics_reconciler 测试使 controller 覆盖率达标（`f61118f`）。

---

## 7. 诚实边界

以下限制如实列出，不做粉饰：

1. **DeepSeek 模型质量不足以放行云端自动修复。** 最佳基线（v4）严格决策合同仅 **28/36（77.8%）**，有效动作 9/10，远达不到「放行云端自动修复」的证据强度。r6 有界迭代 28/36 → 26/36 回退，已还原 v4。真实 DeepSeek 结果未获得云端自动修复授权。
2. **云端部署是 gate-down 受控演示，不是云端自动修复证明。** 阿里云 k3s create/smoke/destroy 已真实走通，但诊断用 `fake` provider、未调用真实 DeepSeek、未跑真实邮件闭环。
3. **样本量仅 36 case，存在单样本抽样方差。** r6 中 cpu（5 例）与 adversarial-dependency（6/11 例）的回退是单样本抽样，方差未在多次运行中确认。
4. **E2E 存在既有间歇性竞态。** 修复消除了确定性/高频竞态，最后一次运行 9/9 全绿；但不同轮次仍可能触发不同用例的间歇性竞态（如 RecoveredWithoutAction、同目标互斥超时）。
5. **`buildcache/` 残留仅存在于 git 历史**（提交 `fa3e282`、`1ca7456`），当前工作树干净（`git ls-files 'buildcache/**'` 为空）。
6. **镜像由 Release workflow 在 CI 内构建推送**（tag `0.2.0` 与 `sha-*`，amd64/arm64）；§3.1 的镜像表为本地 image ID，与 GHCR manifest digest 不同，不可混用。
7. **chart NetworkPolicy 缺口已在仓库修复**，但 cloud-init k3s 镜像源、cloud-smoke.sh 陈旧等问题在云端以运行时 patch / 等价命令绕过，尚未全部固化进 chart。
8. **operator 在 `aegisops-system` 创建 leader-election Event 被拒**（`events is forbidden`），不影响主流程。
9. **认证为静态 token，非生产级身份体系。** 当前 viewer/approver 鉴权基于预共享静态 token（SHA256 派生标识，见 6.1），未接入 OIDC/OAuth、mTLS 或短期凭据轮换。
10. **单节点、单 PostgreSQL，无高可用拓扑。** 阿里云演示为单节点 k3s + 单 PostgreSQL；无 PDB、HPA、Pod 拓扑分散（topology spread）或多副本冗余，控制面与数据面均未做故障转移演练。
11. **备份恢复与升级演练不足。** PostgreSQL 备份/恢复、Chart 原地升级、CRD/API 兼容升级尚未经过真实演练。
12. **负载容量与长期稳定性数据不足。** 缺少并发 Incident 容量上限、持续告警下的 P50/P95 延迟、24/72 小时长期运行、重复告警抑制效果等系统级指标。

因此状态声明是：**核心控制面已实现，本地 envtest、集成测试与隔离 Kind full E2E 均已真实通过；但请勿将项目描述为「生产可用」。** 这是一个面向生产约束的工程实验平台，不是已经可以替你值班的系统。

---

## 8. 文档与链接索引

事实与状态：

- 实施状态事实表：[`docs/implementation-status.md`](implementation-status.md)
- 评估方法：[`docs/evaluation.md`](evaluation.md)
- 云上部署报告：[`docs/cloud-demo-report.md`](cloud-demo-report.md)
- 项目复盘文章：[`docs/project-retrospective.md`](project-retrospective.md)
- 发布清单：[`docs/release/v0.2.0-checklist.md`](release/v0.2.0-checklist.md)
- 演示脚本：[`docs/demo-script.md`](demo-script.md)

缺陷复盘（postmortem）：

- [`docs/postmortems/diagnosis-api-auth-bypass.md`](postmortems/diagnosis-api-auth-bypass.md)
- [`docs/postmortems/worker-concurrency-limit.md`](postmortems/worker-concurrency-limit.md)
- [`docs/postmortems/target-remediation-race.md`](postmortems/target-remediation-race.md)

实验记录与演练报告：

- [`docs/experiments/m97-r5-deepseek-20260811.md`](experiments/m97-r5-deepseek-20260811.md)
- [`docs/experiments/m97-r6-deepseek-20260813.md`](experiments/m97-r6-deepseek-20260813.md)
- [`docs/experiments/001-oom-patch-memory.md`](experiments/001-oom-patch-memory.md)
- [`docs/experiments/003-verification-failure-rollback.md`](experiments/003-verification-failure-rollback.md)

演示截图：

- [`docs/assets/screenshots/`](assets/screenshots/)（10 张真实截图 + README 说明）

执行证据（公开脱敏版，外部可复核）：

- [公开证据包 `docs/releases/v0.2.0/evidence/`](releases/v0.2.0/evidence/)（发布门禁 / 真实 SMTP / 云端演示 / DeepSeek 评估）
- 私有未脱敏原件：`.omo/evidence/task-*.md`（gitignored，仅供维护者核对）
- 执行计划：`.omo/plans/aegisops-v020-release.md`（gitignored）

外部：

- GitHub 仓库：[`github.com/user27c/aegisops`](https://github.com/user27c/aegisops)
- 博客文章：本地 Hugo 博客仓库 `content/projects/aegisops/`（见 [`.omo/evidence/task-20-aegisops-v020-blog.md`](../.omo/evidence/task-20-aegisops-v020-blog.md)，含 10 张截图逐字节复制清单）

---

## 附：关键数字清单（全部带分母与来源）

| 数字                                                                | 分母                            | 来源                                                                            |
| ------------------------------------------------------------------- | ------------------------------- | ------------------------------------------------------------------------------- |
| 19 项实现任务全部完成（17 计划 + 2 追加）                           | 19 项任务                       | [`.omo/plans/aegisops-v020-release.md`](../.omo/plans/aegisops-v020-release.md) |
| 终审波 F1–F4 全部 APPROVE                                           | 4 项终审                        | 同上                                                                            |
| 覆盖率 controller 80.2% / executor 80.0% / policy 92.7%             | 各包可覆盖语句                  | [task-14](../.omo/evidence/task-14-aegisops-v020-release.md)                    |
| trivy 0 HIGH/CRITICAL                                               | 5 个镜像                        | 同上                                                                            |
| Kind E2E 全绿 901.6s                                                | 9 个顶层用例                    | 同上 + [发布清单](release/v0.2.0-checklist.md)                                  |
| r5 严格决策合同 28/36、危险动作 0/36、有效动作 9/10、安全降级 26/26 | 36 case / 10 有预期 / 26 无预期 | [m97-r5](experiments/m97-r5-deepseek-20260811.md)                               |
| r6 合同 28/36→26/36、危险动作 0/36→0/36                             | 36 case                         | [m97-r6](experiments/m97-r6-deepseek-20260813.md)                               |
| 真实 SMTP delivered=2、failed=0                                     | 2 封（FIRING+RESOLVED）         | [task-6](../.omo/evidence/task-6-aegisops-v020-release.md)                      |
| 云上 70 分钟、估算 ¥0.5–1.0、零计费残留                             | 1 次真实 create/smoke/destroy   | [cloud-demo-report.md](cloud-demo-report.md)                                    |
| 5 个 OCI 镜像 + 5 份 SBOM + 1 份 Helm tgz                           | 5 镜像 / 5 SBOM / 1 chart       | [发布清单](release/v0.2.0-checklist.md)                                         |
