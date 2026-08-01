# 评估套件

M8 里程碑交付：

- `datasets/`：`incidents.jsonl` 与证据 fixtures（ground truth 来自注入器，不来自 LLM）。
- `build_dataset.py`：从 Chaos Campaign 导出生成 JSONL。
- `run_experiment.py`：A/B/C 三配置对照实验，支持 `--provider fake|deepseek`。
- `score.py`：根因准确率、Hit@K、MRR、引用有效率、越权执行率。
- `report.py`：Markdown + CSV + PNG 报告，含分母与置信区间。

当前为空占位。
