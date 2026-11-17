# TOTP management

Settings mutations follow [security step-up](security-step-up.md). The recent-login
conditions below apply when `AUTH_SECURITY_STEP_UP_REQUIRED=0`. With `1`, a
verification completed within the last five minutes in the same active session
replaces the ten-minute login-age requirement. Operation-specific TOTP code
checks still apply.

TOTP secrets are authoritative in the auth database. Existing encrypted
`totp_secrets` rows and `TOTP_ENC_KEY` are copied without conversion so an
existing authenticator continues to produce valid codes.

- `GET /auth/totp/status` requires an authenticated auth session and returns
  whether an active secret exists.
- `POST /auth/totp/setup` requires an auth session created in the last ten
  minutes. It creates a random secret, stores only its encrypted value in a
  setup row that expires after ten minutes, and returns the secret and
  `otpauth` URI once to the UI.
- `POST /auth/totp/activate` requires the same recent session and a valid
  six-digit code for the pending setup. It replaces the user's encrypted
  secret in one transaction, consumes the setup, and invalidates pending OAuth
  codes, OAuth tokens, and login challenges.
- `POST /auth/totp/disable` requires a recent session and a valid code from the
  active secret. It refuses to remove the last available login method and
  invalidates pending OAuth codes, OAuth tokens, and login challenges.
- All endpoints require the exact auth UI origin and `X-Auth-UI: 1`. Mutation
  endpoints use the shared request rate limit, and storage failures send a
  redacted Discord alert. Secrets and codes are never logged or included in
  alerts.

During cutover, Taskly Web uses
`PUBLIC_AUTH_TOTP_URL=https://auth.taskly.iy0306.com/security`. At the same
time, every legacy API instance sets `AUTH_TOTP_EXTERNAL=1`. The gate rejects
legacy REST and GraphQL TOTP writes, removes TOTP from legacy method discovery,
and rejects legacy TOTP login attempts. Both flags remain disabled until the
final data copy and rollback rehearsal have succeeded.

The legacy login and auth login must never consume TOTP codes from separate
database copies after ownership changes. Rollback therefore requires pausing
writes, reconciling the active/encrypted row back to the legacy database, and
verifying a real code before enabling the old TOTP path again.

ログイン、追加確認、有効化、無効化は同じ利用者と TOTP シークレットのコード消費記録を共有する。並行した要求でも同じコードは一度だけ受け付ける。追加確認でコードを使った直後に無効化する場合は次のタイムステップのコードを待つ。新しいシークレットの有効化コードも消費されるため、同じコードで直後にログインできない。
