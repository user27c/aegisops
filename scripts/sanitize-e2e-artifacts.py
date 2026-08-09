#!/usr/bin/env python3
"""Fail-closed E2E artifact scanner and sanitizer.

The source directory is an isolated, runner-local diagnostic capture.  This
tool never uploads or mutates it.  In ``--scan-only`` mode it reports whether
raw artifacts contain sensitive values.  With ``--destination`` it writes a
separate, redacted copy and verifies that copy before it can be uploaded.
"""

from __future__ import annotations

import argparse
import re
import sys
from collections import Counter
from pathlib import Path

REDACTED = "[REDACTED]"
TEXT_SUFFIXES = {".json", ".log", ".txt", ".yaml", ".yml", ".out", ".md"}

# These patterns intentionally require an actual value.  Names such as
# DEEPSEEK_API_KEY, a Secret reference, or prose containing "secret" do not
# match and therefore remain useful diagnostic context.
VALUE_PATTERNS: tuple[tuple[str, re.Pattern[str]], ...] = (
    (
        "bearer-token",
        re.compile(r"(?i)(\bbearer[ \t]+)([A-Za-z0-9._~+/-]{8,})"),
    ),
    (
        "api-key-value",
        re.compile(
            r"(?im)(\b(?:api[_-]?key|access[_-]?key|client[_-]?secret)\b[ \t]*[:=][ \t]*[\"']?)([^\s\"',;#]{8,})"
        ),
    ),
    (
        "password-value",
        re.compile(r"(?im)(\b(?:password|passwd|pwd)\b[ \t]*[:=][ \t]*[\"']?)([^\s\"',;#]{8,})"),
    ),
    (
        "token-value",
        re.compile(r"(?im)(\b(?:token|secret)\b[ \t]*[:=][ \t]*[\"']?)([^\s\"',;#]{8,})"),
    ),
    (
        "test-canary",
        re.compile(r"(?i)(AEGISOPS(?:[_-]E2E)?[_-]CANARY(?:[_-](?:TOKEN|SECRET|EMAIL))?[ \t]*[:=][ \t]*)([^\s\"',;#]+)"),
    ),
    (
        "email-address",
        re.compile(r"\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b"),
    ),
)
PRIVATE_KEY = re.compile(
    r"-----BEGIN(?: [A-Z0-9]+)? PRIVATE KEY-----.*?-----END(?: [A-Z0-9]+)? PRIVATE KEY-----",
    re.DOTALL,
)
KIND_SECRET = re.compile(r"(?im)^\s*kind\s*:\s*[\"']?Secret[\"']?\s*$")
SECTION_START = re.compile(r"^(?P<indent>\s*)(?P<key>data|stringData)\s*:\s*(?P<value>.*)$")
MAPPING_VALUE = re.compile(r"^(?P<indent>\s*)(?P<key>[^:#][^:]*):(?:\s*)(?P<value>.*)$")


def is_text(path: Path) -> bool:
    return path.suffix.lower() in TEXT_SUFFIXES or not path.suffix


def redact_secret_data_document(document: str) -> tuple[str, int]:
    """Redact only data/stringData values inside a Kubernetes Secret document."""
    if not KIND_SECRET.search(document):
        return document, 0

    lines = document.splitlines(keepends=True)
    output: list[str] = []
    section_indent: int | None = None
    redactions = 0
    for line in lines:
        if section_indent is not None:
            if not line.strip():
                output.append(line)
                continue
            if len(line) - len(line.lstrip()) > section_indent:
                mapping = MAPPING_VALUE.match(line)
                newline = "\n" if line.endswith("\n") else ""
                if mapping and mapping.group("value").strip() == REDACTED:
                    output.append(line)
                elif not mapping and line.strip() == REDACTED:
                    output.append(line)
                elif mapping:
                    output.append(f"{mapping.group('indent')}{mapping.group('key')}: {REDACTED}{newline}")
                else:
                    output.append(f"{line[: len(line) - len(line.lstrip())]}{REDACTED}{newline}")
                if line.strip() != REDACTED and (not mapping or mapping.group("value").strip() != REDACTED):
                    redactions += 1
                continue
            section_indent = None

        start = SECTION_START.match(line)
        if not start:
            output.append(line)
            continue

        inline_value = start.group("value").strip()
        if not inline_value:
            section_indent = len(start.group("indent"))
            output.append(line)
            continue

        if inline_value in {"{}", REDACTED}:
            output.append(line)
            continue

        newline = "\n" if line.endswith("\n") else ""
        output.append(f"{start.group('indent')}{start.group('key')}: {REDACTED}{newline}")
        redactions += 1
        if inline_value.startswith(("|", ">")):
            section_indent = len(start.group("indent"))
    return "".join(output), redactions


