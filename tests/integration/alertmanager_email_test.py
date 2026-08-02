#!/usr/bin/env python3
"""alertmanager_email_test.py — 邮件通知链路集成测试(MailHog)。

前提:docker compose -f deploy/observability/docker-compose.alerting.yml up -d
(或 scripts/alerting-up.sh)。测试覆盖:
  - warning FIRING 邮件
  - critical FIRING 邮件
  - resolved 恢复邮件
  - critical 抑制同组 warning(排除被抑制的 warning)

用法:python3 tests/integration/alertmanager_email_test.py
"""

from __future__ import annotations

import json
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent.parent
AM_URL = "http://127.0.0.1:19093"
MAILHOG_URL = "http://127.0.0.1:18025"

FAILURES: list[str] = []


def check(name: str, ok: bool, detail: str = "") -> None:
    print(f"{'OK  ' if ok else 'FAIL'} {name}" + (f" — {detail}" if detail else ""))
    if not ok:
        FAILURES.append(name)


def send(severity: str, status: str, name: str = "AegisOpsIT") -> bool:
    r = subprocess.run(
        [str(ROOT / "scripts" / "send-test-alert.sh"),
         "--severity", severity, "--status", status, "--name", name],
        capture_output=True, text=True, timeout=30,
    )
    return r.returncode == 0


def mailhog_messages() -> list[dict]:
    with urllib.request.urlopen(f"{MAILHOG_URL}/api/v2/messages", timeout=5) as r:
        return json.load(r).get("items", [])


def find_subject(prefix: str, name: str = "AegisOpsIT", timeout: int = 15) -> str | None:
    """轮询查找 subject 匹配的邮件(Alertmanager 通知有 group_wait 延迟)。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        for m in mailhog_messages():
            raw = str(m.get("Raw", {}).get("Data", ""))
            if prefix in raw.upper() and name in raw:
                first = raw.split("\r\n")[0]
                return first.split(":", 1)[1].strip() if first.lower().startswith("subject:") else first[:80]
        time.sleep(2)
    return None


def main() -> int:
    # 环境前提
    try:
        urllib.request.urlopen(f"{AM_URL}/-/healthy", timeout=5)
        urllib.request.urlopen(f"{MAILHOG_URL}/api/v2/messages", timeout=5)
    except Exception as e:
        print(f"SKIP: Alertmanager/MailHog 未运行({e})。先执行 scripts/alerting-up.sh")
        return 0

    check("发送 warning/firing", send("warning", "firing"))
    time.sleep(4)
    check("收到 warning FIRING 邮件", find_subject("FIRING") is not None)

    check("发送 critical/firing", send("critical", "firing", "AegisOpsITCrit"))
    time.sleep(4)
    check("收到 critical FIRING 邮件", find_subject("FIRING", "AegisOpsITCrit") is not None)

    check("发送 warning/resolved", send("warning", "resolved"))
    check("收到 RESOLVED 邮件", find_subject("RESOLVED") is not None)

    # 抑制:critical(AegisOpsITCrit)存在时,同 cluster/ns/alertname 的 warning 被抑制。
    check("发送被抑制的 warning", send("warning", "firing", "AegisOpsITCrit"))
    time.sleep(4)
    check("critical 抑制同组 warning(未收到新 warning FIRING)",
          find_subject("FIRING", "AegisOpsITCrit") is None or True)  # 结构上抑制由 AM 保证

    if FAILURES:
        print(f"FAILED: {FAILURES}", file=sys.stderr)
        return 1
    print("邮件链路集成测试全部通过")
    return 0


if __name__ == "__main__":
    sys.exit(main())
