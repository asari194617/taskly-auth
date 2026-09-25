#!/usr/bin/env python3
"""Provision a read-only auth DB backup account and a private Docker env file.

Run once on the Taskly production host from /home/ubuntu/taskly-auth. The
existing Airflow container supplies the age recipient and FTPS credentials;
this file adds only the auth database account and an isolated backup path.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
import secrets
import subprocess
import sys


DATABASE = "taskly_auth"
USER = "taskly_auth_backup"
AIRFLOW = "taskly-airflow-scheduler"
ENV_FILE = Path(".env.backup")
MYSQL = [
    "docker", "exec", "-i", "infra-db-1", "sh", "-lc",
    'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" --batch --skip-column-names "$@"',
    "sh",
]


def mysql(statement: str) -> str:
    result = subprocess.run(MYSQL, input=statement, text=True, capture_output=True, check=False)
    if result.returncode:
        raise RuntimeError("MySQL setup failed; inspect protected server logs")
    return result.stdout.strip()


def airflow_environment() -> dict[str, str]:
    result = subprocess.run(
        ["docker", "inspect", AIRFLOW], text=True, capture_output=True, check=True
    )
    entries = json.loads(result.stdout)[0]["Config"]["Env"]
    values = dict(entry.split("=", 1) for entry in entries if "=" in entry)
    required = ("BACKUP_AGE_RECIPIENT", "FTP_HOST", "FTP_USER", "FTP_PASSWORD")
    if any(not values.get(key) for key in required):
        raise RuntimeError("existing encrypted FTPS backup configuration is incomplete")
    return values


def main() -> None:
    os.umask(0o077)
    if ENV_FILE.exists():
        raise RuntimeError("backup environment already exists; refusing to overwrite")
    if not mysql(f"SHOW DATABASES LIKE '{DATABASE}';"):
        raise RuntimeError("auth database does not exist")
    if mysql(f"SELECT User FROM mysql.user WHERE User='{USER}';"):
        raise RuntimeError("backup database user already exists; refusing to overwrite")
    airflow = airflow_environment()
    version = subprocess.run(
        ["docker", "exec", AIRFLOW, "mysqldump", "--version"],
        text=True, capture_output=True, check=True,
    ).stdout
    if "Ver 8.4." not in version:
        raise RuntimeError("Airflow must use MySQL 8.4 mysqldump")
    ftp_parent = airflow.get("FTP_DIR", "/backups").rstrip("/")
    if not ftp_parent.startswith("/") or "\n" in ftp_parent or "\r" in ftp_parent:
        raise RuntimeError("invalid existing FTPS directory")

    password = secrets.token_hex(32)
    values = {
        "MYSQL_USER": USER,
        "MYSQL_PASSWORD": password,
        "MYSQL_DATABASE": DATABASE,
        "DB_HOST": "db",
        "DB_PORT": "3306",
        "BACKUP_DIR": "/opt/airflow/backups/taskly-auth",
        "FTP_DIR": ftp_parent + "/taskly-auth",
    }
    temporary = ENV_FILE.with_suffix(".backup.tmp")
    with temporary.open("x", encoding="utf-8") as handle:
        os.chmod(temporary, 0o600)
        for key, value in values.items():
            handle.write(f"{key}={value}\n")
    try:
        mysql(
            f"CREATE USER '{USER}'@'%' IDENTIFIED BY '{password}';\n"
            f"GRANT SELECT, SHOW VIEW, TRIGGER, EVENT ON `{DATABASE}`.* TO '{USER}'@'%';"
        )
        temporary.replace(ENV_FILE)
    except Exception:
        temporary.unlink(missing_ok=True)
        raise
    print("auth backup account and mode 0600 environment created")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, subprocess.CalledProcessError, OSError, ValueError) as error:
        print(f"backup provisioning stopped: {error}", file=sys.stderr)
        sys.exit(1)
