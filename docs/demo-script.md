# 演示视频分镜与脚本（5–8 分钟）

> 目标：录一段 5–8 分钟、命令可直接复制的演示视频，完整走一遍 AegisOps 的告警到自愈闭环。
> 本脚本的每一条命令都对应仓库里真实存在的脚本、Makefile target、端口或 API，复制即可运行。
> 环境：`kind-aegisops-dev`（开发集群）。邮件通知链单独用 docker-compose，见第 3、6 段说明。
> 全程不出现任何 Secret、Token、密码或真实邮箱。

## 结构与时长（对齐 NEXT-STEPS-IMPLEMENTATION-PLAN.md 14.4）

| 段  | 时长 | 内容                              |
| --- | ---- | --------------------------------- |
| 1   | 30s  | 问题与架构                        |
| 2   | 45s  | CRD、Policy、安全边界             |
| 3   | 60s  | 注入故障 + firing 邮件            |
| 4   | 90s  | Evidence、DeepSeek、RAG、Reviewer |
| 5   | 60s  | 审批 + Typed Action               |
| 6   | 60s  | Resolved / rollback + 恢复邮件    |
| 7   | 60s  | Grafana / Loki / Tempo / Audit    |
| 8   | 30s  | 真实评估结果与限制                |

## 端口速查（全部来自 scripts/pf-up.sh）

| 服务                      | 本机地址               | 集群内来源                             |
| ------------------------- | ---------------------- | -------------------------------------- |
| alert-gateway             | http://127.0.0.1:18080 | svc/aegisops-gateway                   |
| incident-api + 控制台 SPA | http://127.0.0.1:18081 | svc/aegisops-incident-api              |
| diagnosis API             | http://127.0.0.1:8000  | svc/aegisops-diagnosis-api             |
| fault-lab                 | http://127.0.0.1:18092 | svc/faultlab                           |
| Prometheus                | http://127.0.0.1:19090 | svc/kube-prometheus-stack-prometheus   |
| Grafana                   | http://127.0.0.1:13000 | svc/kube-prometheus-stack-grafana      |
| Alertmanager              | http://127.0.0.1:19093 | svc/kube-prometheus-stack-alertmanager |
| Tempo                     | http://127.0.0.1:13200 | svc/tempo                              |
| MailHog UI                | http://127.0.0.1:18025 | svc/mailhog（集群）或 docker-compose   |

## 录制前准备（不进正片）

```bash
# 0. 工具链 + 本地随机 token（token 落盘 .local/secrets，0600，不打印）
scripts/bootstrap-tools.sh
scripts/init-local-config.sh

# 1. 一键启动开发环境（full 隐含 Prometheus/Loki/Tempo/Grafana + MailHog）
scripts/dev-up.sh --context kind-aegisops-dev --profile full --tag dev --yes

# 2. 补齐 dev-up 未创建的两个 ConfigMap（faultlab 挂载 checkout-config 需要，
#    否则 faultlab Pod 停在 CreateContainerConfigError）
kubectl apply -f deploy/kind/faultlab-configmaps.yaml
kubectl -n fault-lab rollout restart deployment/faultlab
kubectl -n fault-lab rollout status deployment/faultlab --timeout=120s

# 3. 冒烟检查（Pod 就绪 + 5 个 Prometheus targets + 服务健康）
make smoke CONTEXT=kind-aegisops-dev
```

录制前再跑一次 `make smoke CONTEXT=kind-aegisops-dev` 确认绿灯，避免边录边排障。

---

## 第 1 段：问题与架构（30 秒）

**口播稿（约 90 字，30 秒）：**

> 让大模型直接执行 kubectl 会带来幻觉、越权和无法回滚三个问题。AegisOps 把「诊断」和「执行」彻底分开：DeepSeek 只能返回满足 JSON Schema 的候选方案，没有任何集群凭据；真正的写操作只经过 Operator 的五个固定类型化动作，每个动作都带 Preflight、快照、执行、验证、回滚五步，并且全程可审批、可审计。

**画面与命令（边讲边敲）：**

```bash
# 显示版本（视频必须露出 Git SHA，对应计划 14.4 要求）
git rev-parse --short HEAD
git describe --tags --always

# 三个 CRD 与两个命名空间就绪
kubectl get crd | grep ops.aegis.io
kubectl -n aegisops-system get pods
kubectl -n fault-lab get pods
```

**失败备用：** 若 `git describe` 无 tag 输出，直接口播「当前 commit SHA 是 <上一条输出>」，不影响。若 Pod 未就绪，回看录制前准备第 2 步是否执行。

---

## 第 2 段：CRD、Policy、安全边界（45 秒）

