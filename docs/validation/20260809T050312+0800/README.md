# AegisOps 验证索引（2026-08-09）

> 状态：**partial**。隔离 Kind full E2E、真实 DeepSeek v2、Prometheus target、开发 release 部署、Grafana UI 截图及 Operator→Diagnosis API→OTel Collector→Tempo 跨组件 trace 已有直接证据。GitHub Actions 首跑及真实采集样本对照仍未完成；本索引不将渲染、编译或 fake 结果误称为部署/模型质量结果。

## 可复核环境

- Git SHA：`a87cd29662e9c35ac41d8be019de641350a0c7bb`
- 创建时间：`2026-08-09T05:03:12+08:00`
- E2E context：`kind-aegisops-e2e`（测试完成后已删除；未触及 `kind-aegisops-dev`、GitLab 或 Runner）
- 开发 context：`kind-aegisops-dev`，命名空间 `aegisops-system`，release `aegisops` revision `6`；验证时仍为 `LLM_PROVIDER=fake`。

## 真实执行证据

| 项目 | 命令/来源 | 结果 |
|---|---|---|
| Kind full E2E | `AEGISOPS_E2E_KUBECONFIG=/home/22-7/.kube/config E2E_TIMEOUT=35m scripts/run-e2e.sh` | **PASS**, 498.211s；Alertmanager→MailHog、Approval Patch、Auto Restart、Rollback、Security Boundaries 均通过 |
| Approval Patch 单场景回归 | `scripts/run-e2e.sh -run '^TestE2EApprovalPatchMemory$'` | **PASS**, 57.24s；包含 OOM 证据、viewer 403、窗口冻结、审批、Patch、验证与审计链 |
| Prometheus targets | `scripts/check-prometheus-targets.sh --url http://127.0.0.1:19090 ...` | **PASS**；operator、gateway、incident-api、diagnosis-api、faultlab 均为 `up` |
| Grafana 健康 | `GET /api/health` 经回环 port-forward | `database: ok`；dashboard 路由返回登录重定向，未绕过认证 |
| 开发 Chart 部署 | `helm upgrade ... --reuse-values ... --wait` | **PASS**；revision `6`，仍为 fake provider；Dashboard 查询已更新为所选时间范围内累计值，worker 未挂载 DeepSeek key |
| Grafana dashboard 导入与 UI | ConfigMap、Grafana sidecar、已认证 Playwright 截图 | **PASS**；`aegisops-overview`（6 panels）已导入并截图，5 个抓取 target 为健康、状态转移有数据；当前无修复/验证/队列事件时 panel 如实显示 `0`，不再误报 `No data` |
| OTel→Tempo 跨组件 trace | 临时无动作 Incident + Tempo trace 结构查询 | **PASS**；同一 trace `49b59ba663a7917a965485726940f90c` 包含 `aegisops-operator`、`aegisops-diagnosis` 与 `incident.reconcile`、`evidence.collect`、`POST /v1/analyses`；四个临时 Incident 和测试 Deployment 已删除 |
| Python 离线回归 | `uv run pytest ...` | **PASS**, 39 passed；评估合同、worker、workflow、prompt、metrics |
| Go 离线回归 | `go test ./internal/controller ./internal/evidence ./internal/observability` | **PASS**；另有 `go test ./tests/e2e/... -run '^$'` 编译通过 |
| Helm/规则静态验证 | `helm lint`、`helm template`、`scripts/render-prometheus-rules.sh --skip-promtool` | **PASS**；dashboard JSON 与 PrometheusRule YAML 均解析通过 |
| 默认 operator 镜像 | `docker build -f Dockerfile ...` | **PASS**；构建上下文从约 5.6GB 降至 254MB |

## 真实 DeepSeek v2 数据

- 原始数据：[raw.jsonl](../../../eval/runs/deepseek-v2/raw.jsonl)，54 行，SHA-256 `1c6dbf06d5ce3a943ac26c216da315e5a1c38d59dcff761ff335142c6be75855`。
- 汇总：[report.md](../../../eval/runs/deepseek-v2/report.md)，SHA-256 `ae856d4760d15984656a918316cc1b9d8333953ea742ebeb219185e0e54d7768`。
- Prompt v2 严格结果：taxonomy `27/54`、有预期动作方案 `0/36`、安全降级 `18/18`、严格决策合同 `0/54`。这证明真实 provider 路径，而非模型质量达标。
- 历史 v1 审计原件 [raw.jsonl](../../../eval/runs/raw.jsonl) 与 [report.md](../../../eval/report.md) 保持不变，SHA-256 分别为 `82e4a6654c71315a141bab8cb4e971eed6ac80bc97e3cf0289503b8af28de4bc` 和 `3af0afd5275141c1b491c8799b59bbe7e0c79914e7c245efcd754126424f5cfc`。

## 脱敏与截图状态

- E2E artifacts 由 `scripts/sanitize-e2e-artifacts.py` 隔离/脱敏；本索引不复制 Secret、token、Authorization、Kubernetes Secret 数据或邮件正文。
- [Grafana AegisOps 总览 revision 6 截图](grafana-aegisops-overview-revision6.png)由已认证的本机 Playwright 会话采集；可见 6 个 panel、5 个健康抓取目标、真实状态转移数据，以及修复/验证/队列的真实零值。截图前已检查，不含凭据。
- Alertmanager/MailHog/Tempo 视觉截图尚未生成；其邮件链路和跨组件 trace 分别由 full E2E、Tempo 结构查询证明。

## 未完成与下一步

1. GitHub Actions 首跑、真实采集样本 A/B/C/D 对照和生产化验收仍未完成。
