#!/usr/bin/env python3
"""alertmanager_email_test.py — 邮件通知链路集成测试(MailHog)。

前提:scripts/alerting-up.sh。服务未运行时必须失败，不能假绿。测试覆盖:
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
from uuid import uuid4

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
        check=False, capture_output=True, text=True, timeout=30,
    )
    if r.returncode:
        print(f"send failed: {r.stderr.strip()}", file=sys.stderr)
    return r.returncode == 0


def mailhog_messages() -> list[dict]:
    with urllib.request.urlopen(f"{MAILHOG_URL}/api/v2/messages", timeout=5) as r:
        return json.load(r).get("items", [])


def matching_messages(status: str, severity: str, name: str) -> list[dict]:
    """按唯一告警名、状态和严重度过滤，避免历史邮件污染。"""
    matches = []
    for message in mailhog_messages():
        raw = str(message.get("Raw", {}).get("Data", "")).upper()
        if status.upper() in raw and severity.upper() in raw and name.upper() in raw:
            matches.append(message)
    return matches


def wait_for_new_message(status: str, severity: str, name: str, before: int, timeout: int = 15) -> bool:
    """轮询等到匹配邮件数增加（Alertmanager 有 group_wait）。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if len(matching_messages(status, severity, name)) > before:
            return True
        time.sleep(2)
    return False


def no_new_message(status: str, severity: str, name: str, before: int, timeout: int = 6) -> bool:
    """在抑制窗口内匹配邮件数不增加。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if len(matching_messages(status, severity, name)) > before:
            return False
        time.sleep(1)
    return True


def main() -> int:
    # 环境前提
    try:
        urllib.request.urlopen(f"{AM_URL}/-/healthy", timeout=5)
        urllib.request.urlopen(f"{MAILHOG_URL}/api/v2/messages", timeout=5)
    except Exception as exc:
        print(f"FAIL: Alertmanager/MailHog 未运行({exc})。先执行 scripts/alerting-up.sh", file=sys.stderr)
        return 1

    run_id = uuid4().hex[:12]
    warning_name = f"AegisOpsIT-{run_id}"
    critical_name = f"AegisOpsITCrit-{run_id}"

    warning_before = len(matching_messages("FIRING", "warning", warning_name))
    check("发送 warning/firing", send("warning", "firing", warning_name))
    check("收到 warning FIRING 邮件", wait_for_new_message("FIRING", "warning", warning_name, warning_before))

    critical_before = len(matching_messages("FIRING", "critical", critical_name))
    check("发送 critical/firing", send("critical", "firing", critical_name))
    check("收到 critical FIRING 邮件", wait_for_new_message("FIRING", "critical", critical_name, critical_before))

    resolved_before = len(matching_messages("RESOLVED", "warning", warning_name))
    check("发送 warning/resolved", send("warning", "resolved", warning_name))
    check("收到 RESOLVED 邮件", wait_for_new_message("RESOLVED", "warning", warning_name, resolved_before))

    # 抑制:critical 存在时，同 cluster/ns/alertname 的 warning 不应产生新邮件。
    inhibited_before = len(matching_messages("FIRING", "warning", critical_name))
    check("发送被抑制的 warning", send("warning", "firing", critical_name))
    check(
        "critical 抑制同组 warning（未收到新 warning FIRING）",
        no_new_message("FIRING", "warning", critical_name, inhibited_before),
    )

    # 清理本次唯一告警，避免残留 firing 状态影响下一次运行。
    check("清理 warning", send("warning", "resolved", warning_name))
    check("清理 critical", send("critical", "resolved", critical_name))

    if FAILURES:
        print(f"FAILED: {FAILURES}", file=sys.stderr)
        return 1
    print("邮件链路集成测试全部通过")
    return 0


if __name__ == "__main__":
    sys.exit(main())
