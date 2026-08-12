# 运维手册

## 安装（空 kind/k3s）

一键方式(M9.5 起可用,推荐):

```bash
# 0. 工具链与本地配置(随机 token,0600,不打印)
scripts/bootstrap-tools.sh
scripts/init-local-config.sh

# 1. 一键启动(构建镜像 → kind load → 安装 observability(可选) → Helm → fault-lab → port-forward)
scripts/dev-up.sh --context kind-aegisops-dev --profile full --tag dev --yes

# 2. 冒烟检查
make smoke CONTEXT=kind-aegisops-dev

# 3. 卸载(release + fault-lab + port-forward;数据保留)
scripts/dev-down.sh --context kind-aegisops-dev --yes
# 可选: --purge-data 删 PVC;--delete-kind-cluster 删 Kind 集群
```

参数速查:

| 参数                      | 说明                                                         |
| ------------------------- | ------------------------------------------------------------ |
| `--context`               | 必填;仅允许 `kind-*`/`k3d-*`,其他需 `--allow-nonlocal`       |
| `--profile minimal\|full` | full 隐含 observability(邮件/Prometheus/Loki/Tempo)+ mailhog |
| `--registry`              | 默认 `aegisops.local`(本地);真实 registry 用于 push          |
| `--tag`                   | 必填,禁止 `latest`                                           |
| `--skip-build`            | 跳过镜像构建(镜像已 load 时)                                 |
| `--values`                | 附加 Helm values 文件(可多次)                                |
| `--yes`                   | 跳过交互确认                                                 |

手动方式(拆解):

```bash
# 1. 工具链
scripts/bootstrap-tools.sh
# 2. CRD
kubectl apply -f config/crd/bases/
# 3. 命名空间与 token secrets(推荐用 init-local-config.sh 生成后由 dev-up 创建)
kubectl create ns fault-lab aegisops-system
kubectl label ns fault-lab aegisops.io/managed=true
kubectl -n aegisops-system create secret generic aegisops-gateway-token \
  --from-literal=webhook-token=<随机串>
kubectl -n aegisops-system create secret generic aegisops-console-auth \
  --from-literal=tokens='console-token-xyz:viewer,approver'
kubectl -n aegisops-system create secret generic aegisops-diagnosis-token \
  --from-literal=token=<随机串>
# 4. 镜像(registry 默认 aegisops.local;kind load 到集群)
scripts/build-images.sh --tag dev
kind load docker-image aegisops.local/{aegisops-operator,aegisops-alert-gateway,aegisops-incident-api,aegisops-diagnosis,fault-lab}:dev \
  --name aegisops-dev
# 5. Helm(镜像先构建/load 到集群)
helm install aegisops deploy/helm/aegisops -n aegisops-system \
  --set global.imageRegistry=aegisops.local --set global.imageTag=dev \
  --set diagnosis.llmProvider=fake -f .local/values.yaml
# 6. 策略与演练应用
kubectl apply -f config/samples/ops_v1alpha1_remediationpolicy.yaml
kubectl apply -f deploy/kind/faultlab.yaml
# 7. port-forward(PID 存 .tmp/pf.pids,dev-down/pf-up.sh down 停止)
scripts/pf-up.sh up
```

## Alertmanager 接入

```yaml
route: { receiver: aegisops }
receivers:
  - name: aegisops
    webhook_configs:
      - url: http://aegisops-gateway/webhooks/alertmanager
        send_resolved: true
        http_config:
          headers: { Authorization: "Bearer <webhook-token>" }
```

## 升级与 DB migration

- 服务升级：滚动更新（operator 建议 2 副本 + leader election）。
- PG migration：Helm 使用按 release revision 命名的普通 Job 运行 `alembic upgrade head`，以便 `helm --wait` 等待其完成且镜像升级不会尝试修改不可变 Job template；手动迁移前仍应备份并执行 `cd services/diagnosis && uv run alembic upgrade head`。
- CRD 升级：`kubectl apply -f config/crd/bases/`（v1alpha1 冻结后仅 additive 变更）。

## 备份

- PG：`pg_dump`（analysis_jobs/evidence_snapshots/audit_events/runbook_chunks）。
- 无状态组件无需备份。

## Key rotation

- webhook token：更新 Secret → 重启 gateway（文件挂载）。
- console tokens：更新 `aegisops-console-auth`。
- DeepSeek Key：更新 `deepseek-api` Secret。

## 阿里云单节点 k3s 演示（M9.8，尚未实际执行）

Terraform 资产位于 `infra/terraform/aliyun/`。它只创建单节点、按量付费的演示基础设施；AccessKey、SSH 私钥、DeepSeek/SMTP/Console/Webhook token 与真实域名都不得写入 Git 或 Terraform state。

```bash
# 先在本地受控环境配置阿里云凭据与 SSH 公钥路径，再只读审查计划。
cd infra/terraform/aliyun
terraform init
terraform fmt -check
terraform validate
terraform plan
```

Terraform apply 后，使用 SSH tunnel 或已受限的管理网络配置 kubeconfig。在目标集群中通过本地 Secret 文件创建必需的 AegisOps Secret；不要在命令历史中写入值。之后运行：

```bash
scripts/cloud-deploy.sh \
  --context <k3s-context> --registry <immutable-registry> --tag <immutable-tag> \
  --values <local-secret-free-values.yaml> \
  --confirm 'deploy aliyun-demo'

scripts/cloud-smoke.sh \
  --context <k3s-context> --prom-url <https-or-local-tunnel-prometheus> \
  --confirm 'smoke aliyun-demo'
```

`cloud-smoke.sh` 不调用真实模型、不发送邮件、不执行修复。真实 DeepSeek、邮件和 Auto Restart 闭环需要明确的费用与通知授权，并将脱敏证据写入 [cloud-demo-report.md](cloud-demo-report.md)。销毁前仅运行检查清单；它永远不会自动 destroy：

```bash
scripts/cloud-destroy-checklist.sh --confirm 'review destroy aliyun-demo'
```

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
