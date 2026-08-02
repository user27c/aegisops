---
run_id: <run-id>
reviewed: false
---

# Postmortem: <run-id>

## Summary
一句话描述事故与处理结果。

## Impact
- 影响面：
- 时长：
- 严重级别：

## Timeline
| 时间 | 事件 |
|---|---|

## Detection
告警名 / 触发方式 / 首次响应。

## Evidence
- 证据快照 ID：
- 关键发现（指标/日志/事件）：

## Root Cause
诊断结论（category + confidence）。

## Contributing Factors
（如适用）

## Remediation
执行的动作与参数、审批记录。

## Verification
验证方式与连续成功次数。

## What Went Well / What Failed
## Action Items
- [ ] （跟进项，reviewed 后进入 RAG）

## Raw Artifact Links
- incident CR 导出、analysis job、audit 事件区间。

> 约束：LLM 生成的草稿必须经人工确认并把 frontmatter 改为 `reviewed: true` 才能进入 RAG 索引。
