#!/usr/bin/env python3
"""assert-test-email.py — 通过 MailHog API 断言测试邮件。

检查:收到邮件、subject 含 severity/alertname、正文含 summary/runbook/dashboard。
用法:assert-test-email.py --mailhog-url http://127.0.0.1:18025 [--expect-status firing|resolved]
"""

from __future__ import annotations

import argparse
import json
import quopri
import sys
import time
import urllib.request


def wait_messages(base: str, want: int = 1, timeout: int = 20) -> list[dict]:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(f"{base}/api/v2/messages", timeout=3) as r:
                data = json.load(r)
            items = data.get("items", [])
            if len(items) >= want:
                return items
        except Exception:
            pass
        time.sleep(1)
    return []


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--mailhog-url", default="http://127.0.0.1:18025")
    ap.add_argument("--expect-status", choices=["firing", "resolved"], default="firing")
    ap.add_argument("--expect-severity", default="warning")
    ap.add_argument("--expect-alertname", default="AegisOpsTest")
    args = ap.parse_args()

    # 扫描最近的邮件,选 subject 匹配期望状态的(Alertmanager 会因
    # resolve_timeout 自动发送 RESOLVED,items[0] 未必是目标邮件)。
    items = wait_messages(args.mailhog_url, want=1)
    if not items:
        print("FAIL: 未收到邮件", file=sys.stderr)
        return 1

    status = "RESOLVED" if args.expect_status == "resolved" else "FIRING"
    msg = None
    for m in items:
        raw = str(m.get("Raw", {}).get("Data", ""))
        if status in raw.upper():
            msg = m
            break
    if msg is None:
        print(f"FAIL: 未找到 {status} 邮件(最近 {len(items)} 封)", file=sys.stderr)
        return 1

    content = msg.get("Content", {})
    # MailHog v1:subject 在 Raw.Data 首行;正文在 Content.Body。
    raw_data = str(msg.get("Raw", {}).get("Data", ""))
    subject = ""
    for line in raw_data.split("\r\n"):
        if line.lower().startswith("subject:"):
            subject = line.split(":", 1)[1].strip()
            break
    # multipart 邮件:正文在 Raw.Data 原文中(HTML part),未解析进 Content.Body。
    # Alertmanager 默认 quoted-printable 编码,先解码。
    try:
        body = quopri.decodestring(raw_data.encode("utf-8")).decode("utf-8", errors="replace")
    except Exception:
        body = raw_data

    checks = {
        "subject 含状态": status in subject.upper(),
        "subject 含 severity": args.expect_severity.upper() in subject.upper(),
        "subject 含 alertname": args.expect_alertname in subject,
        "正文含 summary": "AegisOps 测试告警" in body,
        "正文含 runbook": "operations.md" in body,
        "正文含 dashboard": "aegisops-overview" in body,
        "正文含脱敏提示": "不代表 AegisOps 已执行任何修复动作" in body,
    }
    failed = [k for k, ok in checks.items() if not ok]
    for k, ok in checks.items():
        print(f"{'OK' if ok else 'FAIL'} {k}")
    if failed:
        print(f"FAIL: {failed}", file=sys.stderr)
        return 1
    print(f"邮件断言通过: {subject}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
