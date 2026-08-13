# AegisOps

**面向 Kubernetes 的证据驱动智能诊断与受控自愈 Operator**

`Alertmanager 告警 → 指纹去重 → 多源证据快照 → RAG 检索 → DeepSeek 诊断 → 二次审查 → 确定性策略校验 → 人工审批或低风险自动放行 → Operator 类型化执行 → 健康验证 → 失败回滚 → 事故报告`

AegisOps 不是"让大模型直接执行 kubectl"的聊天机器人，而是一套可以解释、审批、回滚和审计的可靠性控制面。

## 核心设计

- **闭环完整**：从告警到恢复验证的事故响应闭环。
- **AI 与执行权分离**：DeepSeek 只能返回满足 JSON Schema 的候选方案，没有 kubeconfig；集群写操作只能经过 Operator 的固定类型化动作。
- **证据驱动**：结论必须引用 PromQL、LogQL、Kubernetes Event 与 RAG Runbook，缺少证据时降级为"需要人工排查"。
- **安全可证明**：风险分级、动作白名单、参数边界、方案摘要哈希、最小权限 RBAC、并发锁、幂等执行、超时回滚与完整审计。
- **效果可测量**：可重复的故障注入 + 带真值的数据集，度量根因命中率、检索 Hit@K、危险方案拦截率、修复成功率与 MTTR。

## 仓库结构

```text
api/v1alpha1/        三个 CRD：AIOpsIncident / RemediationPolicy / RemediationApproval
cmd/                  operator / alert-gateway / incident-api 三个入口
internal/             controller / evidence / policy / executor / verifier / audit / httpapi ...
services/diagnosis/   FastAPI + LangGraph + DeepSeek + RAG（无 Kubernetes 写权限）
web/                  React 事故控制台
fault-lab/            受控故障演练应用
runbooks/             6 类故障的 Runbook
eval/                 数据集、对照实验与指标报告
deploy/helm/aegisops/ Helm Chart（最小权限 RBAC + NetworkPolicy）
docs/                 架构、安全模型、状态机、评估与演示文档
```

## 快速开始

前置条件：Go 1.25+、uv、pnpm、kubectl、helm、kind/k3s。

```bash
# 0. 检查工具链
scripts/bootstrap-tools.sh

# 1. 本地构建与单元测试
make verify

# 2. 启动 Kind 开发环境（M1 里程碑后可用）
scripts/dev-up.sh --context kind-aegisops
```

详细安装与演示步骤见 [docs/operations.md](docs/operations.md) 与 [docs/demo-script.md](docs/demo-script.md)；评估方法与最新结果见 [docs/evaluation.md](docs/evaluation.md)；设计决策见 [docs/adr](docs/adr/)。

> **状态声明（2026-08-13，v0.2.0）**：AegisOps 核心控制面已实现，发布门禁（T14）全绿——本地 envtest、诊断/PostgreSQL/MailHog 集成、真实 SMTP smoke、trivy 镜像扫描与隔离 `kind-aegisops-e2e` 的 full 自动化 E2E（9 个顶层用例，901.6s）均已通过；GitHub Actions CI 与托管 Kind full E2E 已各真实通过一次；开发集群已验证 Grafana dashboard 与 Operator→Diagnosis API→OTel Collector→Tempo 跨组件 trace。真实 DeepSeek A/B/C/D 已在语义有效的 36 case 数据集上执行：r5 严格决策合同 28/36、危险动作 0/36；r6 有界迭代 28/36→26/36 回退后已还原 v4 基线，动作有效性与网络失败率仍不足以放行云端自动修复。阿里云单节点 k3s 云上 create/smoke/destroy 已真实走通（gate-down 受控演示：fake 诊断，未宣称云端自动修复）。**请勿将本项目描述为“生产可用”。**

## 里程碑状态

| 里程碑 | 内容                                   | 状态                                                  |
| ------ | -------------------------------------- | ----------------------------------------------------- |
| M0     | 仓库与工具链                           | ✅                                                    |
| M1     | CRD + Gateway + 只读 Console           | ✅                                                    |
| M2     | Controller 状态机 + Evidence           | ✅                                                    |
| M3     | Diagnosis API、Worker、RAG             | ✅                                                    |
| M4     | Policy + Approval                      | ✅                                                    |
| M5     | 5 个 Typed Actions                     | ✅                                                    |
| M6     | Verification、Audit、Crash Recovery    | ✅                                                    |
| M7     | Fault Lab + Observability              | ✅                                                    |
| M8     | E2E、Eval、文档收尾                    | ✅                                                    |
| M9.x   | v0.2.0 收尾(鉴权/锁/邮件/E2E/真实评估) | ✅ 完成（发布门禁全绿；真实评估与云端自动修复未达标） |

## 端到端验证与评估（2026-08，kind + Helm 全组件）

发布门禁 `scripts/release-check.sh --with-integration-e2e` 全绿；隔离 Kind full E2E 9 个顶层用例全部通过（总时长 901.6s）——告警到邮件、审批补丁、ScaleCPU fail-closed、Auto Restart、真实 Loki 证据、RestoreConfigMap、回滚、安全边界（5 个子场景）。

集群内全链路：注入 OOM → 容器 OOMKilled(exit 137)→ 告警 → Incident → 真实 K8s 证据 → 诊断(OOMKilled，容器名从证据提取)→ 策略(ApprovalRequired) → 人工批准 → PatchResourceLimit 真实执行(300Mi→384Mi) → 连续 2 次验证 → Resolved；审计链 ApprovalGranted→ExecutionStarted→ExecutionCompleted→IncidentResolved。

Eval（fake 基线，确定性测试替身，**不代表模型质量**）：54 runs，根因命中 100%、越权执行 0/54。真实 DeepSeek（r5，36 case）：严格决策合同 28/36、危险动作 0/36；r6 有界迭代 28/36→26/36 回退后已还原 v4 基线。详见 [docs/evaluation.md](docs/evaluation.md)。

云上部署（阿里云单节点 k3s）：create/smoke/destroy 真实走通（约 70 分钟，估算 ¥0.5–1.0），gate-down 受控演示（fake 诊断，未宣称云端自动修复）。详见 [docs/cloud-demo-report.md](docs/cloud-demo-report.md)。

发布清单（镜像 digest、Chart 校验和、SBOM、升级说明、已知限制）见 [docs/release/v0.2.0-checklist.md](docs/release/v0.2.0-checklist.md)。

## 安全边界

- 模型无 Kubernetes 凭据；Operator 无 DeepSeek Key。
- 禁止模型生成/执行任意 Shell、kubectl 或通用 Patch。
- 中风险动作必须审批，审批绑定 `planDigest`（内含目标 resourceVersion 与 Policy generation），方案变化后旧审批自动失效。
- 全部写操作映射到 5 个 Typed Action，每个都有 Preflight / Snapshot / Apply / Verify / Rollback。

详见 [docs/security-model.md](docs/security-model.md) 与 [SECURITY.md](SECURITY.md)。

## 文档索引

- [概要设计](docs/design/aegisops-project-design.md)
- [全量实现蓝图](docs/design/aegisops-implementation-blueprint.md)
- [总体架构](docs/design/aegisops-architecture.mmd)
- [实施状态事实表](docs/implementation-status.md)
- [评估方法](docs/evaluation.md)
- [云上部署报告](docs/cloud-demo-report.md)
- [v0.2.0 发布清单](docs/release/v0.2.0-checklist.md)

## 许可

Apache License 2.0。本项目是面向生产约束的工程实验平台，**不宣称生产可用**。
