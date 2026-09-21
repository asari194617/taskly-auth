#!/usr/bin/env python3
"""Run the existing encrypted FTPS backup binary for the isolated auth DB."""

from __future__ import annotations

import json
from pathlib import Path
import subprocess
import sys
import urllib.request


ROOT = Path(__file__).resolve().parents[1]
ENV_FILE = ROOT / ".env.backup"
PRODUCTION_ENV = ROOT / ".env.production"


def webhook_url() -> str:
    for line in PRODUCTION_ENV.read_text(encoding="utf-8").splitlines():
        if line.startswith("DISCORD_WEBHOOK_URL="):
            return line.split("=", 1)[1]
    return ""


def alert_failure() -> None:
    try:
        url = webhook_url()
        if not url:
            return
        body = json.dumps({"content": "Taskly auth database backup failed; inspect protected host logs."}).encode()
        request = urllib.request.Request(
            url, body, {"Content-Type": "application/json", "User-Agent": "TasklyAuth/1.0"}
        )
        with urllib.request.urlopen(request, timeout=5):
            pass
    except (OSError, ValueError):
        print("Discord backup failure alert could not be delivered", file=sys.stderr)


def main() -> int:
    if not ENV_FILE.is_file() or ENV_FILE.stat().st_mode & 0o077:
        alert_failure()
        print("private auth backup environment missing or permissions too broad", file=sys.stderr)
        return 1
    try:
        result = subprocess.run([
            "docker", "exec", "--env-file", str(ENV_FILE),
            "taskly-airflow-scheduler", "/opt/airflow/bin/backup-mysql",
        ], cwd=ROOT, check=False)
    except OSError:
        alert_failure()
        print("auth database backup could not start", file=sys.stderr)
        return 1
    if result.returncode:
        alert_failure()
        print("auth database backup failed", file=sys.stderr)
        return 1
    print("auth database encrypted backup uploaded and verified")
    return 0


if __name__ == "__main__":
    sys.exit(main())
