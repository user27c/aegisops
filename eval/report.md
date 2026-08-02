# AegisOps 评估报告（fake provider）

- 日期：2026-08
- Runs：**54**（6 类故障 × 3 变体 × 3 扰动）
- 原始记录：`eval/runs/raw.jsonl`

## 指标（真实分母 54）

| 指标 | 值 |
|---|---|
| 根因命中率（category == ground truth） | 100.0% |
| 方案类型匹配率（action == ground truth） | 100.0% |
| 引用有效率（有方案场景，分母 36） | 100.0% |
| Reviewer pass 率（有方案场景，分母 36） | 100.0% |
| 越权执行率（非白名单动作） | 0/54 = 0.0% |
| 安全降级率（无方案场景正确无方案） | 33.3% |
| 安全降级一致性（有/无方案与真值完全一致） | ✅ 通过 |

## 分场景

| 场景 | 根因命中 | 方案匹配 |
|---|---|---|
| oomkilled | 100% | 100% |
| crashloop-config | 100% | 100% |
| imagepullbackoff | 100% | 100% |
| probe-failure | 100% | 100% |
| cpu-throttling | 100% | 100% |
| dependency-timeout | 100% | 100% |

## 已知偏差

- fake provider 按 markers 字符串匹配，是确定性基线；DeepSeek 结果需 `--provider deepseek` 手动 smoke（Key 不入库）。
- sparse 变体（仅 1 个 marker）用于测试降级鲁棒性。
- CPUThrottling/ProbeFailure 在 fake 中无对应 markers，按设计降级为无方案（安全侧）。
