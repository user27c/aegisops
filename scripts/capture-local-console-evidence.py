#!/usr/bin/env python3
"""Capture authenticated local console evidence without persisting the viewer token.

The token is read only from an ephemeral local E2E environment file, stored in
browser sessionStorage for this one run, and never emitted to stdout, logs, or
the generated screenshot/video.
"""

from __future__ import annotations

import argparse
import json
import shutil
import sys
from pathlib import Path

from playwright.sync_api import sync_playwright


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True, help="Verified local incident-api URL")
    parser.add_argument("--environment", type=Path, required=True, help="Local E2E environment.json")
    parser.add_argument("--screenshot", type=Path, required=True)
    parser.add_argument("--video", type=Path, required=True)
    parser.add_argument("--width", type=int, default=1440, help="Viewport width")
    parser.add_argument("--height", type=int, default=1000, help="Viewport height")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    environment = json.loads(args.environment.read_text(encoding="utf-8"))
    viewer_token = environment.get("viewerToken")
    namespace = environment.get("namespace")
    if not isinstance(viewer_token, str) or not viewer_token:
        raise ValueError("environment 缺少 viewerToken")
    if not isinstance(namespace, str) or not namespace:
        raise ValueError("environment 缺少 namespace")
    if args.width < 320 or args.height < 320:
        raise ValueError("viewport 至少为 320x320")

    args.screenshot.parent.mkdir(parents=True, exist_ok=True)
    args.video.parent.mkdir(parents=True, exist_ok=True)
    video_dir = args.video.parent / ".playwright-video-tmp"
    video_dir.mkdir(parents=True, exist_ok=True)
    api_statuses: list[int] = []

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        context = browser.new_context(
            viewport={"width": args.width, "height": args.height},
            record_video_dir=str(video_dir),
            record_video_size={"width": args.width, "height": args.height},
            color_scheme="light",
        )
        page = context.new_page()
        page.on(
            "response",
            lambda response: api_statuses.append(response.status)
            if "/api/v1/incidents" in response.url
            else None,
        )
        page.goto(args.base_url.rstrip("/") + "/", wait_until="domcontentloaded", timeout=30_000)
        page.evaluate("token => sessionStorage.setItem('aegisops_token', token)", viewer_token)
        page.reload(wait_until="networkidle", timeout=30_000)
        page.wait_for_function(
            "document.querySelector('.incident-table, .empty-state, [role=alert]') !== null",
            timeout=30_000,
        )
        if page.locator('[role="alert"]').count():
            raise RuntimeError("控制台仍显示列表加载错误")
        row_count = page.locator(".incident-table tbody tr").count()
        if not api_statuses or api_statuses[-1] != 200:
            raise RuntimeError(f"事故列表 HTTP 状态异常: {api_statuses}")

        # Exercise the namespace/severity filters before returning to the full list.
        page.locator('input[aria-label="按命名空间过滤"]').fill(namespace)
        page.wait_for_timeout(900)
        page.locator('select[aria-label="按严重级别过滤"]').select_option("critical")
        page.wait_for_timeout(900)
        page.locator('select[aria-label="按严重级别过滤"]').select_option("")
        page.locator('input[aria-label="按命名空间过滤"]').fill("")
        page.wait_for_timeout(900)
        page.screenshot(path=str(args.screenshot), full_page=True)

        if row_count > 0:
            page.locator(".incident-table tbody tr a").first.click()
            page.wait_for_timeout(900)
            page.go_back(wait_until="networkidle")
            page.wait_for_timeout(500)
        recorded_video = Path(page.video.path())
        context.close()
        shutil.copy2(recorded_video, args.video)
        browser.close()

    for temporary_video in video_dir.glob("*"):
        temporary_video.unlink()
    video_dir.rmdir()
    print(f"status={api_statuses[-1]} rows={row_count} screenshot={args.screenshot} video={args.video}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:  # noqa: BLE001 - CLI must expose a non-sensitive failure reason.
        print(f"capture failed: {type(exc).__name__}: {exc}", file=sys.stderr)
        raise SystemExit(1)
