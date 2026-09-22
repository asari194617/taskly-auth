# taskly-auth

Taskly 用の独立した認証 UI、認証 API、OAuth 2.1 authorization server、および認証データ移行ツールです。OAuth は Authorization Code + PKCE S256 を使い、認可コードとトークンはハッシュで保存します。

## 開発環境

1. `cp .env.example .env` を実行し、開発専用の `OAUTH_CLIENT_SECRET` と `TOTP_ENC_KEY` を設定します。
2. `docker compose up -d --build` で独立した MySQL と認証サービスを起動します。UI は `http://localhost:3068`、API は `http://localhost:3067`、開発 DB は loopback の `3307` です。
3. 開発ユーザーが必要な場合だけ `AUTH_DSN='auth:...@tcp(127.0.0.1:3307)/taskly_auth?parseTime=true&loc=UTC' AUTH_DEV_EMAIL=... AUTH_DEV_PASSWORD=... go run ./cmd/seed-dev` を実行します。`seed-dev` は本番モードでは実行できません。
4. Taskly 側の `.env` に `AUTH_ISSUER=http://localhost:3067`、`AUTH_CLIENT_ID=taskly-web`、一致する `AUTH_CLIENT_SECRET`、`AUTH_REDIRECT_URI=http://localhost:3063/auth/oauth/callback`、`WEB_PUBLIC_URL=http://localhost:4321` を設定します。Web 側には `PUBLIC_AUTH_OAUTH_START_URL=http://localhost:3063/auth/oauth/start` と `PUBLIC_AUTH_LOGOUT_URL=http://localhost:3068/logout` を設定します。
5. `go test ./...`、`go vet ./...`、`AUTH_DEV_EMAIL=... AUTH_DEV_PASSWORD=... python3 scripts/smoke-dev.py` で確認します。

`compose.yaml` は開発用です。本番では `compose.prod.yaml` を使い、既存 MySQL 内の別 database `taskly_auth` と専用 DB ユーザーに接続します。認証データのコピーは `cmd/migrate-auth` が担当します。

本番ホストで切り替え前の疎通確認をする場合は `compose.stage.yaml` を使います。Docker の内部ネットワークだけに接続し、ホストのポートは公開しません。公開 Nginx は 503 を維持し、認証データの移行と切り替え条件を満たした後に `compose.prod.yaml` に移ります。
本番モードでは `AUTH_MIGRATION_READY=1` を明示するまで認証・OAuth API は 503 を返します。health と OAuth メタデータだけが使える状態です。

public client は `OAUTH_PUBLIC_CLIENTS_JSON` に `[{"client_id":"taskly-dev","redirect_uri":"http://127.0.0.1/callback"}]` のように登録します。Authorization Code + PKCE S256 を必須とし、token endpoint には `client_id` を送って client secret は送りません。HTTPS の app-claimed URI、逆ドメイン形式の private-use scheme、loopback IP の HTTP URIを受け付けます。loopback IP では登録値と同じホスト・パス・クエリで任意の有効なポートを使えます。本番用の client ID と redirect URI は実際のアプリが確定してから登録してください。リポジトリ内に native アプリ実装はありません。[RFC 8252](https://www.rfc-editor.org/rfc/rfc8252) の redirect URI と PKCE の要件に従います。

## 現在の展開制約

このリポジトリは段階導入用です。認証 UI の公開予定ホストは `auth.taskly.iy0306.com` で、旧パスキーの RP ID `taskly.iy0306.com` を維持できるサブドメインです。旧保存形式を読み取る WebAuthn ログインと新規登録・管理経路を追加しましたが、移行済みの実クレデンシャルでの端末検証は未完了です。また、プロフィール・パスワード・TOTP・復旧コード等の旧書き込み経路が残る間は二つの DB に差分が生じます。`migrate-auth` の `verify` モードは差分を読み取り専用で検出しますが、同期は行いません。Web ログアウトの連携は実装しましたが、公開環境では認証 UI 公開後に `PUBLIC_AUTH_LOGOUT_URL=https://auth.taskly.iy0306.com/logout` を設定する必要があります。native アプリとの実機接続も未完了です。パスキー認証の端末検証、認証情報更新の所有権移管、増分同期・切り戻し手順の検証が終わるまで、本番ログインの一斉切り替えを行わないでください。

設計と本番手順は [docs/production-migration.md](docs/production-migration.md)、パスキーの操作と認可条件は [docs/passkey-management.md](docs/passkey-management.md)、パスワード、メールコード、復旧コードの設定は [docs/password-management.md](docs/password-management.md)、[docs/email-code-settings.md](docs/email-code-settings.md)、[docs/recovery-code-management.md](docs/recovery-code-management.md) を参照してください。
