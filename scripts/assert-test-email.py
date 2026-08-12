#!/usr/bin/env python3
"""assert-test-email.py — 断言测试邮件(两种模式)。

MailHog 模式(默认):通过 MailHog API 检查收到邮件、subject 含 severity/alertname、
正文含 summary/runbook/dashboard。
真实 SMTP 模式(--real-smtp):经 Alertmanager /metrics 断言投递成功,不访问邮箱。
用法:
  assert-test-email.py --mailhog-url http://127.0.0.1:18025 [--expect-status firing|resolved]
  assert-test-email.py --real-smtp --alertmanager-url http://127.0.0.1:19094 [--expect-min-delivered 2]
"""

from __future__ import annotations

import argparse
import json
import quopri
import re
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


def metric_sum(body: str, metric: str) -> int:
    """从 Prometheus 文本格式中,累加指定 metric 的 email 通知计数。

    只统计 integration="email" 的样本。绝不接触/打印任何 SMTP 密码。
    """
    total = 0
    for line in body.splitlines():
        if not line.startswith(metric):
            continue
        if 'integration="email"' not in line:
            continue
        m = re.search(r"[-+]?[0-9]+(?:\.[0-9]+)?$", line.strip())
        if m:
            total += int(float(m.group(0)))
    return total


def check_real_smtp(
    am_url: str, min_delivered: int, timeout: int, settle: int
) -> tuple[int, int, int]:
    """轮询 Alertmanager /metrics,断言真实 SMTP 邮件投递达标。

    Alertmanager 指标语义:
    - alertmanager_notifications_total{integration="email"}:通知周期计数(含失败周期)。
    - alertmanager_notifications_failed_total{integration="email"}:最终失败的通知周期数。
    净投递数 = total - failed(成功周期每完成一次 +1,失败周期净投递不变)。
    成功判定 = 净投递数 >= min_delivered 且持续 settle 秒(覆盖 SMTP 失败重试约 30s 的
    记账延迟,避免把"仍在重试中的失败"或"指标尚未归位"误判为成功/失败)。
    不读取任何邮箱/IMAP 凭据,因此不会泄漏密码。
    """
    deadline = time.time() + timeout
    last = (0, 0, 0)
    success_at = None
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(f"{am_url}/metrics", timeout=5) as r:
                body = r.read().decode("utf-8", errors="replace")
            notifications = metric_sum(body, "alertmanager_notifications_total")
            failed = metric_sum(body, "alertmanager_notifications_failed_total")
            delivered = notifications - failed
            last = (notifications, failed, delivered)
            if delivered < min_delivered:
                success_at = None  # 净投递回落,重置稳定计时
            elif success_at is None:
                success_at = time.time()
            elif time.time() - success_at >= settle:
                return notifications, failed, delivered  # 稳定达标
        except Exception:
            pass
        time.sleep(2)
    return last


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--mailhog-url", default="http://127.0.0.1:18025")
    ap.add_argument("--expect-status", choices=["firing", "resolved"], default="firing")
    ap.add_argument("--expect-severity", default="warning")
    ap.add_argument("--expect-alertname", default="AegisOpsTest")
    ap.add_argument(
        "--real-smtp",
        action="store_true",
        help="真实 SMTP 模式:经 Alertmanager 指标断言投递成功(不访问邮箱)",
    )
    ap.add_argument(
        "--alertmanager-url",
        default="http://127.0.0.1:19093",
        help="--real-smtp 模式下被检查的 Alertmanager 地址",
    )
    ap.add_argument(
        "--expect-min-delivered",
        type=int,
        default=2,
        help="--real-smtp 模式下要求的最低净投递邮件数(默认 2=firing+resolved)",
    )
    ap.add_argument(
        "--settle",
        type=int,
        default=40,
        help="--real-smtp 模式下成功后的稳定期(秒),覆盖失败重试的记账延迟",
    )
    ap.add_argument(
        "--timeout", type=int, default=120, help="--real-smtp 模式下轮询超时(秒)"
    )
    args = ap.parse_args()

    if args.real_smtp:
        notifications, failed, delivered = check_real_smtp(
            args.alertmanager_url,
            args.expect_min_delivered,
            args.timeout,
            args.settle,
        )
        ok = delivered >= args.expect_min_delivered
        line = (
            f"{'OK' if ok else 'FAIL'} 真实 SMTP 投递: delivered={delivered} "
            + f"(期望>={args.expect_min_delivered}) total={notifications} failed={failed}"
        )
        print(line)
        if ok:
            print("真实 SMTP 邮件断言通过")
            return 0
        print(
            f"FAIL: 真实 SMTP 邮件未达预期 (delivered={delivered}, total={notifications}, failed={failed})",
            file=sys.stderr,
        )
        return 1

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
        # 隔离性:必须同时匹配状态与 alertname,避免历史邮件干扰。
        if status in raw.upper() and args.expect_alertname in raw:
            msg = m
            break
    if msg is None:
        print(
            f"FAIL: 未找到 {status}/{args.expect_alertname} 邮件(最近 {len(items)} 封)",
            file=sys.stderr,
        )
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
        body = quopri.decodestring(raw_data.encode("utf-8")).decode(
            "utf-8", errors="replace"
        )
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