def redact_text(text: str) -> tuple[str, Counter[str]]:
    findings: Counter[str] = Counter()

    def redact_private_key(match: re.Match[str]) -> str:
        findings["private-key"] += 1
        return REDACTED

    result = PRIVATE_KEY.sub(redact_private_key, text)
    documents = re.split(r"(?m)^(---\s*)$", result)
    # re.split retains separators because the expression has a capture group.
    for index in range(0, len(documents), 2):
        sanitized, count = redact_secret_data_document(documents[index])
        documents[index] = sanitized
        if count:
            findings["kubernetes-secret-data"] += count
    result = "".join(documents)

    for category, pattern in VALUE_PATTERNS:
        def replacement(match: re.Match[str], *, category: str = category) -> str:
            # Email has no label prefix; all other patterns preserve the key.
            if category == "email-address":
                findings[category] += 1
                return REDACTED
            if match.group(2) == REDACTED:
                return match.group(0)
            findings[category] += 1
            return f"{match.group(1)}{REDACTED}"

        result = pattern.sub(replacement, result)
    return result, findings


def scan_directory(source: Path, ignore: Path | None = None) -> tuple[Counter[str], dict[Path, Counter[str]]]:
    findings: Counter[str] = Counter()
    by_file: dict[Path, Counter[str]] = {}
    for path in sorted(source.rglob("*")):
        if path == ignore or not path.is_file():
            continue
        if path.is_symlink():
            findings["untrusted-symlink"] += 1
            by_file[path.relative_to(source)] = Counter({"untrusted-symlink": 1})
            continue
        if not is_text(path):
            findings["unreadable-or-binary"] += 1
            by_file[path.relative_to(source)] = Counter({"unreadable-or-binary": 1})
            continue
        try:
            _, file_findings = redact_text(path.read_text(encoding="utf-8"))
        except UnicodeDecodeError:
            file_findings = Counter({"unreadable-or-binary": 1})
        if file_findings:
            findings.update(file_findings)
            by_file[path.relative_to(source)] = file_findings
    return findings, by_file


def write_report(report: Path, findings: Counter[str], by_file: dict[Path, Counter[str]]) -> None:
    report.parent.mkdir(parents=True, exist_ok=True)
    with report.open("w", encoding="utf-8") as handle:
        if not findings:
            handle.write("No sensitive values detected.\n")
            return
        handle.write("Raw artifacts are isolated; do not upload this directory.\n")
        handle.write("Findings list categories and paths only; values are never recorded.\n")
        for path, categories in sorted(by_file.items()):
            handle.write(f"{path}: {', '.join(sorted(categories))}\n")


def prepare_upload(source: Path, destination: Path) -> Counter[str]:
    try:
        destination.resolve().relative_to(source.resolve())
    except ValueError:
        pass
    else:
        raise ValueError("destination must be outside the raw source directory")
    if destination.exists() and any(destination.iterdir()):
        raise ValueError(f"destination must be empty: {destination}")
    destination.mkdir(parents=True, exist_ok=True)
    raw_findings, _ = scan_directory(source)
    for path in sorted(source.rglob("*")):
        if path.is_symlink() or not path.is_file():
            continue
        relative = path.relative_to(source)
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        if not is_text(path):
            # An opaque file cannot be proven safe, so it is deliberately not copied.
            continue
        try:
            sanitized, _ = redact_text(path.read_text(encoding="utf-8"))
        except UnicodeDecodeError:
            continue
        target.write_text(sanitized, encoding="utf-8")

    summary = destination / "REDACTION-SUMMARY.txt"
    summary.write_text(
        "Raw artifact findings were removed from this upload copy.\n"
        + "\n".join(f"{category}: {count}" for category, count in sorted(raw_findings.items()))
        + "\n",
        encoding="utf-8",
    )
    remaining, by_file = scan_directory(destination, ignore=summary)
    if remaining:
        details = "; ".join(f"{path}: {sorted(categories)}" for path, categories in by_file.items())
        raise ValueError(f"sanitized artifact verification failed: {details}")
    return raw_findings


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True, help="isolated raw artifact directory")
    parser.add_argument("--scan-only", action="store_true", help="report raw findings and return 1 when any exist")
    parser.add_argument("--report", type=Path, help="scan-only report path (categories and paths only)")
    parser.add_argument("--destination", type=Path, help="empty directory for the verified upload copy")
    args = parser.parse_args()
    if not args.source.is_dir():
        parser.error(f"source directory does not exist: {args.source}")
    if args.scan_only == (args.destination is not None):
        parser.error("choose exactly one of --scan-only or --destination")

    if args.scan_only:
        findings, by_file = scan_directory(args.source, ignore=args.report)
        if args.report:
            write_report(args.report, findings, by_file)
        return 1 if findings else 0

    try:
        findings = prepare_upload(args.source, args.destination)
    except ValueError as error:
        print(f"artifact sanitizer: {error}", file=sys.stderr)
        return 2
    print(f"prepared verified upload copy; redacted/skipped raw findings: {sum(findings.values())}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
