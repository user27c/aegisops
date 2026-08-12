# AegisOps 评估套件

评估分为两条严格隔离的轨道：历史 54-run campaign 用于回归和审计，M9.7 A/B/C/D 用于真实受控采集样本对照。两者都不得覆盖既有原始记录。

## 历史记录

- `runs/raw.jsonl` 与 `report.md` 是 DeepSeek v1 的不可改写审计原件。
- `runs/deepseek-v2/` 是合成 marker 数据的真实 provider 记录；它只证明调用路径，不能证明模型质量。
- `run_campaign.py` 保留为历史回归工具。详见 `docs/evaluation.md`。

## M9.7 A/B/C/D evaluator

代码位于 `aegis_eval/`，配置臂为：A=alert-only、B=evidence、C=evidence+RAG、D=evidence+RAG+review。每一项都记录输入/输出哈希、模型调用元数据和可恢复的 `raw.jsonl`；失败、超时和拒答必须保留在分母。

先运行本地质量门禁：

```bash
uv run --project services/diagnosis ruff check eval/aegis_eval eval/tests
uv run --project services/diagnosis python -m unittest discover -s eval/tests -v
```

Fake 仅用于不花费 API 费用的流程回归。它必须带有 `DETERMINISTIC TEST DOUBLE — NOT MODEL QUALITY` 标识，不能作为模型效果结论：

```bash
uv run --project services/diagnosis python -m eval.aegis_eval.cli \
  --provider fake --output-root /tmp/aegisops-eval-fake --max-calls 185 --confirm-budget
```

`datasets/v1-verified-r5/` 是当前本地受控评测基线：36 条案例已通过 SHA256、来源、动作语义与跨案例故障信号审计，审核标记为 `user-authorized-codex`，表示明确用户授权的可追溯审核，**不是**人工身份声明。真实评测必须显式确认预算：

```bash
uv run --project services/diagnosis python scripts/audit_m97_verified_dataset.py \
  --dataset eval/datasets/v1-verified-r5
uv run --project services/diagnosis python -m eval.aegis_eval.cli \
  --provider deepseek --dataset eval/datasets/v1-verified-r5 \
  --output-root eval/runs --max-calls 180 --confirm-budget
```

`datasets/v1-verified-r4/` 是动作语义审核失败的历史材料，不得作为最终数据集或模型质量结论；仅可复现历史 180 次调用：

```bash
uv run --project services/diagnosis python scripts/audit_m97_verified_dataset.py \
  --dataset eval/datasets/v1-verified-r4
uv run --project services/diagnosis python -m eval.aegis_eval.cli \
  --provider deepseek --dataset eval/datasets/v1-verified-r4 \
  --output-root eval/runs --max-calls 180 --confirm-budget
```

2026-08-11 的 r4 历史 run 已完成；后续发现 OOM 缺动作所需 `MetricSeries`，config/crashloop 真值与 Runbook 不一致，且存在无关 rollback 候选。因此其指标不可归因给模型，必须先本地重采集。详见 `docs/evaluation.md`。

逐案授权审核、再哈希和解除门禁的唯一操作流程见 [M9.7 数据集审核协议](../docs/evaluation-m97-review-protocol.md)。

DeepSeek key 只能从本地进程环境读取，绝不能写入命令历史、仓库、日志或产物。
