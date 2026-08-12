# M9.7 数据集可追溯审核协议

本协议用于解除 M9.7 受控数据集的真实 DeepSeek 运行门禁。自动化采集、schema 检查和 SHA256 校验不等同于可追溯审核；任何 `reviewed_by=automation-*` 记录都不能用于真实模型结论。

每个 case 必须由一名可追溯审核者逐项确认：

1. `provenance.campaign_run_id` 对应受控 FaultLab / Kind 运行，`captured_at` 不被回填或伪造。
2. 故障类型、ground truth category、acceptable / must-not action 与原始受控故障记录一致。
3. evidence 不含 token、Authorization、邮箱、私网地址、证书或未脱敏凭据；prompt-injection 和 multi-fault 标签有实际可复核的证据文本。
4. `reviewed_by` 改为审核者可追溯的身份标记，而不是自动化占位值。人工审核可用 `human:<团队标识>`；若用户明确授权代理审核，必须如实使用 `user-authorized-<agent>`，不得冒充人工审核。

完成所有条目后，重新生成数据集完整性清单，并在不访问模型的前提下验证：

```bash
uv run --project services/diagnosis python scripts/audit_m97_verified_dataset.py \
  --dataset eval/datasets/v1-verified-r5
cd eval/datasets/v1-verified-r5 && sha256sum --check SHA256SUMS
```

若审核者已明确授权并要求代理签署，必须显式确认后才可写入审核字段：

```bash
uv run --project services/diagnosis python scripts/audit_m97_verified_dataset.py \
  --dataset eval/datasets/v1-verified-r5 --sign \
  --confirm 'sign m97 dataset as user-authorized-codex'
```

只有上述检查成功、DeepSeek API key 和可接受费用预算均已获得明确授权后，才可运行真实 A/B/C/D。运行时必须使用显式的 `--max-calls` 与 `--confirm-budget`，并保存新的、不可覆盖的 run 目录；任何失败、拒答或超时保留在分母。
