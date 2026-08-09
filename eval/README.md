# 评估套件

M8 里程碑交付：

- `datasets/`：`incidents.jsonl` 与证据 fixtures（ground truth 来自注入器，不来自 LLM）。
- `build_dataset.py`：从 Chaos Campaign 导出生成 JSONL。
- `run_experiment.py`：A/B/C 三配置对照实验，支持 `--provider fake|deepseek`。
- `score.py`：根因准确率、Hit@K、MRR、引用有效率、越权执行率。
- `report.py`：Markdown + CSV + PNG 报告，含分母与置信区间。

## 当前状态

- `run_campaign.py`：6 类故障 × 3 变体 × 3 扰动 = 54 runs，按严格 taxonomy/方案/安全降级合同评分。
- 历史审计原件：`runs/raw.jsonl` 与 `report.md`；它们记录 2026-08 的 DeepSeek v1 实验，不能改写，也不能表述为 v2 实测。
- v2 输出：默认写入 `runs/<provider>-v2/{raw.jsonl,report.md}`；可用 `AEGISOPS_EVAL_OUT_DIR` 指向独立目录。
- 运行：`cd services/diagnosis && uv run python ../../eval/run_campaign.py [fake|deepseek]`
- 注意：fake 是确定性测试替身，不代表 AI 效果；2026-08 的真实 DeepSeek v2 54-run 已保存于 `runs/deepseek-v2/`，但真实采集样本的 M9.7 A/B/C/D 对照评估仍待完成。
