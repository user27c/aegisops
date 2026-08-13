# v0.2.0 公开证据包（脱敏）

> 本目录把 gitignored 的私有执行证据 `.omo/evidence/` 脱敏整理为**外部读者可复核**的公开版。
> 每项给出：执行命令、代码 SHA、时间、结果摘要与可访问的 GitHub Actions 链接。
> 脱敏规则：邮箱地址、公网 IP、管理 CIDR、云资源 ID（ECS/EIP/安全组/VPC/vSwitch/密钥对）、
> ACR 私有仓库路径、DeepSeek 出口 IP 均已用 `<redacted>` 替换；其余命令与输出保持原样。
> 原始未脱敏记录仍保存在 `.omo/evidence/`（gitignored），仅供维护者核对。

- 发布门禁汇总：[release-gates.md](release-gates.md)
- 真实 SMTP 邮件：[smtp.md](smtp.md)
- 阿里云 k3s 云端演示：[cloud-demo.md](cloud-demo.md)
- 真实 DeepSeek 评估：[deepseek-eval.md](deepseek-eval.md)

## 一致性声明

- 代码最终冻结提交：`bd9b93a`；文档冻结提交：`4f89b60`；`v0.2.0` tag 指向 `4f89b60`。
- 镜像为**本地构建、未推送**；本目录记录的 image digest 均为**本地 image ID**
  （`docker image inspect .Id`），非 GHCR 可拉取的 OCI manifest digest。
- Kind E2E 全绿运行 9 个顶层 `TestE2E` 函数，总时长 901.6s。
- 事实表 29 项能力（26 yes / 3 partial），详见 `../../implementation-status.md`。
