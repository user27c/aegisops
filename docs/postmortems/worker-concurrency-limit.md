---
run_id: M9.1b-worker-concurrency-limit
reviewed: false
---

# Postmortem: Worker 并发上限未真正生效

## Summary

Diagnosis Worker 的 `worker_concurrency` 配置名义上约束并发，但原实现用 `asyncio.Semaphore(worker_concurrency)` 只包裹「领取任务 + 创建 task」的同步片段：`async with semaphore:` 在 `create_task` 后立即释放信号量，真正的处理发生在后台任务 `_process_wrapped` 里，完全不受信号量约束。告警突发时可能在途任务数远超配置上限，导致 Worker Pod OOM 与对 DeepSeek 的并发调用风暴。已重构为容量驱动循环（`wait_for_capacity` / `discard_finished` / `claim_one`），在途任务数 ≤ `worker_concurrency`，并补 `worker_concurrency ∈ [1,32]` 校验。

## Impact

- 影响面：Diagnosis Worker Pod（内存 / CPU / DB 连接），以及对 DeepSeek 的并发调用（成本与限流）。
- 时长：从 M3 到 M9.1b（commit `f8b616b`）。
- 严重级别：中高（资源耗尽 + 成本失控；非数据正确性问题）。

## Timeline

| 时间                           | 事件                                                                      |
| ------------------------------ | ------------------------------------------------------------------------- |
| M3                             | Worker 上线，semaphore 仅约束领取循环片段                                 |
| M9.1 规划（NEXT-STEPS §6.2）   | 代码审查确认「semaphore 在创建 task 后立即释放，不能约束在途任务」        |
| 2026-08-02（commit `f8b616b`） | 重构为容量驱动循环 + 1–32 校验，补 5 个单测                               |
| 2026-08（commit `329c06e`）    | 补真实 pgvector PostgreSQL 集成测试（峰值并发/双 Worker/回收/异常不阻塞） |

## Detection

非运行时告警。由 M9.1 事实表审计（对照 `docs/implementation-status.md` 第 16 行「Worker 并发与过期任务回收」）结合代码审查发现的静态缺陷：信号量释放时机错误。

## Evidence

- 事实表第 16 行（`docs/implementation-status.md`）：「Worker 并发与过期任务回收」由 partial 补强，证据列指向 `services/diagnosis/tests/integration/diagnosis/test_worker_concurrency.py`。
- 代码证据：修复前 `worker_loop` 中 `async with semaphore:` 块只包住 `claim_next` + `create_task` + `add_done_callback`，处理在块外。
- 修复 commit：`f8b616b`（M9.1b）+ `329c06e`（集成测试）。

## Root Cause

`asyncio.Semaphore` 的临界区覆盖范围错误。信号量只保护了「领取 + 创建 task」这一短暂同步片段，任务一被 `create_task` 创建即离开 `async with` 块并释放许可；实际耗时在后台任务内执行，从未进入信号量管辖范围，因此 `worker_concurrency` 从未真正限制在途任务数。

## Contributing Factors

- semaphore 语义易误用：许可计数绑定的是「进入临界区」而非「任务生命周期」。
- 无真实 PostgreSQL + 突发负载下的峰值并发断言，缺陷长期未被观测到。

## Why Tests Missed It

- 原实现只有离线逻辑测试，未在真实 PostgreSQL 上驱动 `worker_loop` 的 claim / heartbeat / reaper 路径。
- 缺少「突发 N 个任务、并发上限 2 时峰值并发必须 == 2」这类断言；单测不触发真实领取与后台处理。

## Corrective Action

- 重构 `services/diagnosis/app/worker.py` 的 `worker_loop` 为容量驱动循环：
  - `wait_for_capacity(tasks, concurrency)`：在途任务数达到上限时 `asyncio.wait(FIRST_COMPLETED)` 阻塞到至少一个任务结束。
  - `discard_finished(tasks)`：移除已完成任务并读取 exception，避免 `Task exception was never retrieved`。
  - `claim_one(deps, worker_id)`：独立短事务内领取任务。
- `services/diagnosis/app/config.py`：新增 `worker_concurrency` field_validator，取值 1–32。

## Regression Test

- 单元测试文件：`services/diagnosis/tests/unit/test_worker_concurrency.py`（5 个用例）。
  - 命令：`cd services/diagnosis && uv run pytest tests/unit/test_worker_concurrency.py -q`
  - 覆盖：满员阻塞、未满不阻塞、完成移除保留 pending、异常读取、1–32 配置校验。实测 `5 passed`。
- 集成测试文件（真实 pgvector PostgreSQL）：`services/diagnosis/tests/integration/diagnosis/test_worker_concurrency.py`（4 个用例）。
  - 命令：`cd services/diagnosis && TESTCONTAINERS_RYUK_DISABLED=true uv run pytest tests/integration/diagnosis/test_worker_concurrency.py -q`
  - 覆盖：(a) 突发 20 任务、并发上限 2 时峰值并发 == 2；(b) 双 Worker 经 `FOR UPDATE SKIP LOCKED` 不重复领取同一 Job；(c) 心跳过期任务回收 requeue 且 attempt 不超过 max_attempts；(d) 单个任务抛异常不阻塞队列。
  - 实测结果：`4 passed`。

## Preventive Control

- 并发约束改由「在途任务集合大小」实现，而非信号量，语义上不再依赖临界区范围正确性。
- 峰值并发断言纳入真实 PostgreSQL 集成测试基线（`test_worker_peak_concurrency_bounded_by_config`）。
- `worker_concurrency` 上界校验 1–32，防止误配导致无界并发。

## Verification

- `cd services/diagnosis && uv run pytest tests/unit/test_worker_concurrency.py -q` → `5 passed`。
- `cd services/diagnosis && TESTCONTAINERS_RYUK_DISABLED=true uv run pytest tests/integration/diagnosis/test_worker_concurrency.py -q` → `4 passed`。

## What Went Well / What Failed

- Well：修复用真实 PostgreSQL 集成测试锁定并发上限，而非仅依赖离线单测。
- Failed：信号量语义误用未能在一开始被代码审查拦截。

## Action Items

- [ ] 由人工确认并把 frontmatter 改为 `reviewed: true` 后进入 RAG 索引。

## Raw Artifact Links

- 修复 commit：`f8b616b`（M9.1b: Diagnosis Worker 并发上限真实生效）、`329c06e`（test(diagnosis): 补真实 PG 并发上限与 stale 回收集成测试）。
- 代码：`services/diagnosis/app/worker.py`、`services/diagnosis/app/config.py`。

> 约束：LLM 生成的草稿必须经人工确认并把 frontmatter 改为 `reviewed: true` 才能进入 RAG 索引。
