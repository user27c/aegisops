"""Runbook 索引 CLI。M3 阶段实现 index_runbooks / validate_document。"""

from __future__ import annotations

import argparse


def cli() -> None:
    """命令行入口：aegis-runbooks index/validate。"""
    parser = argparse.ArgumentParser(prog="aegis-runbooks", description="Runbook 索引管理")
    parser.add_argument("command", choices=["index", "validate"], help="子命令")
    parser.add_argument("--root", default="runbooks", help="Runbook 目录")
    parser.add_argument("--dry-run", action="store_true", help="只校验不写入")
    parser.add_argument("--prune", action="store_true", help="标记缺失文档为 inactive")
    args = parser.parse_args()

    raise SystemExit(f"aegis-runbooks {args.command} 尚未实现（M3 里程碑交付）")