**口播稿：**

> 整个事故的生命周期落在 AIOpsIncident 这一个 CR 上，它是状态机的唯一事实源。RemediationPolicy 决定每个动作是自动放行还是要人工审批，RemediationApproval 把审批绑定到方案摘要。看这个默认策略：RestartWorkload 是 Auto，Scale、Patch、Rollback、RestoreConfigMap 全部要审批，且限制参数上界。

**画面与命令：**

```bash
# 三个 CRD 类型
kubectl get crd aiopsincidents.ops.aegis.io remediationpolicies.ops.aegis.io remediationapprovals.ops.aegis.io

# 默认策略：RestartWorkload=Auto，其余四个动作 ApprovalRequired
kubectl -n fault-lab get remediationpolicy fault-lab-default -o yaml
```

口播补一句安全边界：模型无 kubeconfig，Operator 无 DeepSeek Key；中风险动作必须审批，审批绑定 planDigest（含目标 resourceVersion 与 Policy generation），方案一变旧审批自动失效。

**失败备用：** 若策略 CR 不存在，先 `kubectl apply -f config/samples/ops_v1alpha1_remediationpolicy.yaml` 再展示。

---

## 第 3 段：注入故障 + firing 邮件（60 秒）

**口播稿：**

> fault-lab 是一个受控故障演练应用。我先看它的正常状态，然后注入 OOM 故障，它会在 HTTP 返回前被 cgroup 直接杀死，留下真实的 exit 137。同时，告警通知链会往 MailHog 发一封 firing 邮件。

**画面与命令（故障注入，走集群）：**

```bash
# 基线：正常返回 ok
curl -s http://127.0.0.1:18092/checkout

# 注入 OOM（curl 可能收到 EOF，这是预期的，进程被 cgroup 杀死）
curl -sS -X POST 'http://127.0.0.1:18092/inject?type=oom'

# 确认容器确实 OOMKilled（exit 137）
kubectl -n fault-lab get pod -l app.kubernetes.io/instance=faultlab \
  -o jsonpath='{.items[0].status.containerStatuses[0].lastState.terminated}{"\n"}'
```

**画面与命令（firing 邮件，独立 docker-compose 链）：**

```bash
# 启动独立的 Alertmanager + MailHog（邮件模板来自 deploy/observability/alertmanager/templates）
scripts/alerting-up.sh

# 发一条 critical firing 告警（默认走 19093 → MailHog SMTP）
scripts/send-test-alert.sh --severity critical --status firing \
  --name ContainerOOMKilled --namespace fault-lab

# 浏览器打开 http://127.0.0.1:18025 查看 [FIRING] CRITICAL 邮件
```

**注意（录制前必读）：** 邮件链用的是独立 docker-compose，端口 18025/19093/13200 与 kind 集群的端口转发冲突。录制邮件片段时，确保这些端口没被 kind 转发占用。最简单的做法：邮件片段单独录一段，录完 `scripts/alerting-down.sh` 收尾，再回到集群的 incident 片段。

**失败备用：**

- `curl` 没报 EOF 而是 200 也正常，看下一步 Pod 的 lastState 是否 OOMKilled 即可。
- 若 lastState 还没写入，等几秒重跑最后一条 jsonpath。
- 若 18025 打不开：确认 `scripts/alerting-up.sh` 输出「就绪」，且本机没有残留的 18025 占用（`scripts/alerting-down.sh` 后再 `scripts/alerting-up.sh`）。不要伪造邮件截图。

---

## 第 4 段：Evidence、DeepSeek、RAG、Reviewer（90 秒）

**口播稿：**

> 现在把告警送进 alert-gateway，它做指纹去重后创建 Incident。控制器按 Detected → CollectingEvidence → Diagnosing → PolicyChecking 推进。诊断这一步只读多源证据：K8s 状态、Prometheus、Loki、K8s Event，再叠加 RAG 检索的 Runbook，最后 DeepSeek 给方案，Reviewer 二次审查。token 从 Secret 读进变量，绝不打印。

**画面与命令：**

```bash
# 从 Secret 读 webhook token（只进变量，不 echo）
WEBHOOK_TOKEN="$(kubectl -n aegisops-system get secret aegisops-gateway-token \
  -o jsonpath='{.data.webhook-token}' | base64 -d)"

# 发 Alertmanager v4 webhook，创建 incident
NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
FP="$(openssl rand -hex 8)"
curl -sS -X POST http://127.0.0.1:18080/webhooks/alertmanager \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $WEBHOOK_TOKEN" \
  --data @- <<EOF
{
  "version": "4",
  "groupKey": "{}",
  "status": "firing",
  "alerts": [{
    "status": "firing",
    "labels": {
      "alertname": "ContainerOOMKilled",
      "severity": "critical",
      "namespace": "fault-lab",
      "workload": "faultlab",
      "cluster": "local-k3s"
    },
    "annotations": {
      "summary": "faultlab container OOMKilled",
      "description": "demo injected OOM fault"
    },
    "startsAt": "$NOW",
    "fingerprint": "$FP"
  }]
}
EOF

# 观察状态机（-w 持续输出，看到 AwaitingApproval 后 Ctrl+C）
kubectl -n fault-lab get aiopsincidents -w
```

