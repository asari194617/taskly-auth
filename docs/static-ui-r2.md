# 認証 UI の静的配信仕様

## 目的

`auth.taskly.iy0306.com` の認証 UI を Vite で構築し、Cloudflare R2 のカスタムドメインから配信する。認証 API は既存の `https://taskly-auth-api.iy0306.com` と Go サービスを維持する。UI のビルドと R2 への反映は GitHub Actions が行う。

UI と API を別ホストにしても、次の値は変更しない。

- UI origin: `https://auth.taskly.iy0306.com`
- API issuer: `https://taskly-auth-api.iy0306.com`
- WebAuthn RP ID: `taskly.iy0306.com`
- OAuth redirect URI: `https://api.taskly.iy0306.com/auth/oauth/callback`

UI origin と WebAuthn RP ID を維持するため、既存パスキーの登録情報をホスト移行だけを理由に変更しない。実クレデンシャルによる端末試験は本番切り替えの必須条件とする。

## 構成

```text
browser
  |-- https://auth.taskly.iy0306.com/*
  |      Cloudflare -> private R2 bucket (static Vite build)
  |
  `-- https://taskly-auth-api.iy0306.com/*
         Cloudflare/Nginx -> Go auth API :3070
```

Go サービスは API のみを提供する。UI のソースは `apps/web` を正本とし、Go の HTML template と JavaScript は廃止する。開発環境では Vite dev server を `http://localhost:3068` で起動し、API は従来どおり `http://localhost:3067` を使う。

ブラウザーから API へ送るすべての認証リクエストは次を満たす。

- `credentials: include` を指定する。
- `X-Auth-UI: 1` を付ける。
- API の CORS は設定済みの `AUTH_UI_URL` と完全一致する origin だけを許可する。
- OAuth client secret、内部 API token、DB 資格情報を UI の環境変数や成果物へ含めない。

## ページとオブジェクトキー

R2 では要求パスと同じオブジェクトキーを返せるよう、ビルド後に HTML の拡張子なしコピーを生成する。

| URL | R2 object key | 用途 |
| --- | --- | --- |
| `/login` | `login` | メールアドレス入力 |
| `/password` | `password` | パスワード入力・別の認証方法の選択 |
| `/mail-code` | `mail-code` | メールコード入力 |
| `/mfa` | `mfa` | パスワード後の2段階認証・認証設定の追加確認 |
| `/profile` | `profile` | 表示名とメールアドレス |
| `/security` | `security` | パスワード、メールコード、復旧コード、TOTP、パスキー管理（`?section=passkeys` を含む） |
| `/login-history` | `login-history` | ログイン履歴 |
| `/consent` | `consent` | ChatGPT 連携の権限確認と許可・拒否 |
| `/passkeys` | `passkeys` | 旧リンクから `/security?section=passkeys` への互換転送 |
| `/logout` | `logout` | 認証サービスのセッション失効 |
| `/assets/*` | `assets/*` | ハッシュ付き JavaScript と CSS |

`/` は Cloudflare の Single Redirect で `https://auth.taskly.iy0306.com/login` へ 302 リダイレクトする。R2 固有の index document や rewrite 動作には依存しない。アプリと OAuth API が生成する URL も `/login` などの明示的なパスを使う。

## ビルド

API の送信先と Sentry の公開ビルド設定は次のとおり。

```dotenv
VITE_AUTH_API_URL=http://localhost:3067
VITE_SENTRY_DSN=
VITE_SENTRY_ENVIRONMENT=development
VITE_SENTRY_RELEASE=
```

本番 Actions は `VITE_AUTH_API_URL=https://taskly-auth-api.iy0306.com`、`VITE_SENTRY_ENVIRONMENT=production`、`VITE_SENTRY_RELEASE=taskly-auth-ui@<commit SHA>` を指定する。`production-ui` environment の variable `VITE_SENTRY_DSN` に既存 Taskly プロジェクトの**公開 DSN**を設定する。空の場合、本番デプロイは停止する。DSN の secret key や Sentry auth token は設定しない。`VITE_` で始まる値はブラウザーへ公開されるため、secret を指定してはいけない。ローカル・PR 検証は DSN 未設定で通知を停止できる。

Sentry に送る認証 UI イベントの内容と確認方法は [認証 UI のエラー監視](auth-ui-error-monitoring.md)を参照する。

ローカルではリポジトリのルートから次を実行する。

```sh
npm --prefix apps/web ci
npm --prefix apps/web run dev
```

本番相当の成果物は次で生成し、`apps/web/dist` を配信対象とする。

```sh
VITE_AUTH_API_URL=https://taskly-auth-api.iy0306.com npm --prefix apps/web run build
```

ビルド検査では、上記9ページの拡張子なしオブジェクト、ハッシュ付きアセット、API URL が揃い、secret に見える環境変数が参照されていないことを確認する。

## R2 と Cloudflare の初回設定

1. `auth.taskly.iy0306.com` を管理している Cloudflare account に非公開 bucket `taskly-auth-ui` を作成する。
2. bucket の公開開発用 `r2.dev` URL は無効のままにする。
3. bucket の Settings > Custom Domains で `auth.taskly.iy0306.com` を接続する。Cloudflare が管理する DNS レコードへ切り替わるため、現在 `133.242.49.12` を向いている同名の A レコードはこの操作時に置き換える。
4. `/` から `/login` への 302 Single Redirect を作る。式は host が `auth.taskly.iy0306.com` かつ path が `/` の場合だけに限定する。
5. `taskly-auth-api.iy0306.com` の DNS、Nginx、証明書は変更しない。

