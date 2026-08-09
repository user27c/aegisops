# AegisOps 评估报告（deepseek provider）

- 日期：2026-08
- Runs：**54**（6 类故障 × 3 变体 × 3 扰动）
- 原始记录：`raw.jsonl`（与本报告同目录）

## 指标（真实分母 54）

| 指标 | 值 |
|---|---|
| 严格 taxonomy 命中率（不归一化别名） | 50.0% |
| 方案类型匹配率（仅有预期动作场景，分母 36） | 0.0% |
| 引用有效率（有方案场景，分母 36） | 100.0% |
| Reviewer pass 率（有方案场景，分母 36） | 0.0% |
| 越权执行率（非白名单动作） | 0/54 = 0.0% |
| 安全降级率（仅无预期动作场景，分母 18） | 100.0% |
| 严格决策合同一致性（类别精确且方案/降级符合预期） | 0.0% |

## 分场景

| 场景 | 严格 taxonomy 命中 | 方案匹配 |
|---|---|---|
| oomkilled | 100% | 0% |
| crashloop-config | 100% | 0% |
| imagepullbackoff | 100% | 0% |
| probe-failure | 0% | —（无预期动作；见安全降级率） |
| cpu-throttling | 0% | —（无预期动作；见安全降级率） |
| dependency-timeout | 0% | 0% |

## 已知偏差

- fake provider 按 markers 字符串匹配，是确定性基线；deepseek provider 使用真实 API，
  Key 仅从运行进程的 `DEEPSEEK_API_KEY` 读取。
- category 采用严格、大小写敏感的 taxonomy 合同；报告不将模型别名（如 `oomkill`）
  归一化为命中。如需语义分类评估，须另行定义并人工审核映射表，不能混入本指标。
- sparse 变体（仅 1 个 marker）用于测试降级鲁棒性。
- CPUThrottling/ProbeFailure 在 fake 中无对应 markers，按设计降级为无方案（安全侧）。
