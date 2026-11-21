# 本番認証 DB 移行手順

Taskly は `infra-db-1` (MySQL 8.4) を使い、認証 API を配置するホストは `133.242.49.12` です。ホストの空きメモリは限られるため、認証 DB は同じ MySQL インスタンス上の別 database として作成し、専用ユーザーと権限で分離します。別 MySQL インスタンスにする場合は容量計画と移行手順を改めて確認します。

## 認証 UI の新ホスト名と既存パスキー

認証 UI は `auth.taskly.iy0306.com`、認証 API は `taskly-auth-api.iy0306.com` を使う。従来の `taskly-auth.iy0306.com` は切り替え中に残す。WebAuthn の登録・認証オプションでは従来の RP ID `taskly.iy0306.com` を指定し、検証側で新 UI の origin `https://auth.taskly.iy0306.com` を正確に許可する。

ホスト名と証明書の変更だけではパスキーログインは完成しない。認証サービスに旧 `passkeys` 行を読み取る WebAuthn ログイン、旧ユーザー ID の保持、署名カウンタの更新、ブロック判定、新規パスキー登録・管理を実装した。旧 API と新 API が同じパスキー行を別 DB で同時更新する期間は、カウンタや名前・削除の同期を決めるまで切り替えない。

既存パスキーは本番データと分離した検証 DB で、旧 RP ID のまま新 UI から実端末でログインを試す。`/auth/passkey/begin` と `/auth/passkey/finish` の応答とセッション作成を確認する。一時的に公開経路を使う場合は、この2経路だけを接続元 IP で制限して開放し、管理 API の 503 ゲートを維持する。確認後は一時コンテナと経路を削除し、通常の移行ゲートへ戻す。

検証用・本番用の `AUTH_DSN` には `?parseTime=true&charset=utf8mb4&loc=UTC` を付ける。認証 API は起動時に DSN を検査する。

## DNS

Cloudflare の DNS と R2 custom domain で以下を設定します。API と旧予定 UI は本番ホストを向け、新 UI は R2 が管理する custom domain とします。

| 用途 | Type | Name | Content | Proxy status | TTL |
| --- | --- | --- | --- | --- | --- |
| 旧予定 UI | A | `taskly-auth` | `133.242.49.12` | DNS only | Auto |
| 認証 API | A | `taskly-auth-api` | `133.242.49.12` | DNS only | Auto |
| 新 UI | CNAME (Cloudflare managed) | `auth.taskly` | R2 custom domain | Proxied | Auto |

API の Nginx vhost は `deploy/nginx/` に保存し、`127.0.0.1:3070` へ proxy します。UI は [認証 UI の静的配信仕様](static-ui-r2.md) に従って Vite の成果物を Cloudflare R2 から配信し、Go コンテナの UI ポートは公開しません。新 UI は R2 custom domain で配信し、本番サーバーの Nginx を経由しません。認証 API は OAuth の移行ゲートが整うまで 503 と `Retry-After` を維持します。

### 公開先の確認

API の OAuth メタデータまで確認します。想定したコンテナに届かない場合は、Cloudflare の DNS レコードの Content、Origin Rule、Worker Route、Redirect Rule を確認します。Nginx 設定と証明書の更新も `nginx -t` と Certbot の dry run で確認します。

認証コンテナの配置後、API はオリジンと公開 URL の双方、UI は R2 custom domain で確認します。証明書が一致しない場合は `-k` で検査を通さず、証明書と配信設定を修正してから再試行します。

```sh
curl --resolve taskly-auth-api.iy0306.com:443:133.242.49.12 \
  https://taskly-auth-api.iy0306.com/.well-known/oauth-authorization-server
curl --resolve taskly-auth.iy0306.com:443:133.242.49.12 \
  https://taskly-auth.iy0306.com/login
curl https://taskly-auth-api.iy0306.com/.well-known/oauth-authorization-server
curl https://auth.taskly.iy0306.com/login
```

