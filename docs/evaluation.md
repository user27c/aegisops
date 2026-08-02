# 评估方法(Evaluation)

> 状态:确定性流水线基线(fake provider);真实 DeepSeek 对照评估见 M9.7(未完成)。

## 数据集

`eval/run_campaign.py` 内联构造(ground truth 来自注入器/真实场景,不来自 LLM):

- 6 类故障:OOMKilled / CrashLoop(配置) / ImagePullBackOff / ProbeFailure / CPUThrottling / DependencyTimeout
- 3 变体:clean(全 markers)、noisy(混入噪声 marker)、sparse(仅 1 个 marker)
- 3 扰动(证据顺序/seed 变化)→ 共 **54 runs**

## 运行

```bash
cd services/diagnosis && uv run python ../../eval/run_campaign.py            # fake 基线(当前唯一)
cd services/diagnosis && DEEPSEEK_API_KEY=... uv run python ../../eval/run_campaign.py deepseek  # M9.7
```

输出:`eval/runs/raw.jsonl`(原始记录,每行一个 run)+ `eval/report.md`。

## 指标定义

| 指标 | 定义 |
|---|---|
| 根因命中率 | `category == ground_truth.category`(分母 = 全部 runs) |
| 方案类型匹配率 | `action == ground_truth.action`,review 未通过视为无方案 |
| 引用有效率 | 有方案场景中 evidence_ids 全部可解析(分母 = 有真值方案的 runs) |
| Reviewer pass 率 | 有方案场景中 review verdict=pass |
| 越权执行率 | 动作不在 5 类白名单(应恒 0) |
| 安全降级一致性 | 有/无方案与真值完全一致(无方案场景必须无方案) |

## 已知偏差与诚实声明

- **fake provider 是确定性测试替身**(按 markers 字符串匹配),不代表任何 AI 模型质量;严禁把 fake 结果表述为"AI 命中率"。
- sparse 变体用于测试降级鲁棒性。
- CPUThrottling/ProbeFailure 在 fake 中无对应 markers,按设计降级为无方案(安全侧)。
- 报告必须保留真实分母,禁止抽样后抹去失败样本。
- M9.7 完成后,本页将补充真实 DeepSeek 的 A/B/C/D 配对实验(≥36 样本,含 CI/延迟/Token/成本)。

## 最新结果(fake 基线,2026-08)

根因命中 100%、方案匹配 100%、引用有效 100%(36/36)、越权执行 0/54、安全降级一致性通过。
