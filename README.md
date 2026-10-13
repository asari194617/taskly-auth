# taskly-auth

Taskly の独立した認証サービスです。認証 UI、認証 API、OAuth 認可サーバー、既存の認証データを移すツールを管理します。

- **認証 UI**: Vite + React。開発では Vite、本番では Cloudflare R2 から配信します。
- **認証 API**: Go + MySQL。パスワード、メールコード、パスキー、TOTP、復旧コードによるログインと認証設定を提供します。
- **OAuth**: Authorization Code + PKCE S256。認可コードとトークンはハッシュで保存します。
- **移行**: `cmd/migrate-auth` で既存ユーザー ID と認証情報を保持してコピー・照合します。

## 構成

| パス | 内容 |
| --- | --- |
| `apps/web/` | 認証 UI と R2 用のビルド・配信スクリプト |
| `cmd/auth/` | 認証・OAuth・内部同期 API |
| `cmd/migrate-auth/` | 認証データのコピー・読み取り専用照合 |
| `cmd/seed-dev/` | 開発専用ユーザーの作成・パスワード設定 |
| `schema.sql` | 開発 DB の初期スキーマ |
| `scripts/` | スモークテスト、本番準備・バックアップ・移行前検査 |
| `deploy/nginx/` | 本番 API の Nginx 設定 |
| `docs/` | 機能仕様と運用手順 |

## 開発環境

必要なものは Docker Compose、Go（バージョンは `go.mod`）、Node.js 22.12 以上、npm、Python 3 です。以下はリポジトリのルートで実行します。

### 1. 設定と起動

```sh
cp .env.example .env
```

`.env` の `OAUTH_CLIENT_SECRET`、`AUTH_INTERNAL_TOKEN`、`TOTP_ENC_KEY` を開発専用の値へ変更します。内部 token は OAuth client secret と別の値を使います。移行済みデータを扱う環境では、TOTP 鍵を作り直さず既存の鍵を維持します。

単独で開発する場合、`TASKLY_PROFILE_SYNC_URL=` を空にして Taskly へのプロフィール同期を無効にします。同期も検証する場合は、到達可能な Taskly API の URL と両サービスで一致する `AUTH_INTERNAL_TOKEN` を設定してください。メール送信には SMTP 設定と Taskly の `email-compose` サービスが別途必要です。詳細は[認証メール](docs/email-compose.md)を参照してください。

```sh
docker compose up -d --build --wait
```

| サービス | 接続先 |
| --- | --- |
| UI | `http://localhost:3068/login` |
| API | `http://localhost:3067` |
| MySQL | `127.0.0.1:3307` / database `taskly_auth` |

Compose は `.env` をコンテナへ渡します。ホスト上の `go run` や Python スクリプトには必要な変数を別途渡します。

### 2. 開発ユーザーを用意する

```sh
AUTH_DSN='auth:auth_dev_password@tcp(127.0.0.1:3307)/taskly_auth?parseTime=true&charset=utf8mb4&loc=UTC' \
AUTH_DEV_EMAIL='dev@example.test' \
AUTH_DEV_PASSWORD='<development-password>' \
go run ./cmd/seed-dev
```

DB パスワードを変更した場合は DSN も合わせます。`seed-dev` は同じメールのユーザーが存在するとパスワードを上書きし、メールコードを無効にします。`NODE_ENV=production` では実行できません。

### 3. Taskly と OAuth で接続する

Taskly API 側の環境設定に次を指定します。

```dotenv
AUTH_ISSUER=http://localhost:3067
AUTH_CLIENT_ID=taskly-web
AUTH_CLIENT_SECRET=<taskly-authのOAUTH_CLIENT_SECRETと同じ値>
AUTH_REDIRECT_URI=http://localhost:3063/auth/oauth/callback
WEB_PUBLIC_URL=http://localhost:4321
```

Taskly Web のビルド変数は次のとおりです。

```dotenv
PUBLIC_AUTH_OAUTH_START_URL=http://localhost:3063/auth/oauth/start
PUBLIC_AUTH_LOGOUT_URL=http://localhost:3068/logout
```

### 4. 検証する

```sh
go test ./...
go vet ./...
python3 -m unittest scripts/test_check_release_readiness.py
npm --prefix apps/web ci
npm --prefix apps/web run typecheck
npm --prefix apps/web test
npm --prefix apps/web run build
```

MySQL を使うスモークテストは起動済みの開発環境で実行します。`.env` の値に合わせて変数を渡してください。

```sh
OAUTH_CLIENT_SECRET='<development-client-secret>' \
AUTH_INTERNAL_TOKEN='<development-internal-token>' \
AUTH_DEV_EMAIL='dev@example.test' \
AUTH_DEV_PASSWORD='<development-password>' \
python3 scripts/smoke-dev.py
```

