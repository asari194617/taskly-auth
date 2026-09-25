from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).parent))

from check_release_readiness import AUTH_FLAGS, validate_prepared_env


def valid_envs() -> tuple[dict[str, str], dict[str, str]]:
    shared = "s" * 32
    token = "t" * 32
    auth = {
        "AUTH_DSN": "auth:secret@tcp(db:3306)/taskly_auth?parseTime=true&charset=utf8mb4&loc=UTC",
        "AUTH_ISSUER": "https://taskly-auth-api.iy0306.com",
        "AUTH_UI_URL": "https://auth.taskly.iy0306.com",
        "WEBAUTHN_RP_ID": "taskly.iy0306.com",
        "OAUTH_CLIENT_ID": "taskly-web",
        "OAUTH_CLIENT_SECRET": shared,
        "OAUTH_REDIRECT_URI": "https://api.taskly.iy0306.com/auth/oauth/callback",
        "AUTH_INTERNAL_TOKEN": token,
    }
    taskly = {
        "AUTH_ISSUER": auth["AUTH_ISSUER"],
        "AUTH_CLIENT_ID": auth["OAUTH_CLIENT_ID"],
        "AUTH_CLIENT_SECRET": shared,
        "AUTH_REDIRECT_URI": auth["OAUTH_REDIRECT_URI"],
        "AUTH_INTERNAL_TOKEN": token,
        "WEB_PUBLIC_URL": "https://taskly.iy0306.com",
        "AUTH_BLOCK_SYNC_URL": "http://auth:3067",
        "AUTH_LIFECYCLE_SYNC_URL": "http://auth:3067",
        "AUTH_USER_SYNC_URL": "http://auth:3067",
        "AUTH_WRITES_PAUSED": "0",
        **{key: "0" for key in AUTH_FLAGS},
    }
    return auth, taskly


class PreparedEnvironmentTest(unittest.TestCase):
    def test_accepts_consistent_dormant_configuration(self) -> None:
        auth, taskly = valid_envs()
        self.assertEqual(validate_prepared_env(auth, taskly), [])

    def test_rejects_secret_mismatch_and_enabled_flag(self) -> None:
        auth, taskly = valid_envs()
        taskly["AUTH_CLIENT_SECRET"] = "different" * 4
        taskly[AUTH_FLAGS[0]] = "1"
        failures = validate_prepared_env(auth, taskly)
        self.assertIn("matching OAuth client secrets", failures)
        self.assertIn(f"Taskly {AUTH_FLAGS[0]}=0", failures)

    def test_rejects_datetime_dsn_without_explicit_parameters(self) -> None:
        auth, taskly = valid_envs()
        auth["AUTH_DSN"] = "auth:secret@tcp(db:3306)/taskly_auth"
        failures = validate_prepared_env(auth, taskly)
        self.assertIn("auth DSN parseTime=true", failures)
        self.assertIn("auth DSN loc=UTC", failures)
        self.assertIn("auth DSN charset=utf8mb4", failures)


if __name__ == "__main__":
    unittest.main()