API は `issuer` が `https://taskly-auth-api.iy0306.com` の JSON、UI は認証画面を返すことを確認します。`auth.taskly.iy0306.com` を `133.242.49.12` へ `--resolve` する検査は旧 Nginx の退避経路の確認に限り、R2 の公開確認には使いません。

ステージング検証後は `AUTH_MIGRATION_READY=0` の公開用 Compose を使い、API のみを `127.0.0.1:3070` で Nginx に接続します。公開 `/healthz` と OAuth メタデータが 200 を返し、正しい issuer、authorization endpoint、token endpoint と PKCE S256 が宣言されていることを確認します。`/oauth/authorize` と `/auth/login` は 503、`/internal` と `/internal/*` は 404 を確認します。コンテナは read-only、全 capability drop、no-new-privileges で動かします。

本番 OAuth 設定値は `AUTH_ISSUER=https://taskly-auth-api.iy0306.com`、`AUTH_UI_URL=https://auth.taskly.iy0306.com`、`OAUTH_REDIRECT_URI=https://api.taskly.iy0306.com/auth/oauth/callback`、Taskly 側の `WEB_PUBLIC_URL=https://taskly.iy0306.com` です。WebAuthn は `WEBAUTHN_RP_ID=taskly.iy0306.com` を維持し、検証 origin に `AUTH_UI_URL` の値を正確に使用します。Taskly 側の本番環境設定は現行のリポジトリ直下 `.env.production` を暗号化してデプロイする方式であり、`apps/server/.env.production` ではありません。

## 公開前のホスト内検証

`scripts/provision-stage.py` は本番ホストで一度だけ実行し、`taskly_auth` database、同 database にだけ権限を持つ専用ユーザー、mode 0600 の `.env.production` を作ります。`users` テーブルは元DBの構造だけを `CREATE TABLE LIKE` で複製します。既存 Taskly コンテナから TOTP 鍵、SMTP 設定、Discord webhook をプロセス内で引き継ぎ、値を標準出力や Git に出しません。既存の `taskly` database とユーザー行は変更せず、認証行はこの段階ではコピーしません。既存の認証 DB や環境ファイルがあれば実行を拒否します。

`compose.stage.yaml` は GitHub Actions で作成したコミット SHA 固定のイメージを内部 Docker ネットワークだけで動かします。手動実行した workflow は同じイメージを 3 日間の Actions 成果物として保存します。これを `gh run download`、SSH 転送、`docker load` で本番ホストに入れます。ホストのポートを公開しないため Nginx は 503 のままです。内部の DB 接続、OAuth メタデータ、Discord 通知を検証した後に限り、移行と書き込み所有権の切り替えに進みます。`AUTH_MIGRATION_READY=0` のまま `compose.prod.yaml` に接続して health と OAuth メタデータを公開することはできます。認証を有効化する前に下記の全ゲートを満たす必要があります。

## 事前確認

1. 本番の暗号鍵 `TOTP_ENC_KEY`、SMTP 設定、Discord webhook を既存の暗号化された本番環境設定から認証サービスの `.env.production` に安全に移します。ログや Git に値を出しません。
2. `OAUTH_CLIENT_SECRET` と専用 DB パスワードを新規に作成します。Taskly API と認証サービスのクライアント秘密だけを一致させます。
3. source `taskly` と target `taskly_auth` をバックアップします。既存バックアップの復元テストを実施します。
4. 新 DB と専用ユーザーを作成します。`taskly_auth` への権限だけを付与します。Taskly の既存 DB ユーザーに認証 DB の権限を付与しません。
5. 既存ログインを維持した状態で、開発/ステージング環境でコピーと照合を完走します。

