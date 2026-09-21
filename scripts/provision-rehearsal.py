#!/usr/bin/env python3
"""Prepare an isolated, read-only-source rehearsal of the auth row copy."""

from __future__ import annotations

import os
from pathlib import Path
import secrets
import subprocess
import sys


SOURCE_TABLES = (
    "users", "totp_secrets", "recovery_codes", "passkeys",
    "login_codes", "sessions", "blocks",
)
DATABASE = "taskly_auth_rehearsal"
DB_USER = "taskly_auth_rehearsal"
MYSQL = [
    "docker", "exec", "-i", "infra-db-1", "sh", "-lc",
    'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" --batch --skip-column-names "$@"',
    "sh",
]


def mysql(sql: str) -> str:
    result = subprocess.run(MYSQL, input=sql, text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError("MySQL rehearsal setup failed")
    return result.stdout.strip()


def main() -> None:
    os.umask(0o077)
    env_path = Path(".env.rehearsal")
    if env_path.exists():
        raise RuntimeError("rehearsal environment already exists")
    if mysql("SHOW DATABASES LIKE 'taskly_auth_rehearsal';"):
        raise RuntimeError("rehearsal database already exists")
    if mysql("SELECT User FROM mysql.user WHERE User='taskly_auth_rehearsal';"):
        raise RuntimeError("rehearsal database user already exists")

    engines = mysql(
        "SELECT table_name,engine FROM information_schema.tables "
        "WHERE table_schema='taskly' AND table_name IN ("
        + ",".join("'" + table + "'" for table in SOURCE_TABLES)
        + ");"
    )
    observed = dict(line.split("\t", 1) for line in engines.splitlines())
    if set(observed) != set(SOURCE_TABLES) or any(engine != "InnoDB" for engine in observed.values()):
        raise RuntimeError("source tables must all exist and use InnoDB")

    password = secrets.token_hex(32)
    source = f"{DB_USER}:{password}@tcp(infra-db-1:3306)/taskly?parseTime=false&charset=utf8mb4&loc=UTC"
    target = f"{DB_USER}:{password}@tcp(infra-db-1:3306)/{DATABASE}?parseTime=false&charset=utf8mb4&loc=UTC"
    current = dict(
        line.split("=", 1)
        for line in Path(".env.production").read_text(encoding="utf-8").splitlines()
        if "=" in line
    )
    webhook = current.get("DISCORD_WEBHOOK_URL", "")
    if "\n" in webhook or "\r" in webhook:
        raise RuntimeError("Discord URL contains a newline")
    with env_path.open("x", encoding="utf-8") as handle:
        os.chmod(env_path, 0o600)
        handle.write(f"SOURCE_DSN={source}\nTARGET_DSN={target}\n")
        handle.write("AUTH_MIGRATION_MODE=snapshot\n")
        handle.write(f"DISCORD_WEBHOOK_URL={webhook}\n")

    mysql(f"CREATE DATABASE `{DATABASE}` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;")
    grants = [
        f"CREATE USER '{DB_USER}'@'%' IDENTIFIED BY '{password}';",
        f"GRANT ALL PRIVILEGES ON `{DATABASE}`.* TO '{DB_USER}'@'%';",
    ]
    grants.extend(
        f"GRANT SELECT ON `taskly`.`{table}` TO '{DB_USER}'@'%';"
        for table in SOURCE_TABLES
    )
    mysql("\n".join(grants))
    print("empty rehearsal database and limited user created")
    print("source identity tables remain writable and unchanged")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError) as error:
        print(f"rehearsal setup stopped: {error}", file=sys.stderr)
        sys.exit(1)
