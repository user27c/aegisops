# 实施状态事实表

> 维护规则:每项只允许 `yes / no / partial`,不得使用模糊表述。
> 证据列指向测试、脚本或保存的实验记录。
> 更新于:2026-08-09（隔离 `kind-aegisops-e2e` 的 full suite、GitHub Actions CI 与托管 Kind full E2E 均已真实通过一次；DeepSeek v2 54-run、Grafana dashboard UI 截图和开发集群的 Operator→Diagnosis API→OTel Collector→Tempo 跨组件 trace 已真实完成。真实采集样本对照与生产化验收仍待完成）。

| 能力                               | Implemented | Unit | Integration | E2E | Real env | 证据                                                                                                                |
| ---------------------------------- | ----------- | ---: | ----------: | --: | -------: | ------------------------------------------------------------------------------------------------------------------- |
| Alertmanager webhook 接入与去重    | yes         |  yes |         yes | yes |      yes | internal/alertmanager 测试；Kind full E2E 的告警到邮件闭环                                                           |
| CRD schema/CEL 校验                | yes         |  yes |         yes | yes |      yes | envtest；Kind full E2E 安全场景拒绝未知动作                                                                           |
| Incident 状态机与终态              | yes         |  yes |          no | yes |      yes | controller 单测；Kind full E2E 覆盖 Resolved/RolledBack/Escalated 终态                                               |
| 多源证据采集(K8s/Prom/Loki)        | partial     |  yes |          no | partial |    partial | K8s/Prom 路径在 Kind full E2E 使用；开发集群 Loki 未安装，Loki 取证未复验                                            |
| 证据脱敏与限流                     | yes         |  yes |          no |  no |      yes | redactor/limiter 测试                                                                                               |
| 诊断工作流(LangGraph)              | yes         |  yes |         yes | yes |      yes | Python tests；Kind full E2E 以 fake provider 完成可复现诊断闭环                                                     |
| Diagnosis API 服务间鉴权           | yes         |  yes |         yes | yes |      yes | security 单测；真实 PostgreSQL API 集成；Kind full E2E 鉴权边界                                                     |
| Worker 并发与过期任务回收          | yes         |  yes |          no | partial |    partial | 容量驱动循环；stale reaper 离线单测；Kind full E2E 覆盖 worker 处理链路，未专门注入 stale job                        |
| 同目标 Incident 互斥锁             | yes         |  yes |          no | yes |      yes | internal/targetlock 单测；Kind full E2E 双 Incident 仅一个执行                                                      |
| 策略守卫与 planDigest              | yes         |  yes |          no | yes |      yes | policy 单测；Kind full E2E 审批、白名单与窗口冻结                                                                    |
| 审批流(UID/digest/TTL/刷新)        | yes         |  yes |          no | yes |      yes | Kind full E2E viewer 403、审批、PatchResourceLimit、验证与审计链                                                    |
| 5 个类型化动作                     | partial     |  yes |          no | partial |    partial | Kind full E2E 覆盖 Restart/PatchResourceLimit/Rollback；Scale/RestoreConfigMap 尚未真实 E2E                         |
| 执行前快照与回滚                   | yes         |  yes |         yes | yes |      yes | 真实 PostgreSQL snapshot round-trip；Kind full E2E 回滚到候选 revision                                               |
| 崩溃恢复(不重复执行)               | yes         |  yes |          no |  no |      yes | crash_recovery 测试;M5 实测                                                                                         |
| 审计哈希链                         | yes         |  yes |         yes | yes |      yes | audit 单测；真实 PostgreSQL 连续 hash chain；Kind full E2E 审批/执行/恢复审计链                                      |
| 邮件告警通知(MailHog 链路)         | yes         |   no |         yes | yes |      yes | Kind full E2E 验证 FIRING/RESOLVED 邮件且正文不泄漏测试凭据                                                         |
| 邮件告警通知(真实 SMTP)            | **no**      |   no |          no |  no |       no | 发布门禁(需 --allow-real-email smoke)                                                                               |
| PrometheusRule 自身告警            | yes         |   no |         yes | yes |      yes | promtool；Kind full E2E 真实 Alertmanager→MailHog                                                                   |
| Grafana 大盘                       | yes         |   no |          no |  no |      yes | `aegisops-overview` 6 panels 已导入；已认证 Playwright 截图验证 5 个 targets 健康和真实状态转移数据；无事件 panel 如实显示真实零值 `0` |
| OTel 追踪导出                      | yes         |  yes |         yes | partial |      yes | Go/Python tracing 回归；开发集群同一 trace 含 Operator `incident.reconcile`/`evidence.collect` 与 Diagnosis API `POST /v1/analyses`；完整 E2E profile 未启用 Collector |
| Web 控制台(列表/详情/审批)         | yes         |  yes |          no |  no |  partial | web 14+22 tests(vitest);Playwright e2e(Dashboard→Detail→Approve→Phase)                                              |
| Incident API 详情增强(时间线/证据) | **yes**     |  yes |          no |  no |       no | GET /timeline /evidence + detailsUnavailable 降级;details_test 6 tests                                              |
| 分页过滤(opaque cursor)            | **yes**     |  yes |          no |  no |       no | ListIncidents 重写 + INVALID_CURSOR/FILTER_CHANGED + cursor 测试                                                    |
| API 契约对齐                       | **yes**     |  yes |          no |  no |       no | contract_test.go(chi Routes 双向核对)+ test_contract.py(OpenAPI 9 端点);已删 GET /v1/runbooks、GET /v1/audit-events |
| 一键开发环境 dev-up/down           | **yes**     |   no |          no |  no |      yes | dev-up full 幂等 + make smoke 通过 + dev-down 无残留(kind-aegisops-dev 实测)                                        |
| 自动化 E2E 与 CI                   | yes         |   no |          no | yes |      yes | `scripts/run-e2e.sh` full profile 在隔离 Kind 真实通过（498s）；GitHub CI [31300651720](https://github.com/user27c/aegisops/actions/runs/31300651720) 与 Kind E2E [31300651719](https://github.com/user27c/aegisops/actions/runs/31300651719) 均通过 |
| 真实 DeepSeek 评估                 | partial     |  yes |          no |  no |      yes | v1 原件保留；prompt v2/严格评分合同真实 54-run（taxonomy 27/54、方案 0/36、安全降级 18/18、严格合同 0/54）；A/B/C/D 待完成 |
| 云上部署(ACK/k3s)                  | **no**      |   no |          no |  no |       no | M9.8 待实现                                                                                                         |
| 仓库卫生(无跟踪缓存/敏感文件)      | yes         |   no |          no |  no |      yes | scripts/check-repo-hygiene.sh                                                                                       |

## 当前禁止表述(完成 M9 前)

- "54 次真实故障演练" / "DeepSeek 根因命中率 100%" / "生产可用"
- "完整告警通知系统" / "生产可用" / "并发锁已实现" / "M0–M8 全部自动验收通过"

## 建议临时表述

> 已实现 AegisOps 核心控制面；隔离 Kind full E2E 与 GitHub Actions CI/托管 Kind E2E 均已通过，Grafana dashboard UI 和 Operator→Diagnosis API→Collector→Tempo 均有真实证据。真实 DeepSeek v2 合成评估已完成但质量偏低；当前仍需真实采集样本的 A/B/C/D 对照与安全的 k3s/生产化验收。
