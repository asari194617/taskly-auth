#!/usr/bin/env python3
"""Fail-closed checks for the prepared, pre-cutover production state.

Run with sudo on the production host. The script never prints environment
values, tokens, credentials, cookies, or user data.
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path
import stat
import subprocess
import sys
from urllib import error, parse, request


AUTH_FLAGS = (
    "AUTH_PASSKEYS_EXTERNAL",
    "AUTH_PASSWORD_EXTERNAL",
    "AUTH_EMAIL_SETTINGS_EXTERNAL",
    "AUTH_RECOVERY_EXTERNAL",
    "AUTH_TOTP_EXTERNAL",
    "AUTH_BLOCKS_EXTERNAL",
    "AUTH_ACCOUNT_LIFECYCLE_EXTERNAL",
    "AUTH_USERS_EXTERNAL",
)
AUTH_EXPECTED = {
    "AUTH_ISSUER": "https://taskly-auth-api.iy0306.com",
    "AUTH_UI_URL": "https://auth.taskly.iy0306.com",
    "WEBAUTHN_RP_ID": "taskly.iy0306.com",
    "OAUTH_CLIENT_ID": "taskly-web",
    "OAUTH_REDIRECT_URI": "https://api.taskly.iy0306.com/auth/oauth/callback",
}
TASKLY_EXPECTED = {
    "AUTH_ISSUER": AUTH_EXPECTED["AUTH_ISSUER"],
    "AUTH_CLIENT_ID": AUTH_EXPECTED["OAUTH_CLIENT_ID"],
    "AUTH_REDIRECT_URI": AUTH_EXPECTED["OAUTH_REDIRECT_URI"],
    "WEB_PUBLIC_URL": "https://taskly.iy0306.com",
    "AUTH_BLOCK_SYNC_URL": "http://auth:3067",
    "AUTH_LIFECYCLE_SYNC_URL": "http://auth:3067",
    "AUTH_USER_SYNC_URL": "http://auth:3067",
}


class Checks:
    def __init__(self) -> None:
        self.failures: list[str] = []

    def check(self, condition: bool, description: str) -> None:
        if condition:
            print(f"PASS {description}")
        else:
            print(f"FAIL {description}")
            self.failures.append(description)


def read_env(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key] = value
    return values


def validate_prepared_env(auth: dict[str, str], taskly: dict[str, str]) -> list[str]:
    failures: list[str] = []
    for key, expected in AUTH_EXPECTED.items():
        if auth.get(key) != expected:
            failures.append(f"auth {key}")
    for key, expected in TASKLY_EXPECTED.items():
        if taskly.get(key) != expected:
            failures.append(f"Taskly {key}")
    for key in AUTH_FLAGS:
        if taskly.get(key) != "0":
            failures.append(f"Taskly {key}=0")
    if taskly.get("AUTH_WRITES_PAUSED") != "0":
        failures.append("Taskly AUTH_WRITES_PAUSED=0")

    client_secret = auth.get("OAUTH_CLIENT_SECRET", "")
    if len(client_secret) < 32 or client_secret != taskly.get("AUTH_CLIENT_SECRET"):
        failures.append("matching OAuth client secrets")
    internal_token = auth.get("AUTH_INTERNAL_TOKEN", "")
    if len(internal_token) < 32 or internal_token != taskly.get("AUTH_INTERNAL_TOKEN"):
        failures.append("matching internal tokens")

    dsn = auth.get("AUTH_DSN", "")
    query = parse.parse_qs(dsn.split("?", 1)[1], keep_blank_values=True) if "?" in dsn else {}
    if query.get("parseTime") != ["true"]:
        failures.append("auth DSN parseTime=true")
    if query.get("loc") != ["UTC"]:
        failures.append("auth DSN loc=UTC")
    if query.get("charset") != ["utf8mb4"]:
        failures.append("auth DSN charset=utf8mb4")
    return failures


def command(args: list[str], *, input_text: str | None = None) -> str:
    result = subprocess.run(
        args,
        input=input_text,
        text=True,
        capture_output=True,
        check=False,
        timeout=30,
    )
    if result.returncode:
        raise RuntimeError("command failed")
    return result.stdout.strip()


def inspect_container(name: str) -> dict[str, object]:
    return json.loads(command(["docker", "inspect", name]))[0]


def http_status(url: str, method: str = "GET", body: bytes | None = None) -> int:
    req = request.Request(
        url,
        method=method,
        data=body,
        headers={"User-Agent": "TasklyAuthReadiness/1.0", "Content-Type": "application/json"},
    )
    try:
        with request.urlopen(req, timeout=10) as response:
            return response.status
    except error.HTTPError as response:
        return response.code
    except (error.URLError, TimeoutError):
        return 0


def container_env(inspect: dict[str, object]) -> dict[str, str]:
    config = inspect.get("Config", {})
    items = config.get("Env", []) if isinstance(config, dict) else []
    return dict(item.split("=", 1) for item in items if isinstance(item, str) and "=" in item)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--auth-env", type=Path, default=Path("/home/ubuntu/taskly-auth/.env.production"))
    parser.add_argument("--taskly-env", type=Path, default=Path("/home/github-deploy/opt/taskly/.env"))
    parser.add_argument("--expected-auth-tag", default="")
    args = parser.parse_args()
    checks = Checks()

    for label, path in (("auth environment", args.auth_env), ("Taskly environment", args.taskly_env)):
        checks.check(path.is_file(), f"{label} exists")
        if path.is_file():
            checks.check(stat.S_IMODE(path.stat().st_mode) & 0o077 == 0, f"{label} is private")
    if checks.failures:
        return 1

    auth_env = read_env(args.auth_env)
    taskly_env = read_env(args.taskly_env)
    env_failures = validate_prepared_env(auth_env, taskly_env)
    for failure in env_failures:
        checks.check(False, failure)
    if not env_failures:
        checks.check(True, "dormant auth settings are complete and consistent")

    try:
        auth = inspect_container("taskly-auth-auth-1")
        taskly = inspect_container("infra-server-1")
        host = auth.get("HostConfig", {})
        state = auth.get("State", {})
        health = state.get("Health", {}) if isinstance(state, dict) else {}
        checks.check(state.get("Running") is True, "auth container is running")
        checks.check(health.get("Status") == "healthy", "auth container is healthy")
        checks.check(host.get("ReadonlyRootfs") is True, "auth container root filesystem is read-only")
        checks.check("ALL" in (host.get("CapDrop") or []), "auth container drops all capabilities")
        checks.check("no-new-privileges:true" in (host.get("SecurityOpt") or []), "auth container forbids new privileges")
        checks.check(container_env(auth).get("AUTH_MIGRATION_READY") == "0", "auth migration gate is closed")
        checks.check(container_env(taskly).get("AUTH_WRITES_PAUSED") == "0", "Taskly maintenance gate is open")
        checks.check(all(container_env(taskly).get(key) == "0" for key in AUTH_FLAGS), "Taskly ownership flags are dormant")
        if args.expected_auth_tag:
            image = str(auth.get("Config", {}).get("Image", ""))
            checks.check(image.endswith(":" + args.expected_auth_tag), "auth image uses the reviewed commit tag")
    except (RuntimeError, KeyError, TypeError, json.JSONDecodeError):
        checks.check(False, "production containers can be inspected")

    try:
        names = command(["docker", "ps", "--format", "{{.Names}}"])
        checks.check("taskly-auth-passkey-live-" not in names, "temporary passkey container is absent")
        checks.check(command(["docker", "exec", "infra-server-1", "curl", "-fsS", "http://auth:3067/healthz"]) == "ok", "Taskly reaches auth over the private network")
    except RuntimeError:
        checks.check(False, "private auth network is reachable")

    try:
        counts = command([
            "docker", "exec", "infra-db-1", "sh", "-lc",
            'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" --batch --skip-column-names '
            '-e "SELECT (SELECT COUNT(*) FROM taskly.users),'
            '(SELECT COUNT(*) FROM taskly_auth.users);"',
        ]).split("\t")
        checks.check(len(counts) == 2 and int(counts[0]) > 0, "source users remain present")
        checks.check(len(counts) == 2 and int(counts[1]) == 0, "cutover target remains empty")
    except (RuntimeError, ValueError):
        checks.check(False, "identity database counts are readable")

    public = (
        ("auth health", "https://taskly-auth-api.iy0306.com/healthz", "GET", None, 200),
        ("OAuth metadata", "https://taskly-auth-api.iy0306.com/.well-known/oauth-authorization-server", "GET", None, 200),
        ("auth API gate", "https://taskly-auth-api.iy0306.com/auth/login", "POST", b"{}", 503),
        ("internal API public block", "https://taskly-auth-api.iy0306.com/internal/users/1", "GET", None, 404),
        ("auth UI login", "https://auth.taskly.iy0306.com/login", "GET", None, 200),
        ("auth UI security", "https://auth.taskly.iy0306.com/security", "GET", None, 200),
    )
    for label, url, method, body, expected in public:
        checks.check(http_status(url, method, body) == expected, f"{label} returns {expected}")

    try:
        cron = command(["crontab", "-u", "ubuntu", "-l"])
        checks.check("scripts/run-backup.py" in cron, "auth backup cron is installed")
    except RuntimeError:
        checks.check(False, "auth backup cron is installed")

    if checks.failures:
        print(f"NOT READY: {len(checks.failures)} check(s) failed")
        return 1
    print("READY: pre-cutover production state is complete and safely gated")
    return 0


if __name__ == "__main__":
    sys.exit(main())
