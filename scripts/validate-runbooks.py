#!/usr/bin/env python3
"""校验 runbooks/*.md 的 frontmatter 是否满足 runbooks/schema.json。"""

import glob
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SCHEMA = json.loads((ROOT / "runbooks" / "schema.json").read_text(encoding="utf-8"))
REQUIRED = SCHEMA.get("required", [])


def main() -> int:
    failed = 0
    for p in sorted(glob.glob(str(ROOT / "runbooks" / "*.md"))):
        front: dict[str, str] = {}
        for line in Path(p).read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if line.startswith("---"):
                continue
            if ":" in line:
                k, v = line.split(":", 1)
                front[k.strip()] = v.strip()
        missing = [k for k in REQUIRED if k not in front]
        if missing:
            print(f"FAIL {Path(p).name}: 缺少必需字段 {missing}")
            failed += 1
        else:
            print(f"OK   {Path(p).name}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
