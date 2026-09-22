#!/usr/bin/env python3
"""Create an empty auth database and private staging configuration on Taskly's host.

Run in the deployment directory as the Docker-enabled ubuntu user. This never
reads or writes the existing taskly schema and refuses to overwrite an auth
database, DB user, or environment file. It does not copy production users.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
import secrets
import subprocess
import sys


DATABASE = "taskly_auth"
DB_USER = "taskly_auth"
MYSQL = [
    "docker", "exec", "-i", "infra-db-1", "sh", "-lc",
    'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" --batch --skip-column-names "$@"',
    "sh",
]
INHERITED = (
    "TOTP_ENC_KEY", "SMTP_HOST", "SMTP_PORT", "SMTP_USER",
    "SMTP_PASSWORD", "SMTP_FROM", "SMTP_TLS", "DISCORD_WEBHOOK_URL",
)


def mysql(sql: str, database: str | None = None) -> str:
    command = MYSQL.copy()
    if database is not None:
        command.extend(["-D", database])
    result = subprocess.run(
        command, input=sql, text=True, capture_output=True, check=False
    )
    if result.returncode:
        raise RuntimeError("MySQL command failed; inspect the server's protected logs")
    return result.stdout.strip()


def inherited_environment() -> dict[str, str]:
    result = subprocess.run(
        ["docker", "inspect", "infra-server-1"],
        text=True, capture_output=True, check=True,
    )
    items = json.loads(result.stdout)[0]["Config"]["Env"]
    current = dict(item.split("=", 1) for item in items if "=" in item)
    missing = [key for key in INHERITED if not current.get(key)]
    if missing:
        raise RuntimeError("missing existing settings: " + ", ".join(missing))
    return {key: current[key] for key in INHERITED}


def main() -> None:
    os.umask(0o077)
    env_path = Path(".env.production")
    if env_path.exists():
        raise RuntimeError("environment already exists; refusing to overwrite")
    subprocess.run(["docker", "network", "inspect", "infra_default"],
                   stdout=subprocess.DEVNULL, check=True)
    if mysql("SHOW DATABASES LIKE 'taskly_auth';"):
        raise RuntimeError("auth database already exists; refusing to overwrite")
    if mysql("SELECT User FROM mysql.user WHERE User='taskly_auth';"):
        raise RuntimeError("auth database user already exists; refusing to overwrite")

    inherited = inherited_environment()
    if inherited["SMTP_TLS"].lower() not in ("starttls", "smtps"):
        raise RuntimeError("production SMTP must use TLS")
    for key, value in inherited.items():
        if "\n" in value or "\r" in value:
            raise RuntimeError(key + " contains a newline")

    password = secrets.token_hex(32)
    client_secret = secrets.token_hex(32)
    variables = {
        "AUTH_DSN": f"{DB_USER}:{password}@tcp(infra-db-1:3306)/{DATABASE}?parseTime=true&charset=utf8mb4&loc=UTC",
        "AUTH_ISSUER": "https://taskly-auth-api.iy0306.com",
        "AUTH_UI_URL": "https://auth.taskly.iy0306.com",
        "WEBAUTHN_RP_ID": "taskly.iy0306.com",
        "OAUTH_CLIENT_ID": "taskly-web",
        "OAUTH_CLIENT_SECRET": client_secret,
        "OAUTH_REDIRECT_URI": "https://api.taskly.iy0306.com/auth/oauth/callback",
        "LEGACY_LOGIN_URL": "https://taskly.iy0306.com/login",
        **inherited,
    }

    # Keep secrets out of process arguments, terminal output, and source control.
    with env_path.open("x", encoding="utf-8") as handle:
        os.chmod(env_path, 0o600)
        for key, value in variables.items():
            handle.write(f"{key}={value}\n")

    mysql(f"CREATE DATABASE `{DATABASE}` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;")
    # Preserve the exact production column layout for the later row-by-row
    # migration and SHA-256 comparison. CREATE TABLE LIKE does not copy rows.
    mysql(f"CREATE TABLE `{DATABASE}`.`users` LIKE `taskly`.`users`;")
    mysql(
        f"CREATE USER '{DB_USER}'@'%' IDENTIFIED BY '{password}';\n"
        f"GRANT ALL PRIVILEGES ON `{DATABASE}`.* TO '{DB_USER}'@'%';"
    )
    print("empty auth database, dedicated DB user, and private environment created")
    print("existing Taskly data was not copied or changed")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, subprocess.CalledProcessError, OSError) as error:
        print(f"provisioning stopped: {error}", file=sys.stderr)
        sys.exit(1)
