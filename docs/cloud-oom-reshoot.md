# 阿里云 k3s OOM 真实演练与全链路自愈报告（博客 6 图来源）

> 状态：**已演练完成并全闭环至 Resolved**（2026-08-15，cn-hangzhou 单节点 k3s，**真实 OOM 故障受控自愈演练**）。本报告记录阿里云实机演练：为博客「OOM → PatchResourceLimit」叙事提供 6 张高精 1440×900 真实原图，并如实归档运行信息、不可变 digest、截图 SHA256 与审计链。

## 1. 运行与环境信息

- **演练时间**：2026-08-15 03:28–05:20 CST (UTC+8)
- **实例规格**：`ecs.e-c1m4.large`（2 vCPU / 8 GiB，Ubuntu 22.04 LTS，杭州区 `i-bp10ihwurt4dtpa8b9b9`）
- **Kubernetes 版本**：`v1.31.5+k3s1`
- **集群标识**：`aliyun-cn-hangzhou-k3s`
- **目标工作负载**：`fault-lab/Deployment/faultlab`（初始内存限额 `256Mi`）
- **故障注入方式**：`fault-lab` 内部每 5 秒平缓分配并写入 28MiB 物理页，持续 50 秒，触碰 256Mi 限额触发真实内核 `OOMKilled`（`exitCode=137`, `RESTARTS=1`）。

---

## 2. 事故与自愈全生命周期事实

- **Incident 名称**：`containeroomkilled-91588`
- **事件指纹**：`sha256:91588a04e16f1bdc43d395f6760cc66c8dba30bf1d70b0c5fa7cd6d975998bdd`
- **证据哈希**：`ba7c5e0bb697df336524e04bbdc15cddc57eb0349bdb6d4952a9c23f79e8a219`
- **诊断结论**：`内存 limit 低于工作集`（置信度 90%）
- **策略判定**：`ApprovalRequired`（Risk: `medium`，生效策略: `fault-lab/fault-lab-default`）
- **不可变 planDigest**：`sha256:1cf4acdd8b5a1163e0393542ccc2b0b8772ec5b88fdfd957a4408c9bcab3fb1d`
- **SRE 审批理由**：`SRE核准: 批准扩大内存至384Mi以消除OOM`
- **自愈动作执行**：`PatchResourceLimit` 将 `faultlab` limits 调整为 `384Mi`
- **健康验证**：新 Pod `faultlab-5f6474595c-h5bp7` 成功处于 `1/1 Running`，探针检测通过（`Healthy`）
- **最终状态**：**`Resolved`**

---

## 3. 固化的 6 张核心截图与 SHA-256 校验和

| 图号 | 文件名 | 分辨率 | SHA-256 校验和 | 核心证明价值 |
| :---: | :--- | :---: | :--- | :--- |
| **图 1** | `01-aegisops-incident-list.png` | 1440×900 | `e7f4f7ea9053831a209ae1892b80e28e75aa650129ea3b56f43f035f88f37121` | 控制台事故总览与状态机列表 |
| **图 2** | `02-aegisops-awaiting-approval.png` | 1440×900 | `c9d427bdca6a0cdb61bbfe6403511626d0537e64ee7d3768bab582172ddde0ed` | AwaitingApproval 详情与 planDigest |
| **图 3** | `03-grafana-oom-timeline.png` | 1440×900 | `d2ee41442031748747ee12a0336773bcfae6efc02447a30c77c80b5747b179f1` | 真实 50s 阶梯内存 OOM 时序（固定窗口 03:28–03:40） |
| **图 4** | `04-tempo-distributed-traces.png` | 1440×900 | `f1daed2701b98e2ac87c852bb57cd04a57545fe8ce41655f19ec256946c1b671` | Tempo 分布式追踪瀑布图（Trace B: 11 / C: 5 Spans） |
| **图 5** | `05-aegisops-execution-audit.png` | 1440×900 | `c71ef2a059d9711680828963747c1a72232403160df19ab9a333bbc4e9084ecb` | 执行生命周期与连续审计日志流（#1~#4） |
| **图 6** | `06-aegisops-resolved-overview.png` | 1440×900 | `68957788dd71e584954bc712a574e78420d92cc8e02654cc7819dc40c5058fe5` | Resolved 最终恢复态与健康验证 |

---

## 4. 连续防篡改审计链（PostgreSQL 记录）

```text
Sequence 1: ApprovalGranted   | Actor: token-75af1332106060de | Reason: SRE核准: 批准扩大内存至384Mi以消除OOM | Hash: #8a0e1a58bb47
Sequence 2: ExecutionStarted  | Actor: operator               | Event Hash: #d0da4fed80f5
Sequence 3: ExecutionCompleted| Actor: operator               | Message: 已调整容器 faultlab 的 limit        | Hash: #ee0833a17663
Sequence 4: IncidentResolved  | Actor: operator               | Event Hash: #f6a995141a41
```
