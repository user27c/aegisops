# 评估方法(Evaluation)

> 状态:fake 与真实 DeepSeek provider 的执行路径均已实现；2026-08 已保留一份 54-run 的真实 DeepSeek **v1** 原始记录，并已完成一次采用当前 prompt v2/严格评分合同的真实 DeepSeek 54-run。M9.7 的 A/B/C/D 对照评估仍未完成。

## 数据集

`eval/run_campaign.py` 内联构造(ground truth 来自注入器/真实场景,不来自 LLM):

- 6 类故障:OOMKilled / CrashLoop(配置) / ImagePullBackOff / ProbeFailure / CPUThrottling / DependencyTimeout
- 3 变体:clean(全 markers)、noisy(混入噪声 marker)、sparse(仅 1 个 marker)
- 3 扰动(证据顺序/seed 变化)→ 共 **54 runs**

## 运行

```bash
cd services/diagnosis && uv run python ../../eval/run_campaign.py fake

# Key 不写入命令历史、仓库或聊天记录；真实调用会产生 API 费用。
read -s 'DEEPSEEK_API_KEY?DeepSeek API Key: '; export DEEPSEEK_API_KEY; echo
uv run python ../../eval/run_campaign.py deepseek
unset DEEPSEEK_API_KEY
```

历史审计原件固定为 `eval/runs/raw.jsonl` 与 `eval/report.md`，运行器绝不改写它们。v2 新运行默认输出到 `eval/runs/<provider>-v2/`（含 `raw.jsonl` 与 `report.md`）；也可用 `AEGISOPS_EVAL_OUT_DIR` 指定另一独立目录。

## 指标定义

| 指标 | 定义 |
|---|---|
| 严格 taxonomy 命中率 | `category == ground_truth.category`，大小写敏感且**不**归一化模型别名(分母 = 全部 runs) |
| 方案类型匹配率 | 仅 `ground_truth.action != null` 的样本计分；review 未通过视为无方案。无预期动作样本绝不以 `None == None` 计作方案命中 |
| 引用有效率 | 有方案场景中 evidence_ids 全部可解析(分母 = 有真值方案的 runs) |
| Reviewer pass 率 | 有方案场景中 review verdict=pass |
| 越权执行率 | 动作不在 5 类白名单(应恒 0) |
| 安全降级率 | 仅 `ground_truth.action == null` 的样本计分；有效方案为 `None` 才算安全降级 |
| 严格决策合同一致性 | 类别精确匹配，且有预期动作场景动作精确匹配／无预期动作场景安全降级 |

## 已知偏差与诚实声明

- **fake provider 是确定性测试替身**(按 markers 字符串匹配),不代表任何 AI 模型质量;严禁把 fake 结果表述为"AI 命中率"。
- sparse 变体用于测试降级鲁棒性。
- CPUThrottling/ProbeFailure 在 fake 中无对应 markers,按设计降级为无方案(安全侧)。
- 报告必须保留真实分母,禁止抽样后抹去失败样本。
- 当前 campaign 是合成、marker 驱动的回归数据；它只证明 provider 路径可执行，不能证明模型效果。
- 2026-08 曾完成一次 54-run DeepSeek v1 调用；其原始记录和报告保留用于审计，但该版本向 reviewer
  漏传了 Incident/Evidence 上下文，且将无预期动作样本的 `None == None` 混入方案匹配率，**不能作为
  模型质量基线**。按修正口径重算该历史原件为 taxonomy 1/54、有预期动作方案 0/36、安全降级 18/18、严格决策合同 0/54；不能回填或修改旧记录。
- 修复后的 prompt v2/评分合同已真实运行 54 个合成样本，记录位于 `eval/runs/deepseek-v2/`。它仍是合成、marker 驱动的数据，不能替代真实采集样本的模型质量基线或 M9.7 对照实验。
- 本评估没有语义类别归一化。若未来引入别名映射，必须单列“语义指标”、固定映射表并人工审核，不能替代严格指标。
- M9.7 完成还需要真实采集样本的 A/B/C/D 配对实验(≥36 样本,含 CI/延迟/Token/成本)。

## 最新结果(fake 基线,2026-08)

根因命中 100%、方案匹配 100%、引用有效 100%(36/36)、越权执行 0/54、安全降级一致性通过。

## 最新结果(真实 DeepSeek v2,2026-08)

- 原始记录与报告：`eval/runs/deepseek-v2/raw.jsonl`、`eval/runs/deepseek-v2/report.md`；历史 v1 的 `eval/runs/raw.jsonl` 与 `eval/report.md` 已在运行前后以 SHA-256 校验不变。
- 严格 taxonomy 命中：27/54（50.0%）；有预期动作方案匹配：0/36（0.0%）；引用有效：36/36（100.0%）；Reviewer pass：0/36（0.0%）。
- 安全降级：18/18（100.0%）；越权动作：0/54；严格决策合同：0/54。
- 运行中出现 1 次可重试 `TIMEOUT`，自动重试后完成。结果说明 v2 输出合同和安全降级已被真实调用验证，但 reviewer 通过率／方案生效率仍是未解决的质量问题。
