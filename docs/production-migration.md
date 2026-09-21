# 本番認証 DB 移行手順

この文書は 2026-09-21 の本番構成に基づきます。Taskly は `infra-db-1` (MySQL 8.4) を使い、認証 API を配置するホストは `133.242.49.12` です。ホストの空きメモリは限られるため、認証 DB は同じ MySQL インスタンス上の別 database として作成し、専用ユーザーと権限で分離します。別 MySQL インスタンスにする場合は容量計画と移行手順を改めて確認します。

## DNS

Cloudflare の DNS で以下を設定します。既存の Taskly ホストは Cloudflare proxy を使用しています。

| Type | Name | Content | Proxy status | TTL |
| --- | --- | --- | --- | --- |
| A | `taskly-auth` | `133.242.49.12` | Proxied | Auto |
| A | `taskly-auth-api` | `133.242.49.12` | Proxied | Auto |

両ホストに Nginx の vhost と有効な TLS 証明書が必要です。UI は `127.0.0.1:3068`、API は `127.0.0.1:3067` に proxy します。HTTP → HTTPS リダイレクトと Cloudflare Full (strict) を設定します。`deploy/nginx/` に vhost のひな型があります。DNS と証明書が整うまで本番 OAuth redirect は有効にしません。

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

## 切り替えのゲート

切り替え前に以下を実証します。

- ユーザー ID、件数、ハッシュ、パスワード認証、メールコード、TOTP、復旧コード、ブロック判定を照合する。
- 旧 RP ID のパスキー利用者がログインでき、対象ユーザーが新 RP ID に再登録できる。
- 旧 Taskly アプリのユーザー・パスワード・TOTP・復旧コード・パスキー更新経路を auth DB に移管する。旧 DB と新 DB の書き込み競合を解消する。
- 既存の API キー、権限、ユーザー参照、既存セッションを確認する。
- 新しい FQDN の TLS、OAuth redirect、開発環境、Discord 異常通知、切り戻しを確認する。

いずれかが未達なら、本番のログイン経路を切り替えず、旧 DB の認証行も削除しません。

## ロールバック

Taskly Web の OAuth リンクを非表示にし、旧ログインを維持します。新認証 DB 側で行われたパスワード変更、復旧コード消費、TOTP 設定、メールログイン設定などがあれば、旧 DB に安全に反映してから旧方式を正本に戻します。source と target のバックアップを保持し、単純な上書き復元で新しい更新を失わないようにします。