現行の Taskly Airflow `backup_mysql` は `MYSQL_DATABASE=taskly` のみを dump し、`taskly_auth` を含みません。認証 DB へ実ユーザーをコピーする前に、認証 DB 専用の暗号化された定期バックアップを構成し、別の MySQL 8.4 環境で SQL を最後まで復元してユーザー件数と OAuth テーブルを照合してください。ファイルの復号や gzip 検査だけでは復元可能とは判定しません。

`scripts/provision-backup.py` は本番ホストで `taskly_auth` への `SELECT, SHOW VIEW, EVENT, TRIGGER` 権限を持つ backup ユーザーと mode 0600 の `.env.backup` を一度だけ作る。既存 Airflow コンテナの MySQL 8.4 `mysqldump`、age recipient、FTPS 資格情報を再利用し、`scripts/run-backup.py` は認証 DB を `/opt/airflow/backups/taskly-auth` へ暗号化してから、既存の非公開 FTPS ディレクトリ内の `taskly-auth` サブディレクトリへ転送・ハッシュ照合する。既存アプリ DB と認証 DB のバックアップ保持・削除を分離するため、両ディレクトリを混ぜない。失敗時は認証サービス用 Discord webhook へ秘密値を含めず通知する。初回手動バックアップが成功した後、ホストの `ubuntu` ユーザーの cron に毎日 03:30 JST の `python3 /home/ubuntu/taskly-auth/scripts/run-backup.py` を登録する。Airflow コンテナが停止している場合はジョブが失敗して通知されるため、復旧後に再実行する。

実ユーザーの移行後も、別環境で認証 DB を復元し、件数を照合する。

## コピー

`migrate-auth` は `users`, `totp_secrets`, `recovery_codes`, `passkeys`, `login_codes`, `sessions`, `blocks` を主キーごとコピーします。元行を更新・削除しません。移行先に 1 行でも存在すれば拒否します。各テーブルのコピー後と全体コピー後に件数と SHA-256 を比較します。切り替え後の退会と期限後消去は[アカウント退会・消去の同期](account-lifecycle-sync.md)に従います。

新規環境の `schema.sql` の `users` は `deleted_at DATETIME NULL` を持つ。既存の `taskly_auth.users` が旧スキーマのままなら、コピー前に `SHOW COLUMNS FROM taskly_auth.users` で列の有無を確認し、空テーブルであることを確認した上で `ALTER TABLE taskly_auth.users ADD COLUMN deleted_at DATETIME NULL` を適用する。移行ツールは元 DB と先 DB の列名・順序を照合し、差異があればコピーを拒否する。`policy_consents` はアプリ DB の同意イベントとして維持し、この7テーブルのコピー対象には含めない。退会後30日で動くアプリ側の消去処理は認証 DB を消去しないため、両 DB の退会・保留・期限後消去を連携し、復元後にも消去を再適用できるまで本番切り替えを禁止する。

稼働中に手順を試す場合は、**本番切り替え先とは別の空DB**を用意して `AUTH_MIGRATION_MODE=snapshot migrate-auth` を実行します。元DBの `REPEATABLE READ` 読み取り専用トランザクションから一貫したスナップショットをコピーし、ログにも「切り替え不可」と記録します。元サービスの書き込みは止めません。この DB は検証用であり、新しいユーザー更新を含む正本にはなりません。

本番ホストでは `scripts/provision-rehearsal.py` を一度だけ実行して `taskly_auth_rehearsal` と専用ユーザーを作ります。このユーザーは元DBの上記7テーブルに `SELECT` 権限だけを持ち、検証DBにだけ書き込めます。mode 0600 の `.env.rehearsal` を使い、内部ネットワークで移行イメージを実行します。検証DBを本番認証サービスの `AUTH_DSN` に指定してはいけません。

コピー後の差分確認には `AUTH_MIGRATION_MODE=verify` を使います。ソースとターゲットの7テーブルをそれぞれ読み取り専用の一貫したスナップショットで照合し、件数または行ハッシュが異なれば非ゼロで終了してDiscordへ通知します。DBには書き込みません。二つのスナップショットは同時点の原子的な取得ではないため、一致しても本番切り替えの承認にはなりません。差分がある検証DBへ上書きせず、新しい空の検証DBでコピーをやり直します。

