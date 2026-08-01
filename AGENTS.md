# aegisops - AI Agent Guide

AegisOps：面向 Kubernetes 的证据驱动智能诊断与受控自愈 Operator。
完整规范以 `docs/design/aegisops-implementation-blueprint.md`（蓝图）为准，本文件只记录工程速查。

## 项目结构

```
api/v1alpha1/                 三个 CRD 类型（ops.aegis.io/v1alpha1）
  aiopsincident_types.go      AIOpsIncident（状态机唯一事实源）
  remediationpolicy_types.go  RemediationPolicy（动作策略）
  remediationapproval_types.go RemediationApproval（planDigest 绑定审批）
cmd/                          三个入口：operator / alert-gateway / incident-api
internal/
  controller/                 状态机编排（不直接碰 PromQL/Deployment）
  evidence/                   只读证据采集（K8s/Prom/Loki/Tempo）
  policy/                     确定性纯逻辑策略校验
  executor/                   唯一允许修改工作负载的包
  verifier/                   单次健康检查
  audit/                      审计事件（Critical/BestEffort）
  httpapi/                    Web 后端（禁止导入 executor）
  analysisclient/             Go 访问诊断服务的唯一入口
  config/ observability/      配置与可观测性
services/diagnosis/           FastAPI + LangGraph + DeepSeek + RAG（无 K8s 写权限）
web/                          React 事故控制台
fault-lab/                    受控故障演练应用
runbooks/                     6 类故障 Runbook（frontmatter 有元数据）
eval/                         数据集与 A/B/C 对照实验
deploy/helm/aegisops/         Helm Chart（最小权限 RBAC + NetworkPolicy）
scripts/                      17 个工程脚本
docs/                         架构、安全模型、状态机、评估、演示文档 + design/ 原始设计
```

## 硬性约束（违反需先新增 ADR）

1. DeepSeek 与诊断服务没有 Kubernetes 写权限。
2. LLM 不得生成或执行任意 Shell/kubectl/代码/通用 Patch。
3. Reconcile 不得同步等待长时间 LLM 调用。
4. 所有写操作必须映射到固定 Typed Action（五个）。
5. 中风险动作必须审批，绑定不可复用的 planDigest。
6. 每个动作必须实现 Preflight/Snapshot/Apply/Verify/Rollback。
7. 无匹配 Policy、审计不可用、证据不足、验证条件不明确时全部 fail closed。

## 常用命令

```bash
make generate manifests     # 生成 deepcopy/CRD/RBAC
make build                  # 构建三个 Go 二进制
make verify                 # fmt + lint + 单元测试 + helm lint
make test-go test-python test-web test-envtest
scripts/build-images.sh --registry ghcr.io/user27c --tag dev
```

生成文件（禁止手改）：`api/v1alpha1/zz_generated.deepcopy.go`、`config/crd/bases/*`、`config/rbac/role.yaml`、`PROJECT`。

## 包边界速查

- `api/` 不导入 `internal/`。
- `controller/` 不直接拼 PromQL、不直接 Patch Deployment。
- `executor/` 是唯一修改工作负载的包。
- `verifier/` 只做一次检查，不 sleep/poll。
- `httpapi/` 不能导入 `executor/`。
- `policy/` 必须是确定性纯逻辑。
