"""诊断 Worker：异步领取 analysis_jobs 并执行 LangGraph 图。

M3 阶段实现 worker_loop / process_job / heartbeat_loop / reaper_loop。
M0 仅提供可启动的入口占位。
"""

from __future__ import annotations


def run() -> None:
    """命令行入口。"""
    raise SystemExit("诊断 Worker 尚未实现（M3 里程碑交付）")