このスモークは認証情報の変更、ブロック、退会・消去まで実行し、指定した開発ユーザーを削除します。専用の使い捨て DB・ユーザーで実行し、再実行前には `seed-dev` でユーザーを作り直してください。public client も検証する場合は、下記の登録に加え `OAUTH_DEV_PUBLIC_CLIENT_ID` と `OAUTH_DEV_PUBLIC_REDIRECT_URI` を渡します。CI の実行条件は [build.yml](.github/workflows/build.yml) を参照してください。

## OAuth public client

`OAUTH_PUBLIC_CLIENTS_JSON` にクライアントを登録します。

```json
[{"client_id":"taskly-dev","redirect_uri":"http://127.0.0.1/callback"}]
```

Authorization Code + PKCE S256 が必須です。token endpoint には `client_id` を送り、client secret は送りません。HTTPS の app-claimed URI、逆ドメイン形式の private-use scheme、loopback IP の HTTP URI を受け付けます。loopback IP は登録値と同じホスト・パス・クエリで任意の有効なポートを使えます。

native アプリの実装はこのリポジトリにありません。本番クライアントは実際の redirect URI と端末でのコールバック検証が完了してから登録します。

## 本番構成と切り替え

| 用途 | URL / 設定 |
| --- | --- |
| UI（R2） | `https://auth.taskly.iy0306.com` |
| API / OAuth issuer | `https://taskly-auth-api.iy0306.com` |
| WebAuthn RP ID | `taskly.iy0306.com`（旧 Taskly と同じ値） |
| OAuth callback | `https://api.taskly.iy0306.com/auth/oauth/callback` |

`compose.yaml` は独立 MySQL を持つ開発用です。本番は既存 MySQL の別 database `taskly_auth` と専用 DB ユーザーを使います。

- `compose.stage.yaml`: 内部 Docker ネットワークで検証します。ホストのポートは公開せず、`AUTH_MIGRATION_READY=0` に固定します。
- `compose.prod.yaml`: API を `127.0.0.1:3070` で Nginx に接続します。UI は R2 で配信し、Go イメージは API のみを提供します。

両 Compose は `.env.production` からサービス設定を読みます。本番 Compose の `AUTH_IMAGE_TAG`、`AUTH_MIGRATION_READY`、`AUTH_SECURITY_STEP_UP_REQUIRED` は Compose 用 `.env` またはシェル環境で指定します。本番モードでは `AUTH_MIGRATION_READY=1` を明示するまで認証・OAuth・内部同期 API は503となり、health と OAuth メタデータは利用できます。

### 検証済み事項と残る条件

記録済みの検証では、R2 UI の公開、API の health・OAuth メタデータ、移行ゲート、コピー・照合・バックアップ復元を確認しています。2026-09-25 には隔離 DB の既存パスキーで新 UI origin からの実端末ログインも成功しました。これらは本番ユーザーの最終コピーやログイン切り替えの完了を意味しません。

旧認証情報の更新・消費経路が残る間は DB 間に差分が生じます。`migrate-auth` の `verify` は読み取り専用の検出であり、同期は行いません。書き込み所有権の移管、書き込み停止中の最終コピー・照合、切り戻しと復元後の再適用、連携ログアウトを[本番移行手順](docs/production-migration.md)に従って確認してから切り替えます。過去の検証結果と現在の稼働状態は区別し、本番作業前に同文書の移行前検査を再実行してください。

認証設定の変更に対する追加確認は `AUTH_SECURITY_STEP_UP_REQUIRED=1` で有効になります。既定値 `0` はログインから10分以内の条件、`1` は同じセッションでの追加確認から5分以内の条件です。認証アプリが有効なら TOTP または未使用の復旧コード、未設定なら登録メールへの専用コードで確認します。表示名変更とメールアドレス確認は対象外です。UI と API を配備してから有効化します。

## ドキュメント

| 分野 | 文書 |
| --- | --- |
| 本番運用 | [移行・検証・バックアップ・切り戻し](docs/production-migration.md)、[UI の R2 配信](docs/static-ui-r2.md) |
| アカウント | [プロフィールの所有権](docs/account-profile-ownership.md)、[ユーザー作成・同期](docs/user-provisioning-sync.md)、[退会・消去](docs/account-lifecycle-sync.md)、[ブロック同期](docs/block-sync.md) |
| 認証設定 | [追加確認](docs/security-step-up.md)、[パスワード](docs/password-management.md)、[メールコード](docs/email-code-settings.md)、[パスキー](docs/passkey-management.md)、[TOTP](docs/totp-management.md)、[復旧コード](docs/recovery-code-management.md) |
| メール・履歴 | [認証メール](docs/email-compose.md)、[ログイン履歴・CSV・IP と位置の信頼境界](docs/login-history.md) |
