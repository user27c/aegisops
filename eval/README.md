# 评估套件

M8 里程碑交付：

- `datasets/`：`incidents.jsonl` 与证据 fixtures（ground truth 来自注入器，不来自 LLM）。
- `build_dataset.py`：从 Chaos Campaign 导出生成 JSONL。
- `run_experiment.py`：A/B/C 三配置对照实验，支持 `--provider fake|deepseek`。
- `score.py`：根因准确率、Hit@K、MRR、引用有效率、越权执行率。
- `report.py`：Markdown + CSV + PNG 报告，含分母与置信区间。

## 当前状态

- `run_campaign.py`：6 类故障 × 3 变体 × 3 扰动 = 54 runs（fake 确定性基线）。
- 原始记录：`runs/raw.jsonl`（每行一个 run，含 ground truth 与结果）。
- 报告：`report.md`（真实分母；根因命中 100%、越权执行 0/54）。
- 运行：`cd services/diagnosis && uv run python ../../eval/run_campaign.py [fake|deepseek]`
- 注意：fake 是确定性测试替身，不代表 AI 效果；真实 DeepSeek 评估见 M9.7。
