# 本番認証 DB 移行手順

この文書は 2026-09-21 の本番構成に基づきます。Taskly は `infra-db-1` (MySQL 8.4) を使い、認証 API を配置するホストは `133.242.49.12` です。ホストの空きメモリは限られるため、認証 DB は同じ MySQL インスタンス上の別 database として作成し、専用ユーザーと権限で分離します。別 MySQL インスタンスにする場合は容量計画と移行手順を改めて確認します。

## DNS

Cloudflare の DNS で以下を設定します。証明書発行時は DNS only とし、認証サービスの公開検証後に Cloudflare proxy を使う場合は Full (strict) にします。

| Type | Name | Content | Proxy status | TTL |
| --- | --- | --- | --- | --- |
| A | `taskly-auth` | `133.242.49.12` | DNS only | Auto |
| A | `taskly-auth-api` | `133.242.49.12` | DNS only | Auto |

両ホストの Nginx vhost は `deploy/nginx/` に保存します。UI は `127.0.0.1:3068`、API は `127.0.0.1:3067` に proxy し、HTTP は HTTPS にリダイレクトします。バックエンドが不在の間は 503 と `Retry-After` を返します。OAuth の移行ゲートが整うまで本番 OAuth redirect は有効にしません。

### 公開先の確認 (2026-09-21)

DNS 登録後、両ホストの A 応答は Cloudflare の proxy IP になり、公開 `/healthz` は HTTPS 200 (`ok`) を返しました。ただし公開 UI の `/login` と API の `/.well-known/oauth-authorization-server` は 404 (`Cannot GET ...`) です。さらに `133.242.49.12` へ直接接続した際の API ホスト名は TLS 証明書が一致せず、同ホストに認証サービスの Docker コンテナと Nginx vhost はありませんでした。**公開 `/healthz` の 200 だけでは認証サービスへの到達を証明できません。** Cloudflare の当該 DNS レコードの Content と、該当する Origin Rule、Worker Route、Redirect Rule を確認してください。Cloudflare proxy の応答から元の A レコードの Content は読み取れません。

その後ユーザーが両レコードを DNS only に変更しました。`1.1.1.1` と `8.8.8.8` から両ホストの A レコードが `133.242.49.12` を返すことを確認しました。初回の HTTP はオリジン Nginx の 404、HTTPS は証明書のホスト名不一致でした。

同日、両 Nginx vhost を本番ホストに配置し、既存の Certbot アカウントで両ホスト名を含む Let's Encrypt 証明書を発行しました。有効期限は 2026-12-20 です。`nginx -t`、HTTP → HTTPS の 301、両ホストの証明書検証、Certbot 更新の dry run が成功しました。HTTPS は現在 503 を返します。これは認証コンテナをまだ起動していないためです。証明書と入口は整いましたが、認証サービスの稼働と本番切り替えは未完了です。

認証コンテナの配置後、オリジンと Cloudflare 経由の双方で以下を確認します。証明書が一致しない場合は `-k` で検査を通さず、証明書と vhost を修正してから再試行します。

```sh
curl --resolve taskly-auth-api.iy0306.com:443:133.242.49.12 \
  https://taskly-auth-api.iy0306.com/.well-known/oauth-authorization-server
curl --resolve taskly-auth.iy0306.com:443:133.242.49.12 \
  https://taskly-auth.iy0306.com/login
curl https://taskly-auth-api.iy0306.com/.well-known/oauth-authorization-server
curl https://taskly-auth.iy0306.com/login
```

API は `issuer` が `https://taskly-auth-api.iy0306.com` の JSON、UI は認証画面を返すことを確認します。DNS レコードの Content は両方 `133.242.49.12` とし、別の Cloudflare ルートが割り込まないようにします。

本番 OAuth 設定値は `AUTH_ISSUER=https://taskly-auth-api.iy0306.com`、`AUTH_UI_URL=https://taskly-auth.iy0306.com`、`OAUTH_REDIRECT_URI=https://api.taskly.iy0306.com/auth/oauth/callback`、Taskly 側の `WEB_PUBLIC_URL=https://taskly.iy0306.com` です。Taskly 側の本番環境設定は現行のリポジトリ直下 `.env.production` を暗号化してデプロイする方式であり、`apps/server/.env.production` ではありません。

## 公開前のホスト内検証

`scripts/provision-stage.py` は本番ホストで一度だけ実行し、`taskly_auth` database、同 database にだけ権限を持つ専用ユーザー、mode 0600 の `.env.production` を作ります。`users` テーブルは元DBの構造だけを `CREATE TABLE LIKE` で複製します。既存 Taskly コンテナから TOTP 鍵、SMTP 設定、Discord webhook をプロセス内で引き継ぎ、値を標準出力や Git に出しません。既存の `taskly` database とユーザー行は変更せず、認証行はこの段階ではコピーしません。既存の認証 DB や環境ファイルがあれば実行を拒否します。

