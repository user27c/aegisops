# 发布门禁汇总（v0.2.0）

- 命令：`scripts/release-check.sh --with-integration-e2e --artifact-dir artifacts/release-v020`
- 最终结果：**exit 0**，最后一行 `[INFO] release-check 全部通过`
- 执行日期：2026-08-13；仓库 `/home/22-7/Documents/日常/aegisops`
- 起始 HEAD：`e56ed4a`；最终 HEAD：`bd9b93a`

## 逐项结果

| 门禁项 | 结果 | 关键信息 |
| ------ | ---- | -------- |
| check-repo-hygiene.sh | ✅ exit 0 | 无跟踪缓存/敏感文件 |
| make verify-generated | ✅ exit 0 | 生成文件无漂移 |
| make verify | ✅ exit 0 | golangci-lint 0 issues；controller 80.2% / executor 80.0% / policy 92.7% |
| make test-envtest | ✅ exit 0 | envtest 1.35.0 |
| make test-integration | ✅ exit 0 | 8 pytest + Alertmanager/MailHog 邮件链路 |
| terraform fmt/validate | ✅ exit 0 | 阿里云 Terraform 配置有效 |
| kubeconform -strict | ✅ exit 0 | 41 资源 Valid 37 / Invalid 0 / Skipped 4（第三方 CRD） |
| promtool check/test rules | ✅ exit 0 | 1 rules + test SUCCESS |
| build-images（5 镜像） | ✅ | Go 1.26.5 + alpine 3.22 + python 3.12-slim |
| trivy HIGH/CRITICAL | ✅ 5/5 镜像 0 漏洞 | `--exit-code 1`（本地脚本正确阻断） |
| Kind E2E（full） | ✅ exit 0 | 9 个顶层用例，901.6s |
| sanitize-e2e-artifacts --scan-only | ✅ exit 0 | 无敏感信息残留 |

## Kind full E2E 用例明细（最后一次全绿运行）

| 用例 | 结果 | 时长 |
| ---- | ---- | ---- |
| TestE2EAlertEmail | ✅ | 282.50s |
| TestE2EApprovalPatchMemory | ✅ | 58.13s |
| TestE2EApprovalScaleCPUFailClosed | ✅ | 333.32s（1→2→1 回滚） |
| TestE2EAutoRestart | ✅ | 28.42s |
| TestE2ELokiEvidenceInPack | ✅ | 0.22s |
| TestE2ELokiFailureStaysPartial | ✅ | 0.03s |
| TestE2ERestoreConfigMapFromImmutableBackup | ✅ | 110.59s |
| TestE2ERollbackDeployment | ✅ | 56.35s |
| TestE2ESecurityBoundaries | ✅ | 32.02s（5 个子用例） |

## 门禁期修复（9 提交，均未 push）

`f61118f`、`720be32`、`fcde76d`、`01306c6`、`03d6afc`、`201be30`、`dfda4fb`、`442438e`、`bd9b93a`
（覆盖 controller 覆盖率补齐、Go 1.26.5 依赖升级、release 脚本修正、verifier PromQL 双计、E2E 竞态与审批 TOCTOU）。

## 工具链版本

promtool 3.13.2 / kubeconform 0.8.0 / syft 1.51.0 / trivy 0.73.0 / golangci-lint v2.8.0（本地源码构建 go1.26.5）/ envtest 1.35.0。

## Actions 链接

- Kind E2E 托管运行（全绿）：https://github.com/user27c/aegisops/actions/runs/31300651719
- CI 运行：https://github.com/user27c/aegisops/actions/runs/31300651720
- （注：本地隔离 Kind full E2E 为发布门禁主证据；GitHub 托管 Kind E2E 为旁证。）
