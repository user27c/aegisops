# 实施状态事实表

> 维护规则:每项只允许 `yes / no / partial`,不得使用模糊表述。
> 证据列指向测试、脚本或保存的实验记录。
> 更新于:2026-08-02(M9.1 第一批完成后)。

| 能力 | Implemented | Unit | Integration | E2E | Real env | 证据 |
|---|---|---:|---:|---:|---:|---|
| Alertmanager webhook 接入与去重 | yes | yes | yes | no | yes | internal/alertmanager 测试;M1 集成记录 |
| Incident 状态机与终态 | yes | yes | no | no | yes | controller 80.4%;M2 集成记录 |
| 多源证据采集(K8s/Prom/Loki) | yes | yes | no | no | yes | evidence 82.2%;M7 集成记录 |
| 证据脱敏与限流 | yes | yes | no | no | yes | redactor/limiter 测试 |
| 诊断工作流(LangGraph) | yes | yes | yes | no | yes | Python 25 tests;M3 集成记录 |
| Diagnosis API 服务间鉴权 | **yes** | yes | no | no | no | app/security.py + 13 tests(fc881a3) |
| Worker 并发上限 | **yes** | yes | no | no | no | 容量驱动循环 + 5 tests(f8b616b) |
| 同目标 Incident 互斥锁 | **yes** | yes | no | no | no | internal/targetlock + 10 tests(9e37daa) |
| 策略守卫与 planDigest | yes | yes | no | no | yes | policy 92.7%;M4 集成记录 |
| 审批流(UID/digest/TTL/刷新) | yes | yes | no | no | yes | M4 集成记录 |
| 5 个类型化动作 | yes | yes | no | no | yes | executor 80.0%;M5 集成记录 |
| 执行前快照与回滚 | yes | yes | no | no | yes | M6a 集成记录 |
| 崩溃恢复(不重复执行) | yes | yes | no | no | yes | crash_recovery 测试;M5 实测 |
| 审计哈希链 | yes | yes | no | no | yes | audit 100%;M6 集成记录 |
| 邮件告警通知(MailHog 链路) | **yes** | no | yes | no | no | 集成测试通过(AegisOpsTest FIRING/RESOLVED/CRITICAL) |
| 邮件告警通知(真实 SMTP) | **no** | no | no | no | no | 发布门禁(需 --allow-real-email smoke) |
| PrometheusRule 自身告警 | **yes** | no | yes | no | no | AegisOpsTargetDown + promtool 4 场景测试 |
| Grafana 大盘 | yes | no | no | no | yes | deploy/observability/grafana |
| OTel 追踪导出 | partial | no | no | no | no | 中间件就绪,无采集器 |
| Web 控制台(列表/详情/审批) | yes | yes | no | no | partial | web 14 tests |
| 一键开发环境 dev-up/down | **no** | no | no | no | no | M9.5 待实现(当前明确失败) |
| 自动化 E2E 与 CI | **no** | no | no | no | no | M9.6 待实现(make test-e2e 明确失败) |
| 真实 DeepSeek 评估 | **no** | no | no | no | no | M9.7 待实现(当前仅 fake 基线) |
| 云上部署(ACK/k3s) | **no** | no | no | no | no | M9.8 待实现 |
| 仓库卫生(无跟踪缓存/敏感文件) | yes | no | no | no | yes | scripts/check-repo-hygiene.sh |

## 当前禁止表述(完成 M9 前)

- "E2E 全自动化通过" / "54 次真实故障演练" / "DeepSeek 根因命中率 100%"
- "完整告警通知系统" / "生产可用" / "并发锁已实现" / "M0–M8 全部自动验收通过"

## 建议临时表述

> 已实现 AegisOps 核心控制面并在 Kind 中手工完成故障自愈闭环;当前正在补齐自动化 E2E、真实 DeepSeek 对照评估、邮件告警和生产化安全约束。