`compose.stage.yaml` は GitHub Actions で作成したコミット SHA 固定のイメージを内部 Docker ネットワークだけで動かします。手動実行した workflow は同じイメージを 3 日間の非公開 artifact として保存します。これを `gh run download`、SSH 転送、`docker load` で本番ホストに入れます。ホストのポートを公開しないため Nginx は 503 のままです。内部の DB 接続、OAuth メタデータ、Discord 通知を検証した後に限り、移行と書き込み所有権の切り替えに進みます。`compose.prod.yaml` に切り替える前に下記の全ゲートを満たす必要があります。

2026-09-21 に専用 DB と設定ファイルを作成し、内部ステージングコンテナの health check と OAuth メタデータを確認しました。新 DB の `users` は 0 行で、元 DB のユーザー行は変更していません。公開 FQDN は引き続き HTTPS 503 です。Discord webhook の実送信では既定の HTTP クライアント名が 403 となり、`TasklyAuth/1.0` を指定すると 204 で受理されました。通知コードにこのヘッダーを設定しています。

## 事前確認

1. 本番の暗号鍵 `TOTP_ENC_KEY`、SMTP 設定、Discord webhook を既存の暗号化された本番環境設定から認証サービスの `.env.production` に安全に移します。ログや Git に値を出しません。
2. `OAUTH_CLIENT_SECRET` と専用 DB パスワードを新規に作成します。Taskly API と認証サービスのクライアント秘密だけを一致させます。
3. source `taskly` と target `taskly_auth` をバックアップします。既存バックアップの復元テストを実施します。
4. 新 DB と専用ユーザーを作成します。`taskly_auth` への権限だけを付与します。Taskly の既存 DB ユーザーに認証 DB の権限を付与しません。
5. 既存ログインを維持した状態で、開発/ステージング環境でコピーと照合を完走します。

## コピー

`migrate-auth` は `users`, `totp_secrets`, `recovery_codes`, `passkeys`, `login_codes`, `sessions`, `blocks` を主キーごとコピーします。元行を更新・削除しません。移行先に 1 行でも存在すれば拒否します。各テーブルのコピー後と全体コピー後に件数と SHA-256 を比較します。

認証情報への書き込みを停止したことを確認してから、`SOURCE_DSN` と `TARGET_DSN` に異なる database の接続文字列を指定し、`AUTH_WRITES_PAUSED=1 migrate-auth` を実行します。このフラグはオペレーターによる停止確認であり、プログラムが書き込みを止めるものではありません。コピーと最終照合が終わるまで旧ログインを再開しません。失敗した場合は本番切り替えを行わず、バックアップから空の移行先を復元して原因を修正します。

既存の `sessions` は保存しますが、新 OAuth セッションとして自動的には引き継ぎません。Taskly API の既存セッションを旧 DB で TTL まで受け入れる期間を設けます。
既存の `login_codes` も保存しますが、新サービスは UTC の `auth_login_codes` を別に使用します。切り替え中に旧コードを入力したユーザーには新しいコードの再送を案内します。

## 切り替えのゲート

切り替え前に以下を実証します。

- ユーザー ID、件数、ハッシュ、パスワード認証、メールコード、TOTP、復旧コード、ブロック判定を照合する。
- 旧 RP ID のパスキー利用者がログインでき、対象ユーザーが新 RP ID に再登録できる。
- 旧 Taskly アプリのユーザー・パスワード・TOTP・復旧コード・パスキー更新経路を auth DB に移管する。旧 DB と新 DB の書き込み競合を解消する。
- native/public client の PKCE 接続と、Taskly セッション・認証サービスセッションの連携ログアウトを確認する。
- 既存の API キー、権限、ユーザー参照、既存セッションを確認する。
- 新しい FQDN の TLS、OAuth redirect、開発環境、Discord 異常通知、切り戻しを確認する。

いずれかが未達なら、本番のログイン経路を切り替えず、旧 DB の認証行も削除しません。

## ロールバック

Taskly Web の OAuth リンクを非表示にし、旧ログインを維持します。新認証 DB 側で行われたパスワード変更、復旧コード消費、TOTP 設定、メールログイン設定などがあれば、旧 DB に安全に反映してから旧方式を正本に戻します。source と target のバックアップを保持し、単純な上書き復元で新しい更新を失わないようにします。
