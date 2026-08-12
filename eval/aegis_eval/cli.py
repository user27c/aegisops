"""命令行入口：只允许显式选择 fake 或真实 DeepSeek。"""

from __future__ import annotations

import argparse
import json
from pathlib import Path

from .experiment import CONFIGS, ExperimentOptions, run
from .report import write_report


def main() -> None:
    parser = argparse.ArgumentParser(description="AegisOps M9.7 A/B/C/D evaluator")
    parser.add_argument("--provider", choices=("fake", "deepseek"), required=True)
    parser.add_argument("--dataset", type=Path, default=Path("eval/datasets/v1-verified-r5"))
    parser.add_argument("--output-root", type=Path, default=Path("eval/runs"))
    parser.add_argument("--configs", default=",".join(CONFIGS), help="逗号分隔的 A/B/C/D 配置名")
    parser.add_argument("--max-calls", type=int, default=180)
    parser.add_argument("--max-input-tokens", type=int, default=16_000)
    parser.add_argument("--max-output-tokens", type=int, default=2_048)
    parser.add_argument("--resume", type=Path)
    parser.add_argument("--allow-incomplete-dataset", action="store_true", help="只允许 fake 流程回归")
    parser.add_argument(
        "--confirm-budget",
        action="store_true",
        help="显式确认本次计划逻辑调用数与 token 上限；不会自动放宽成本保护",
    )
    args = parser.parse_args()
    options = ExperimentOptions(
        provider=args.provider,
        dataset_dir=args.dataset,
        output_root=args.output_root,
        config_names=tuple(name for name in args.configs.split(",") if name),
        max_calls=args.max_calls,
        max_input_tokens=args.max_input_tokens,
        max_output_tokens=args.max_output_tokens,
        resume_dir=args.resume,
        allow_incomplete_dataset=args.allow_incomplete_dataset,
        confirm_budget=args.confirm_budget,
    )
    run_dir = run(options)
    manifest = json.loads((run_dir / "manifest.json").read_text(encoding="utf-8"))
    print(f"计划逻辑调用: {manifest['planned_logical_calls']}")
    print(write_report(run_dir))


if __name__ == "__main__":
    main()