旧 Taskly の `blocks.expires_at` は JST の DATETIME として保存されている。新規コピーではこの列だけ9時間戻して UTC の DATETIME とし、照合では移行元の同列を変換して比較する。無期限の NULL はそのまま保持する。既にコピー済みの移行先を補正する場合は、元サービスの書き込みを止めてバックアップを取り、`AUTH_MIGRATION_MODE=repair-blocks AUTH_WRITES_PAUSED=1` で同じ `SOURCE_DSN` と `TARGET_DSN` を指定して `migrate-auth` を実行する。元行と同じ未変換値の行のみ更新し、変換済みの行はスキップする。元行にない新しい UTC 同期行は触れず、どちらとも異なる値があれば停止する。再実行しても二重変換しない。切り替え前に期限前・期限後・無期限・明示解除の4ケースを確認する。

```sh
python3 scripts/provision-rehearsal.py
docker run --rm --read-only --network infra_default \
  --env-file .env.rehearsal --entrypoint /usr/local/bin/migrate-auth \
  ghcr.io/asari194617/taskly-auth:<verified-commit-sha>
docker run --rm --read-only --network infra_default \
  --env-file .env.rehearsal -e AUTH_MIGRATION_MODE=verify \
  --entrypoint /usr/local/bin/migrate-auth \
  ghcr.io/asari194617/taskly-auth:<verified-commit-sha>
```

本番切り替え用のコピーでは、Taskly API の全インスタンスで `AUTH_WRITES_PAUSED=1` の保守ゲートを有効にし、GET の OAuth コールバックを含む `/healthz` 以外が503になることを確認します。バックグラウンド処理と外部 DB クライアントも停止したことを確認してから、`SOURCE_DSN` と `TARGET_DSN` に異なる database の接続文字列を指定し、`AUTH_MIGRATION_MODE=cutover AUTH_WRITES_PAUSED=1 migrate-auth` を実行します。移行ツール側のフラグはオペレーターによる停止確認であり、プログラムが書き込みを止めるものではありません。コピーと最終照合が終わるまで旧ログインを再開しません。失敗した場合は本番切り替えを行わず、バックアップから空の移行先を復元して原因を修正します。

既存の `sessions` は保存しますが、新 OAuth セッションとして自動的には引き継ぎません。Taskly API の既存セッションを旧 DB で TTL まで受け入れる期間を設けます。
既存の `login_codes` も保存しますが、新サービスは UTC の `auth_login_codes` を別に使用します。切り替え中に旧コードを入力したユーザーには新しいコードの再送を案内します。

## 切り替えのゲート

本番切り替えの直前状態は、ホストで次の読み取り専用検査を実行する。環境ファイルの値やユーザーデータは出力しない。全項目が `PASS` となり、最後に `READY` が表示されるまで書き込み停止と最終コピーへ進まない。

```sh
sudo python3 /home/ubuntu/taskly-auth/scripts/check_release_readiness.py \
  --expected-auth-tag <reviewed-main-commit-sha>
```

この検査は、認証APIの503ゲート、Taskly側の8個の所有権フラグがすべて0であること、OAuth client secretと内部同期tokenの一致、DSNのUTC設定、コンテナのhardening、内部ネットワーク、元DBのユーザー保持、空の切り替え先DB、公開URL、バックアップcronを一括確認する。最終コピー後は対象DBが空でなくなるため、この事前検査を再実行せず、コピーの `verify` と切り替え後の機能試験を使う。

切り替え前に以下を実証します。