カスタムドメイン接続は account と DNS への広い権限を必要とする一度限りの設定である。日常の Actions は対象 bucket の Object Read & Write だけを持つ R2 API token を使い、DNS を変更しない。

## GitHub Actions デプロイ

Repository environment `production-ui` に次を登録する。

| 種別 | 名前 | 内容 |
| --- | --- | --- |
| secret | `R2_ACCOUNT_ID` | Cloudflare account ID |
| secret | `R2_ACCESS_KEY_ID` | 対象 bucket 限定 R2 access key ID |
| secret | `R2_SECRET_ACCESS_KEY` | 対応する secret access key |
| variable | `R2_BUCKET` | `taskly-auth-ui` |

`deploy-ui-r2.yml` は `main` への UI 関連変更の push と手動実行で、次の順に処理する。

1. lockfile を使って依存を再現する。
2. UI の test と build を実行する。
3. S3 互換 endpoint `https://<ACCOUNT_ID>.r2.cloudflarestorage.com` に成果物を upload する。
4. `assets/` を先に upload し、その後 HTML と拡張子なしページを upload する。
5. upload がすべて成功した後、成果物にない古い object を削除する。

同じ environment へのデプロイは concurrency group で直列化する。ページは `Cache-Control: no-cache` と `Content-Type: text/html; charset=utf-8`、ハッシュ付きアセットは `Cache-Control: public, max-age=31536000, immutable` と適切な MIME type を設定する。

Pull Request では UI の test と build までを実行し、R2 へは書き込まない。production environment の secret は Pull Request workflow へ渡さない。

### 初回デプロイ実績 (2026-09-25)

- 非公開 bucket `taskly-auth-ui` を APAC / Standard で作成し、公開開発用 `r2.dev` URL は無効のままとした。
- bucket 限定の Object Read & Write credential と `R2_BUCKET=taskly-auth-ui` を GitHub の `production-ui` environment に登録した。
- PR #1 を merge commit `7832e2b4f9859674c71f3789ef3775df78df7875` で main に反映した。
- main の build workflow `36096399918` と R2 deploy workflow `36096400056` は成功した。
- `auth.taskly.iy0306.com` を R2 custom domain として接続し、Cloudflare 上で Active を確認した。従来の同名 A レコードは Cloudflare 管理の R2 向けレコードへ置き換わった。
- Single Redirect `taskly-auth-ui-root` と Response Header Transform Rule `taskly-auth-ui-security-headers` を有効化した。

公開 URL の実測では `/` が `/login` へ 302、`/login`、`/security`、`/passkeys`、`/logout` が HTTPS 200 を返した。HTML は `Content-Type: text/html; charset=utf-8` と `Cache-Control: no-cache`、ハッシュ付き JavaScript/CSS は正しい MIME type と `Cache-Control: public, max-age=31536000, immutable` を返した。5種類のセキュリティヘッダーも全経路で確認した。ブラウザーで `/login` を開き、CSP 違反を含む console warning/error がないことを確認した。

初回 UI デプロイ時点では、認証 API の `/.well-known/oauth-authorization-server` は 503 と `Retry-After: 3600` を返していた。その後 API を公開用 Compose に接続し、health と OAuth メタデータは200、認証・OAuth 操作は503となった（[本番移行手順](production-migration.md)参照）。静的 UI の公開では認証 DB のコピー、移行フラグ、Taskly 側 OAuth 設定を変更していない。

## セキュリティヘッダー

R2 の custom domain に Cloudflare Response Header Transform Rules を設定する。

```text
Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; connect-src https://taskly-auth-api.iy0306.com https://o4510197265989632.ingest.us.sentry.io; img-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
Referrer-Policy: no-referrer
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Permissions-Policy: camera=(), geolocation=(), microphone=()
```

HTML 内に inline script と inline style を置かず、この CSP で動作させる。開発サーバーでは Vite の実行に必要な設定を使い、本番ヘッダーの確認は custom domain で行う。

## 切り替えと確認

R2 への初回デプロイだけでは利用者を切り替えない。次を順に確認する。

1. Actions の build と upload が成功する。
2. R2 custom domain を接続し、`/login`、`/security`、`/passkeys`、`/logout` が HTTPS 200 になる。
3. 応答ヘッダー、MIME type、キャッシュ方針、API の CORS preflight を確認する。
4. 隔離済みの1ユーザー・1パスキー DB と公開 API を一時接続し、既存パスキーでログインする。
5. 試験後は公開 API を 503 ゲートへ戻し、隔離 DB の行数が変わっていないことを確認する。
6. 認証データの書き込み所有権、最終コピー、照合、バックアップ復元の全ゲートが揃ってから `AUTH_MIGRATION_READY=1` と Taskly 側の OAuth 設定を有効にする。

UI の配信先変更は認証 DB のコピーや切り替えを実行しない。既存ユーザーを保持し、旧ログインを正本とした状態で進める。

## ロールバック

UI だけに問題がある場合は、Actions の手動実行で正常だった commit を checkout して同じ bucket へ再デプロイする。API または認証データに問題がある場合は API の 503 ゲートを戻し、Taskly の OAuth リンクを無効にして旧ログインを維持する。R2 bucket や旧 DB のデータを削除しない。
