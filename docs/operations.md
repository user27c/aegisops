# 运维手册

## 安装（空 kind/k3s）

```bash
# 1. 工具链
scripts/bootstrap-tools.sh
# 2. CRD
kubectl apply -f config/crd/bases/
# 3. 命名空间与 token secrets
kubectl create ns fault-lab aegisops-system
kubectl label ns fault-lab aegisops.io/managed=true
kubectl -n aegisops-system create secret generic aegisops-gateway-token \
  --from-literal=webhook-token=<随机串>
kubectl -n aegisops-system create secret generic aegisops-console-auth \
  --from-literal=tokens='console-token-xyz:viewer,approver'
# 4. Helm（镜像先构建/load 到集群）
helm install aegisops deploy/helm/aegisops -n aegisops-system \
  --set global.imageRegistry=<registry> --set diagnosis.llmProvider=fake
# 5. 策略与演练应用
kubectl apply -f config/samples/ops_v1alpha1_remediationpolicy.yaml
kubectl apply -f deploy/kind/faultlab.yaml   # 或独立部署 fault-lab
```

## Alertmanager 接入

```yaml
route: {receiver: aegisops}
receivers:
  - name: aegisops
    webhook_configs:
      - url: http://aegisops-gateway/webhooks/alertmanager
        send_resolved: true
        http_config:
          headers: {Authorization: "Bearer <webhook-token>"}
```

## 升级与 DB migration

- 服务升级：滚动更新（operator 建议 2 副本 + leader election）。
- PG migration：`cd services/diagnosis && uv run alembic upgrade head`（迁移前备份）。
- CRD 升级：`kubectl apply -f config/crd/bases/`（v1alpha1 冻结后仅 additive 变更）。

## 备份

- PG：`pg_dump`（analysis_jobs/evidence_snapshots/audit_events/runbook_chunks）。
- 无状态组件无需备份。

## Key rotation

- webhook token：更新 Secret → 重启 gateway（文件挂载）。
- console tokens：更新 `aegisops-console-auth`。
- DeepSeek Key：更新 `deepseek-api` Secret。

## LLM 故障

- diagnosis 不可用：Incident 停在 Diagnosing（ErrTransient 退避）；恢复后自动继续。
- 长期不可用：可设置 `diagnosis.api.enabled=false` 观察；不会误执行任何动作。

## Stuck Incident 处理

- `kubectl -n fault-lab get aiopsincidents` 查看 phase/conditions。
- 若因诊断失败 stuck：检查 diagnosis API/worker 日志、PG 连接。
- 若需人工终结：`kubectl patch aiopsincident <name> --subresource=status -p '{"status":{"phase":"Escalated"}}'`（终态写入）。
- 删除演练数据：`kubectl delete aiopsincidents --all -n fault-lab`。

## 卸载

```bash
helm uninstall aegisops -n aegisops-system
kubectl delete crd aiopsincidents.ops.aegis.io remediationpolicies.ops.aegis.io remediationapprovals.ops.aegis.io
# PG 数据（审计/快照）按需保留或删除
```