- ユーザー ID、件数、ハッシュ、パスワード認証、メールコード、TOTP、復旧コード、ブロック判定を照合する。
- 旧 RP ID `taskly.iy0306.com` のパスキー利用者が新 UI origin からログインでき、旧 API と新 API の署名カウンタ更新が競合しない。
- パスキーの所有権移管時に Taskly API の全インスタンスで `AUTH_PASSKEYS_EXTERNAL=1` を有効にし、旧 `/webauthn/*` と GraphQL の登録・変更・削除が拒否されることを確認する。このゲートだけでは他の認証データ書き込みは止まらない。
- Taskly Web のパスキー管理画面と連携ログアウトへの導線は、切り替え時に `PUBLIC_AUTH_PASSKEY_URL` と `PUBLIC_AUTH_LOGOUT_URL` を設定して確認する。
- 旧 Taskly アプリのユーザー・パスワード・TOTP・復旧コード・パスキー更新経路を auth DB に移管する。旧 DB と新 DB の書き込み競合を解消する。
- 復旧コードの所有権移管時は Taskly API の `AUTH_RECOVERY_EXTERNAL=1` で旧 GraphQL/REST の再発行と旧ログイン経路によるコード消費を拒否する。Web の `PUBLIC_AUTH_RECOVERY_URL=https://auth.taskly.iy0306.com/security` は新 UI 公開後に設定する。
- TOTP の所有権移管時は Taskly API の `AUTH_TOTP_EXTERNAL=1` で旧 GraphQL/REST の設定変更、方法一覧、旧ログイン経路によるコード検証を拒否する。Web の `PUBLIC_AUTH_TOTP_URL=https://auth.taskly.iy0306.com/security` は新 UI 公開後に設定する。暗号化済み秘密と `TOTP_ENC_KEY` は変換しない。
- Taskly 側の [`認証データ書き込み経路の棚卸し`](https://github.com/asari194617/taskly/blob/main/docs/auth-identity-write-inventory.md) にある管理者操作、ブロック、プロフィール、セッション失効も移管または明示的に同期する。
- native/public client の PKCE 接続と、Taskly セッション・認証サービスセッションの連携ログアウトを確認する。
- public client の登録機能は実装済みだが、本番 `OAUTH_PUBLIC_CLIENTS_JSON` は空のままにし、実際の native アプリの redirect URI と端末上でのコールバック検証ができるまで登録しない。
- 既存の API キー、権限、ユーザー参照、既存セッションを確認する。
- 新しい FQDN の TLS、OAuth redirect、開発環境、Discord 異常通知、切り戻しを確認する。

いずれかが未達なら、本番のログイン経路を切り替えず、旧 DB の認証行も削除しません。
本番モードの認証 API は `AUTH_MIGRATION_READY=1` を明示するまで `/auth/*` と `/oauth/*` に 503 を返します。内部ステージングでは常に `0` に固定し、全ゲートが満たされるまで公開用 Compose に `1` を渡しません。

`migrate-auth` は切り替え先に不足するテーブルを元 DB の `SHOW CREATE TABLE` から作成します。7 テーブルすべての作成・照合が済んでから `AUTH_MIGRATION_READY=1` とブロック同期フラグを有効にします。

## ロールバック

Taskly Web の OAuth リンクを非表示にし、旧ログインを維持します。新認証 DB 側で行われたパスワード変更、復旧コード消費、TOTP 設定、メールログイン設定などがあれば、旧 DB に安全に反映してから旧方式を正本に戻します。source と target のバックアップを保持し、単純な上書き復元で新しい更新を失わないようにします。

## 本番セッション Cookie の切り替え

HTTPS の API は `__Host-taskly_auth_session`（Secure、HttpOnly、Path=/、Domain なし）だけを認証に使う。旧 `taskly_auth_session` は認証に利用しないため、配備時に既存のブラウザセッションは再ログインが必要になる。ログアウト時は両方の host-only Cookie を削除する。HTTP のローカル開発は従来の名前を使う。親ドメインが設定した同名 Cookie はサーバーから削除できないが、新しい接頭辞の Cookie としてブラウザに受け入れられない。