状态推进：`Detected → CollectingEvidence → Diagnosing → PolicyChecking → AwaitingApproval`。

**失败备用：**

- gateway 返回非 202：看 body 里的 `error`，多数是 webhook token 不对或 JSON 多余字段，改完重发。
- 一直停在 Diagnosing：检查 diagnosis worker 是否 Running（`kubectl -n aegisops-system get pods | grep diagnosis`）。诊断不可用时 incident 会退避重试，恢复后自动继续，不会误执行。
- 若停在 CollectingEvidence 较久，属正常（证据采集 + RAG 检索有耗时），等 30 秒再看。

---

## 第 5 段：审批 + Typed Action（60 秒）

**口播稿：**

> PatchResourceLimit 在策略里是 ApprovalRequired，所以 incident 停在 AwaitingApproval。我用 approver 角色调审批接口，服务端从 Status 里复制 planDigest 绑定，我不能凭空批准一个方案。批准后控制器进入 Executing，执行的是唯一允许改工作负载的 executor 包。

**画面与命令：**

```bash
# 读 approver token（只进变量）
APPROVER_TOKEN="$(kubectl -n aegisops-system get secret aegisops-console-auth \
  -o jsonpath='{.data.tokens}' | base64 -d \
  | awk -F: '$2 ~ /(^|,)approver(,|$)/ {print $1; exit}')"

# 取 incident 名
INCIDENT="$(kubectl -n fault-lab get aiopsincidents \
  -o jsonpath='{.items[?(@.spec.alertName=="ContainerOOMKilled")].metadata.name}')"
echo "$INCIDENT"

# 批准（decision=Approve）
curl -sS -X POST "http://127.0.0.1:18081/api/v1/incidents/fault-lab/${INCIDENT}/approval" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $APPROVER_TOKEN" \
  -d '{"decision":"Approve","reason":"demo 人工批准"}'

# 看 proposal：动作类型 + 参数（内存上限上调）
kubectl -n fault-lab get aiopsincident "$INCIDENT" \
  -o jsonpath='{.status.proposal.action}{"\n"}{.status.proposal.parameters}{"\n"}'
```

也可以切到控制台 UI（http://127.0.0.1:18081）在界面里点审批，效果一致。

**失败备用：**

- 审批返回 CONFLICT：说明 incident 已经不在 AwaitingApproval 阶段（可能已超时或已被处理），`kubectl -n fault-lab get aiopsincident "$INCIDENT" -o jsonpath='{.status.phase}'` 看当前阶段。
- 返回 403：token 角色不是 approver。重新用 awk 提取带 approver 角色的 token。
- `INCIDENT` 为空：webhook 的 alertName 拼写与 jsonpath 过滤条件不一致，改用 `kubectl -n fault-lab get aiopsincidents` 直接抄名字。

---

## 第 6 段：Resolved / rollback + 恢复邮件（60 秒）

**口播稿：**

> 批准后进入 Executing，然后 Verifying。控制器连续两次验证通过才写 Resolved，期间如果验证失败会自动 RollingBack，用执行前快照回滚。这里正常走完，我再发一封 resolved 邮件收尾。

**画面与命令：**

```bash
# 观察 Executing → Verifying → Resolved
kubectl -n fault-lab get aiopsincidents -w

# 最终状态与执行摘要
kubectl -n fault-lab get aiopsincident "$INCIDENT" \
  -o jsonpath='{.status.phase}{"\n"}{.status.execution.lastError}{"\n"}'

# 验证 fault-lab 已恢复（checkout 200）
curl -s http://127.0.0.1:18092/checkout
```

**恢复邮件（独立 docker-compose 链，若已 alerting-down 则先 alerting-up）：**

```bash
scripts/send-test-alert.sh --severity critical --status resolved \
  --name ContainerOOMKilled --namespace fault-lab

# 浏览器 http://127.0.0.1:18025 查看 [RESOLVED] 邮件
scripts/alerting-down.sh   # 邮件片段录完收尾
```

