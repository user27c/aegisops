# Demo Script（全新环境 40 分钟）

前置：kind 集群 + Helm 部署完成（见 operations.md）。

## 0-5min：环境确认

```bash
kubectl get crd | grep ops.aegis.io
curl -s localhost:18080/healthz && curl -s localhost:18081/healthz
kubectl -n fault-lab get pods   # faultlab 就绪
```

## 5-20min：注入故障 → 自愈闭环（自动化脚本）

```bash
# 1. 注入配置故障（faultlab /checkout 返回 500）
curl -X POST 'localhost:18092/inject?type=config&duration=300'
curl -i localhost:18092/checkout   # 期望 500
# 2. 触发告警（模拟 Alertmanager firing）
curl -X POST -H "Authorization: Bearer <token>" \
  --data @tests/fixtures/alerts/checkout-500.json localhost:18080/webhooks/alertmanager
# 3. 观察自愈
kubectl -n fault-lab get aiopsincidents -w
# 期望:Detected→CollectingEvidence→Diagnosing→PolicyChecking→
#      AwaitingApproval(审批)→Executing→Verifying→Resolved
# 4. 批准（若策略要求审批）
curl -X POST -H "Authorization: Bearer console-token-xyz" \
  -d '{"decision":"Approve","reason":"demo"}' \
  localhost:18081/api/v1/incidents/fault-lab/<incident>/approval
# 5. 验证恢复
curl localhost:18092/checkout   # 200
```

## 20-30min：审计与可观测

```bash
# 审计链
docker exec aegisops-pg psql -U aegisops -d aegisops \
  -c "SELECT sequence,event_type,left(previous_hash,8),left(event_hash,8) FROM audit_events ORDER BY sequence"
# Grafana（localhost:13000）:AegisOps 总览
# Prometheus（localhost:19090）:aegisops_* 指标
```

## 30-40min：回滚演示（可选）

构造 ScaleDeployment 提案 → 执行后手动置 RollingBack → 观察 replicas 恢复执行前值（PG 快照）。

## 失败备用路线

- 诊断无方案（reviewer fail）：检查 worker 进程（清理幽灵 worker）、PG 连接、fake markers。
- Gateway 500：检查 webhook token、body 大小。
- 端口冲突：`fuser -k <port>/tcp` 后重启对应进程。