**rollback 补充（口播，可选 15 秒）：** 回滚路径写好了完整闭环：`RollingBack → RolledBack`，用 PG 里的执行前快照恢复。要可复现演示，跑一次 E2E 的 rollback 用例即可：

```bash
scripts/e2e-up.sh --profile full          # 隔离集群 kind-aegisops-e2e
make test-e2e                             # 含回滚、审批、邮件、安全边界
```

**失败备用：**

- 卡在 Verifying：验证窗口默认 2m，属正常，继续等。
- 若进入 RollingBack：如实录下回滚过程，这本身就是卖点，不要手动改成 Resolved。
- `curl checkout` 仍 500：确认注入的故障已清理（`curl -X POST http://127.0.0.1:18092/cleanup`）。

---

## 第 7 段：Grafana / Loki / Tempo / Audit（60 秒）

**口播稿：**

> 最后一环是可观测与审计。Grafana 有现成的 AegisOps 总览面板，Prometheus 暴露 aegisops_* 指标，日志进 Loki，跨组件 trace 进 Tempo，审计事件带哈希链存在 PostgreSQL，时间线接口能按 sequence 和 eventHash 还原。

**画面与命令：**

```bash
# Grafana 总览面板（浏览器打开）
# http://127.0.0.1:13000/d/aegisops-overview
# 面板：活跃 Incident、抓取目标健康、状态转移、修复动作、验证检查

# Prometheus 指标（两个真实指标）
curl -s 'http://127.0.0.1:19090/api/v1/query?query=aegisops_incidents_total'
curl -s 'http://127.0.0.1:19090/api/v1/query?query=faultlab_http_requests_total'

# 审计时间线（viewer token 只读）
VIEWER_TOKEN="$(kubectl -n aegisops-system get secret aegisops-console-auth \
  -o jsonpath='{.data.tokens}' | base64 -d \
  | awk -F: '$2 ~ /(^|,)viewer(,|$)/ {print $1; exit}')"
curl -s "http://127.0.0.1:18081/api/v1/incidents/fault-lab/${INCIDENT}/timeline" \
  -H "Authorization: Bearer $VIEWER_TOKEN"
```

**Loki / Tempo（口播 + 可选命令）：**

```bash
# Loki 采集链路自检（默认 13100；kind 集群里 Loki 只在 e2e full 转发到 13100，
# dev 集群需先手动转发：kubectl -n observability port-forward svc/loki 13100:3100）
scripts/check-loki-evidence.sh --url http://127.0.0.1:13100

# Tempo 查询前端在 13200，trace 通常经 Grafana 的 Tempo 数据源查看
```

**失败备用：**

- Grafana 打不开：`kubectl -n observability get pods` 确认 grafana Pod 就绪，端口转发是否在跑（`scripts/pf-up.sh up --context kind-aegisops-dev`）。
- 指标查询为空：刚跑完的 demo 有数据即可；若完全为空，回看 serviceMonitor 是否启用。
- Loki 自检失败：检查 13100 是否真的被转发，Loki 只在 e2e full profile 才默认转发 13100，dev 集群需手动转发，口播如实说明。

---

## 第 8 段：真实评估结果与限制（30 秒）

**口播稿（务必如实，不粉饰）：**

> 我做了合成数据集上的评估。fake provider 是确定性测试替身，100% 命中只能证明路径可执行，不代表任何模型质量。真实 DeepSeek v2 在 54 个合成样本上 taxonomy 命中 27/54，方案匹配 0/36，但越权动作 0/54，安全降级 18/18。M9.7 受控 36 案例的 v4 基线：危险动作 0/36，安全降级 26/26，严格决策合同 28/36。结论是安全边界守住了，但方案有效性与云端自动修复还达不到放行标准。项目当前不宣称生产可用。

**画面：** 展示 `docs/evaluation.md` 的第 70 到 95 行，或终端里 `cat docs/evaluation.md | sed -n '70,95p'`。

**失败备用：** 数字以 `docs/evaluation.md` 为准，直接照文档念，不要临时编。

---

## 收尾（不进正片）

```bash
scripts/dev-down.sh --context kind-aegisops-dev --yes   # 卸载 release + fault-lab，数据保留
# 可选：--purge-data 删 PVC，--delete-kind-cluster 删 Kind 集群
```

## 录制检查清单（对照计划 14.4 要求）

- [x] 预先写好 `docs/demo-script.md`，不在现场临时敲大量命令
- [x] 录制前执行过 `make smoke CONTEXT=kind-aegisops-dev`
- [x] 视频露出 Git SHA（第 1 段 `git rev-parse --short HEAD`）
- [x] 全程不展示 Secret、Token、密码、真实邮箱（token 只读进变量）
- [x] 每段带失败备用路线，不伪造成功画面
