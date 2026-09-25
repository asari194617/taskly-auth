# 本番認証 DB 移行手順

この文書は 2026-09-21 の本番構成に基づきます。Taskly は `infra-db-1` (MySQL 8.4) を使い、認証 API を配置するホストは `133.242.49.12` です。ホストの空きメモリは限られるため、認証 DB は同じ MySQL インスタンス上の別 database として作成し、専用ユーザーと権限で分離します。別 MySQL インスタンスにする場合は容量計画と移行手順を改めて確認します。

## 認証 UI の新ホスト名と既存パスキー

2026-09-22 に認証 UI の公開予定ホストを `auth.taskly.iy0306.com` に変更する。従来の `taskly-auth.iy0306.com` は切り替え中に残し、認証 API の `taskly-auth-api.iy0306.com` は維持する。本番 Taskly の WebAuthn RP ID は実測で `taskly.iy0306.com`、旧画面の origin は `https://taskly.iy0306.com` だった。新 UI はこの RP ID のサブドメインなので、WebAuthn の登録・認証オプションに **同じ RP ID** を明示し、検証側で新 origin `https://auth.taskly.iy0306.com` を正確に許可すれば、既存クレデンシャルを再登録せずに利用できる可能性がある。RP ID を新ホスト名へ変更してはいけない。

ホスト名と証明書の変更だけではパスキーログインは完成しない。認証サービスに旧 `passkeys` 行を読み取る WebAuthn ログイン、旧ユーザー ID の保持、署名カウンタの更新、ブロック判定、新規パスキー登録・管理を実装した。移行済みの実クレデンシャルを使った端末検証は未完了である。旧 API と新 API が同じパスキー行を別 DB で同時更新する期間は、カウンタや名前・削除の同期を決めるまで切り替えない。2026-09-22 の読み取り専用集計では有効ユーザー46件、パスキー保有者1件、パスキー以外に現行ログイン手段がないユーザー0件だった。

## DNS

Cloudflare の DNS と R2 custom domain で以下を設定します。API と旧予定 UI は本番ホストを向け、新 UI は R2 が管理する custom domain とします。

| 用途 | Type | Name | Content | Proxy status | TTL |
| --- | --- | --- | --- | --- | --- |
| 旧予定 UI | A | `taskly-auth` | `133.242.49.12` | DNS only | Auto |
| 認証 API | A | `taskly-auth-api` | `133.242.49.12` | DNS only | Auto |
| 新 UI | CNAME (Cloudflare managed) | `auth.taskly` | R2 custom domain | Proxied | Auto |

API の Nginx vhost は `deploy/nginx/` に保存し、`127.0.0.1:3070` へ proxy します。UI は [認証 UI の静的配信仕様](static-ui-r2.md) に従って Vite の成果物を Cloudflare R2 から配信し、Go コンテナの UI ポートは公開しません。2026-09-25 に `auth.taskly.iy0306.com` を R2 custom domain へ切り替えたため、このホストは本番サーバーの Nginx を経由しません。認証 API は OAuth の移行ゲートが整うまで 503 と `Retry-After` を維持します。

### 公開先の確認 (2026-09-21)

DNS 登録後、両ホストの A 応答は Cloudflare の proxy IP になり、公開 `/healthz` は HTTPS 200 (`ok`) を返しました。ただし公開 UI の `/login` と API の `/.well-known/oauth-authorization-server` は 404 (`Cannot GET ...`) でした。さらに `133.242.49.12` へ直接接続した際の API ホスト名は TLS 証明書が一致せず、同ホストに認証サービスの Docker コンテナと Nginx vhost はありませんでした。**公開 `/healthz` の 200 だけでは認証サービスへの到達を証明できません。** Cloudflare の当該 DNS レコードの Content と、該当する Origin Rule、Worker Route、Redirect Rule を確認してください。Cloudflare proxy の応答から元の A レコードの Content は読み取れません。

その後ユーザーが両レコードを DNS only に変更しました。`1.1.1.1` と `8.8.8.8` から両ホストの A レコードが `133.242.49.12` を返すことを確認しました。初回の HTTP はオリジン Nginx の 404、HTTPS は証明書のホスト名不一致でした。

同日、両 Nginx vhost を本番ホストに配置し、既存の Certbot アカウントで両ホスト名を含む Let's Encrypt 証明書を発行しました。有効期限は 2026-12-20 です。`nginx -t`、HTTP → HTTPS の 301、両ホストの証明書検証、Certbot 更新の dry run が成功しました。HTTPS は現在 503 を返します。内部ステージングコンテナは稼働していますが、公開ポートには接続していません。証明書と入口は整いましたが、本番切り替えは未完了です。

2026-09-22 に追加された `auth.taskly.iy0306.com` の A レコードが `1.1.1.1` と `8.8.8.8` から `133.242.49.12` を返すことを確認した。同ホスト専用の Nginx vhost と Let's Encrypt 証明書を配置した。証明書は 2026-12-21 まで有効で、Certbot の更新 dry run、`nginx -t`、HTTP 301、証明書検証付きの HTTPS 503、オリジン直結の HTTPS 503 が成功した。新ホストは公開ポートの認証コンテナへ接続せず、明示的な503ゲートを維持している。

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

2026-09-25 に Vite UI を R2 へ初回デプロイし、R2 custom domain の Active を確認した。`/` は `/login` へ302、`/login`、`/security`、`/passkeys`、`/logout` は200となり、MIME type、ページとアセットの Cache-Control、セキュリティヘッダーを確認した。認証 API は引き続き503ゲートを維持している。この作業では認証 DB のコピーと切り替えフラグを変更していない。

本番 OAuth 設定値は `AUTH_ISSUER=https://taskly-auth-api.iy0306.com`、`AUTH_UI_URL=https://auth.taskly.iy0306.com`、`OAUTH_REDIRECT_URI=https://api.taskly.iy0306.com/auth/oauth/callback`、Taskly 側の `WEB_PUBLIC_URL=https://taskly.iy0306.com` です。WebAuthn は `WEBAUTHN_RP_ID=taskly.iy0306.com` を維持し、検証 origin に `AUTH_UI_URL` の値を正確に使用します。Taskly 側の本番環境設定は現行のリポジトリ直下 `.env.production` を暗号化してデプロイする方式であり、`apps/server/.env.production` ではありません。

## 公開前のホスト内検証

`scripts/provision-stage.py` は本番ホストで一度だけ実行し、`taskly_auth` database、同 database にだけ権限を持つ専用ユーザー、mode 0600 の `.env.production` を作ります。`users` テーブルは元DBの構造だけを `CREATE TABLE LIKE` で複製します。既存 Taskly コンテナから TOTP 鍵、SMTP 設定、Discord webhook をプロセス内で引き継ぎ、値を標準出力や Git に出しません。既存の `taskly` database とユーザー行は変更せず、認証行はこの段階ではコピーしません。既存の認証 DB や環境ファイルがあれば実行を拒否します。

`compose.stage.yaml` は GitHub Actions で作成したコミット SHA 固定のイメージを内部 Docker ネットワークだけで動かします。手動実行した workflow は同じイメージを 3 日間の非公開 artifact として保存します。これを `gh run download`、SSH 転送、`docker load` で本番ホストに入れます。ホストのポートを公開しないため Nginx は 503 のままです。内部の DB 接続、OAuth メタデータ、Discord 通知を検証した後に限り、移行と書き込み所有権の切り替えに進みます。`compose.prod.yaml` に切り替える前に下記の全ゲートを満たす必要があります。

2026-09-21 に専用 DB と設定ファイルを作成し、内部ステージングコンテナの health check と OAuth メタデータを確認しました。新 DB の `users` は 0 行で、元 DB のユーザー行は変更していません。公開 FQDN は引き続き HTTPS 503 です。Discord webhook の実送信では既定の HTTP クライアント名が 403 となり、`TasklyAuth/1.0` を指定すると 204 で受理されました。通知コードにこのヘッダーを設定しています。
DB 接続・スキーマ・設定の起動失敗時は、Discord への送信が完了するか5秒のタイムアウトになるまで待ってからプロセスを終了します。通常のリクエスト中の通知は非同期です。起動失敗経路は単体テスト済みですが、本番 webhook を用いた障害注入は未実施です。コミット `f467c41` のイメージを内部ステージングへ反映し、healthy、`users` 0 行、内部 OAuth 503、公開 UI 503 を再確認しました。
移行ガードを追加したコミット `6c4a410` の内部コンテナでも health は成功し、`/oauth/authorize` は 503 でした。公開ポートはなく、公開 FQDN も 503 を維持しています。
その後の OAuth 実装では、リフレッシュトークン系列の再利用・失効時に同系列のアクセストークンも失効させるため `oauth_access_tokens.family_id` を追加しました。既にテーブルがある内部ステージングでは起動時にカラムとインデックスを追加します。コミット `e2ab506` のイメージを内部ステージングへ配置し、コンテナの healthy、DB の追加カラム、`users` 0 行、内部 `/oauth/authorize` の 503 を確認しました。公開 API も TLS 検証成功の 503 を維持しています。
コミット `37de2fe` で登録済み public client のAuthorization Code + PKCEを追加し、MySQLを使うCIスモークテストでconfidential/public両clientのコード交換・更新・再利用検知が成功しました。同イメージの内部ステージングは healthy です。本番の public client 登録は空のため、メタデータは `client_secret_basic` だけを示します。認証DBは0ユーザー、公開OAuthは503を維持しています。
コミット `b988693` で認証 UI の `/logout` を追加し、Taskly Web のログアウト後に認証 API のセッションも失効させる経路を実装しました。認証セッションのDB削除に失敗した場合は503を返し、UIは再試行を表示します。同コミットの MySQL CI スモークテストは認証セッション失効後に再認可がログイン画面へ戻ることを確認しました。手動イメージビルド時のスモークジョブは認証コンテナへ接続できず失敗したため、失敗時のコンテナログ採取を追加して再実行し、最新CIは成功しました。ビルド済み `b988693` のイメージを内部ステージングへ反映し、コンテナ healthy、内部 `/logout` の表示、内部 OAuth 503、公開 UI/API の TLS 検証成功と503を確認しました。元DBのユーザーは47件、切り替え先認証DBは0件、検証DBは47件です。公開前のため Taskly Web の本番 `PUBLIC_AUTH_LOGOUT_URL` はまだ設定しません。

2026-09-22 にコミット `8818859` で既存パスキーの認証経路を追加した。旧保存形式を読み、旧ユーザー ID と RP ID を維持し、ワンタイムのチャレンジと行ロック付きの署名カウンタ更新を行う。開発環境のCIでGoテスト・静的解析・MySQL OAuthスモークテストが成功した。ホストの既存 `.env.production` は `AUTH_UI_URL=https://auth.taskly.iy0306.com` と `WEBAUTHN_RP_ID=taskly.iy0306.com` に原子的に更新し、ビルド済みイメージのSHA-256一致後に内部ステージングへ配置した。コンテナは healthy、内部画面にパスキーボタンがあり、内部 OAuth と公開 UI/API は引き続き503である。旧ユーザーの実クレデンシャルによる認証試験は未実施で、実データの切り替えは行っていない。

同日、コミット `a3375b9` でパスキー登録・一覧・名前変更・削除を認証 UI/API に追加した。登録と変更操作は直近10分の認証セッションを要求し、登録要求 ID は一回限りとした。Goテスト、静的解析、開発用MySQLの登録開始・無効応答拒否スモークテストが成功した。イメージのSHA-256を転送前後で照合して内部ステージングへ配置し、コンテナ healthy、内部管理画面の表示、内部管理APIの503、公開 UI/API の証明書検証成功と503を確認した。Taskly Web の管理画面リンクは `PUBLIC_AUTH_PASSKEY_URL` が設定されたビルドでだけ切り替わり、本番ではまだ有効にしていない。実端末での登録・既存パスキー認証は未検証である。

## 事前確認

1. 本番の暗号鍵 `TOTP_ENC_KEY`、SMTP 設定、Discord webhook を既存の暗号化された本番環境設定から認証サービスの `.env.production` に安全に移します。ログや Git に値を出しません。
2. `OAUTH_CLIENT_SECRET` と専用 DB パスワードを新規に作成します。Taskly API と認証サービスのクライアント秘密だけを一致させます。
3. source `taskly` と target `taskly_auth` をバックアップします。既存バックアップの復元テストを実施します。

現行の Taskly Airflow `backup_mysql` は `MYSQL_DATABASE=taskly` のみを dump し、`taskly_auth` を含みません。認証 DB へ実ユーザーをコピーする前に、認証 DB 専用の暗号化された定期バックアップを構成し、別の MySQL 8.4 環境で SQL を最後まで復元してユーザー件数と OAuth テーブルを照合してください。ファイルの復号や gzip 検査だけでは復元可能とは判定しません。

`scripts/provision-backup.py` は本番ホストで `taskly_auth` の読み取り専用 backup ユーザーと mode 0600 の `.env.backup` を一度だけ作る。既存 Airflow コンテナの MySQL 8.4 `mysqldump`、age recipient、FTPS 資格情報を再利用し、`scripts/run-backup.py` は認証 DB を `/opt/airflow/backups/taskly-auth` へ暗号化してから、既存の非公開 FTPS ディレクトリ内の `taskly-auth` サブディレクトリへ転送・ハッシュ照合する。既存アプリ DB と認証 DB のバックアップ保持・削除を分離するため、両ディレクトリを混ぜない。失敗時は認証サービス用 Discord webhook へ秘密値を含めず通知する。初回手動バックアップが成功した後、ホストの `ubuntu` ユーザーの cron に毎日 03:30 JST の `python3 /home/ubuntu/taskly-auth/scripts/run-backup.py` を登録する。Airflow コンテナが停止している場合はジョブが失敗して通知されるため、復旧後に再実行する。

2026-09-21 に専用 backup ユーザーの権限を `taskly_auth` への `SELECT, SHOW VIEW, EVENT, TRIGGER` に限定し、mode 0600 の `.env.backup` を作成した。初回バックアップ `backup-20260921-133621.sql.gz.age` は専用 FTPS ディレクトリへ転送され、再取得後のサイズと SHA-256 が一致した。ホストの JST 03:30 の cron 登録も確認した。暗号化ファイルを別のローカル MySQL 8.4 コンテナへ復号し、SQL を最後まで投入した。元DBと復元先はいずれも9テーブル、ユーザー0件、OAuthトークン0件だった。検証コンテナとローカルの復号鍵コピーは試験後に削除した。

2026-09-22 JST 03:30 の定期ジョブでも `backup-20260921-183007.sql.gz.age` が作成され、FTPS への転送とハッシュ照合が成功した。同日、ホスト上の一時ディレクトリにスクリプトをコピーし、backup 設定ファイルが欠落する障害を注入した。スクリプトは終了コード1を返し、Discord 通知の送信エラーは発生しなかった。本番の `.env.backup` と cron は変更していない。実ユーザー移行後の復元・件数照合は引き続き必要である。
4. 新 DB と専用ユーザーを作成します。`taskly_auth` への権限だけを付与します。Taskly の既存 DB ユーザーに認証 DB の権限を付与しません。
5. 既存ログインを維持した状態で、開発/ステージング環境でコピーと照合を完走します。

## コピー

`migrate-auth` は `users`, `totp_secrets`, `recovery_codes`, `passkeys`, `login_codes`, `sessions`, `blocks` を主キーごとコピーします。元行を更新・削除しません。移行先に 1 行でも存在すれば拒否します。各テーブルのコピー後と全体コピー後に件数と SHA-256 を比較します。切り替え後の退会と期限後消去は[アカウント退会・消去の同期](account-lifecycle-sync.md)に従います。

2026-09-23 に Taskly の `users` に `deleted_at DATETIME NULL` が追加された。新規環境の `schema.sql` は同列を持つ。既存の `taskly_auth.users` が旧スキーマのままなら、コピー前に `SHOW COLUMNS FROM taskly_auth.users` で列の有無を確認し、空テーブルであることを確認した上で `ALTER TABLE taskly_auth.users ADD COLUMN deleted_at DATETIME NULL` を適用する。移行ツールは元 DB と先 DB の列名・順序を照合し、差異があればコピーを拒否する。`policy_consents` はアプリ DB の同意イベントとして維持し、この7テーブルのコピー対象には含めない。退会後30日で動くアプリ側の消去処理は認証 DB を消去しないため、両 DB の退会・保留・期限後消去を連携し、復元後にも消去を再適用できるまで本番切り替えを禁止する。

2026-09-23 の本番読み取り専用確認では、`taskly.users` 47行、`taskly_auth.users` 0行で、両方とも `deleted_at` はまだなかった。2026-09-24 に Taskly 側の migration 適用を確認した後、0行の `taskly_auth.users` に `deleted_at DATETIME NULL` を追加した。両 DB で同列は ordinal position 13、nullable で一致し、確認後も利用者件数は47行と0行のままである。実ユーザーのコピーはまだ実行していない。

稼働中に手順を試す場合は、**本番切り替え先とは別の空DB**を用意して `AUTH_MIGRATION_MODE=snapshot migrate-auth` を実行します。元DBの `REPEATABLE READ` 読み取り専用トランザクションから一貫したスナップショットをコピーし、ログにも「切り替え不可」と記録します。元サービスの書き込みは止めません。この DB は検証用であり、新しいユーザー更新を含む正本にはなりません。

本番ホストでは `scripts/provision-rehearsal.py` を一度だけ実行して `taskly_auth_rehearsal` と専用ユーザーを作ります。このユーザーは元DBの上記7テーブルに `SELECT` 権限だけを持ち、検証DBにだけ書き込めます。mode 0600 の `.env.rehearsal` を使い、内部ネットワークで移行イメージを実行します。検証DBを本番認証サービスの `AUTH_DSN` に指定してはいけません。

2026-09-21 の本番ホスト上の検証では、`users` 47、`totp_secrets` 1、`recovery_codes` 10、`passkeys` 1、`login_codes` 23、`sessions` 68、`blocks` 2 行をスナップショットからコピーし、7テーブルすべての件数と SHA-256 が一致しました。実行後のユーザー件数は元DB 47、切り替え先 `taskly_auth` 0、検証DB 47 です。元DBは書き込み可能なままで、この検証DBを切り替え先には使いません。

コピー後の差分確認には `AUTH_MIGRATION_MODE=verify` を使います。ソースとターゲットの7テーブルをそれぞれ読み取り専用の一貫したスナップショットで照合し、件数または行ハッシュが異なれば非ゼロで終了してDiscordへ通知します。DBには書き込みません。二つのスナップショットは同時点の原子的な取得ではないため、一致しても本番切り替えの承認にはなりません。差分がある検証DBへ上書きせず、新しい空の検証DBでコピーをやり直します。

2026-09-21 09:46 UTC にコミット `619acc9` のイメージで元DBと検証DBを再照合し、7テーブルすべての件数と行ハッシュが一致しました。件数は順に `47, 1, 10, 1, 23, 68, 2` です。同じイメージの内部ステージングは healthy、実際の認証DBは0ユーザー、公開 OAuth は TLS 検証成功の503です。元DBの書き込みを停止していないため、この一致は最終コピーや切り替えの承認ではありません。

```sh
python3 provision-rehearsal.py
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

切り替え前に以下を実証します。

- ユーザー ID、件数、ハッシュ、パスワード認証、メールコード、TOTP、復旧コード、ブロック判定を照合する。
- 旧 RP ID `taskly.iy0306.com` のパスキー利用者が新 UI origin からログインでき、旧 API と新 API の署名カウンタ更新が競合しない。
- パスキーの所有権移管時に Taskly API の全インスタンスで `AUTH_PASSKEYS_EXTERNAL=1` を有効にし、旧 `/webauthn/*` と GraphQL の登録・変更・削除が拒否されることを確認する。このゲートだけでは他の認証データ書き込みは止まらない。
- 旧 Taskly アプリのユーザー・パスワード・TOTP・復旧コード・パスキー更新経路を auth DB に移管する。旧 DB と新 DB の書き込み競合を解消する。
- 復旧コードの所有権移管時は Taskly API の `AUTH_RECOVERY_EXTERNAL=1` で旧 GraphQL/REST の再発行と旧ログイン経路によるコード消費を拒否する。Web の `PUBLIC_AUTH_RECOVERY_URL=https://auth.taskly.iy0306.com/security` は新 UI 公開後に設定する。
- TOTP の所有権移管時は Taskly API の `AUTH_TOTP_EXTERNAL=1` で旧 GraphQL/REST の設定変更、方法一覧、旧ログイン経路によるコード検証を拒否する。Web の `PUBLIC_AUTH_TOTP_URL=https://auth.taskly.iy0306.com/security` は新 UI 公開後に設定する。暗号化済み秘密と `TOTP_ENC_KEY` は変換しない。
- Taskly 側の [`認証データ書き込み経路の棚卸し`](https://github.com/asari194617/taskly/blob/feat/auth-service-extraction/docs/auth-identity-write-inventory.md) にある管理者操作、ブロック、プロフィール、セッション失効も移管または明示的に同期する。
- native/public client の PKCE 接続と、Taskly セッション・認証サービスセッションの連携ログアウトを確認する。
- public client の登録機能は実装済みだが、本番 `OAUTH_PUBLIC_CLIENTS_JSON` は空のままにし、実際の native アプリの redirect URI と端末上でのコールバック検証ができるまで登録しない。
- 既存の API キー、権限、ユーザー参照、既存セッションを確認する。
- 新しい FQDN の TLS、OAuth redirect、開発環境、Discord 異常通知、切り戻しを確認する。

いずれかが未達なら、本番のログイン経路を切り替えず、旧 DB の認証行も削除しません。
本番モードの認証 API は `AUTH_MIGRATION_READY=1` を明示するまで `/auth/*` と `/oauth/*` に 503 を返します。内部ステージングでは常に `0` に固定し、全ゲートが満たされるまで公開用 Compose に `1` を渡しません。

2026-09-22 にパスワード管理を含むイメージ `5d466568ea72064837aba6c5d182eda8aeab9cbf` を内部ステージングへ配置した。Docker の health は healthy、内部 `/healthz` は200、`/security` は200、`/auth/password/status` は移行ゲートにより503。DB の読み取り専用件数は旧 `taskly.users` 47、新 `taskly_auth.users` 0。公開 `https://auth.taskly.iy0306.com/security` と `https://taskly-auth-api.iy0306.com/oauth/authorize` は証明書検証成功の503であり、ユーザー認証はまだ公開していない。

同日にメールコード許可設定を含むイメージ `09c4f51f9cfc9d6e63a51fa2734ce86b3470b022` を内部ステージングへ配置した。転送前後の tar SHA-256 は `bba362993c1fce8710c9af91a0f312784c667754f0c9f1b411d4e399a1a1da36` で一致。コンテナ healthy、内部 `/healthz` は200、`/security` は200、`/auth/email-code/settings` は移行ゲートにより503。旧 DB は47ユーザー、新 DB は0ユーザー、公開 UI/API は証明書検証成功の503だった。開発用 MySQL の PR スモークテストは成功した。最初の手動ビルドでは開発用 Compose の MySQL ヘルスチェックが一時初期化サーバーを誤って準備完了と判定したため、最終 TCP サーバーを待つよう修正し、次の PR CI で成功した。その後の手動ワークフロー再実行は GitHub の請求・利用上限通知によりジョブ開始前に拒否されたため、既に作成済みで同じ認証バイナリを含む上記コミットの成果物を使用した。

2026-09-23 に復旧コード再発行を追加し、独立した開発用 MySQL で旧コード失効・新コード利用・使用済み状態を検証した。GitHub Actions のジョブ開始が請求・利用上限で拒否されたため、コミット `9ab872d0ad3000d9229559064aa81e2b0e931c5e` をローカルで `linux/amd64` イメージにビルドした。転送前後の tar SHA-256 は `e27bb4128a7473c9ff8e57bbcf060362bf13bb87b0d836e0895f819b6c0cb19b` で一致した。内部ステージングは healthy、`/healthz` は200、`/security` は200、`/auth/recovery/generate` は移行ゲートで503。公開 UI/API は証明書検証成功の503、旧 DB は47ユーザー、新認証 DB は0ユーザーで、旧 DB の正本を維持する。このイメージには GitHub CI の実行結果がない。

その後 GitHub Actions が復旧し、PR の Go テスト・MySQL スモークと手動ワークフロー `35745333508` の Go テスト・MySQL スモーク・イメージ作成が成功した。コミット `dcdac23f61cd8125fa94a9cc867b9fd589ed59d1` の非公開成果物を取得し、転送前後の tar SHA-256 `58f4301a6077a5acc9c32d2ef078349164a07f635b5b4fa9fc4ac17fb64eb331` を照合して内部ステージングに配置した。コンテナは healthy、ホスト公開ポートなし、内部 `/healthz` は200、`/security` は200、`/auth/recovery/generate` は503。公開 UI/API は TLS 検証成功の503、旧 DB は47ユーザー、新認証 DB は0ユーザーだった。

同日、TOTP 管理を追加したコミット `54fa181b23a12fad3fc9ffb937886e13b9f43618` は PR と手動ワークフロー `35805587467` で Go テスト、静的解析、独立 MySQL の設定・MFAログイン・無効化スモーク、イメージ作成が成功した。非公開成果物の転送前後 SHA-256 `345738b701ef6b67af6a6dad761c39388cd53edec201da3716228a7539b37afe` を照合して内部ステージングに配置した。コンテナは healthy、ホスト公開ポートなし、内部 `/healthz` と `/security` は200、`/auth/totp/status` は移行ゲートで503。起動時に `auth_totp_setups` が作成され、旧 DB は47ユーザー、新認証 DBは0ユーザーを維持した。本番 Nginx に残っていた監視用ポート `3067` / `3068` への古い upstream を、予約済みの `3070` / `3071` に修正し、構文検査後に再読込した。公開 API、旧 UI、新 UI はいずれも TLS 検証成功の503である。

同日、ブロック同期を追加したコミット `945fe2dbbb56488e55ba156c0744fce87f26548a` は PR と手動ワークフロー `35819720289` で Go テスト、静的解析、独立 MySQL のブロック・session 失効・解除を含む全スモーク、イメージ作成が成功した。成果物の転送前後 SHA-256 `6f515675edd9d2421278d8c86dd797e2e7d2280116e719a063a64437d62771fb` を照合し、内部ステージングへ配置した。コンテナは healthy、ホスト公開ポートなし、内部 `/healthz` と `/security` は200、internal block API は移行ゲートで503。公開 API、旧 UI、新 UI も503を維持した。旧 DB は47ユーザー、新認証 DBは0ユーザーである。空の認証 DB に `blocks` はまだ存在しないが、`migrate-auth` は最終コピー時に source の `SHOW CREATE TABLE` から不足テーブルを作成してから全列を照合する。`AUTH_MIGRATION_READY=1` とブロック同期フラグは、最終コピーで7テーブルが作成・照合されるまで有効にしない。

2026-09-24 にアカウント退会・消去同期を追加したコミット `2ba607989b87d5beae4f1c5d3657c3ae4c8f0eba` は PR と手動ワークフロー `35945112287` で Go テスト、静的解析、独立 MySQL の退会、再ログイン拒否、冪等な物理消去を含む全スモーク、イメージ作成が成功した。成果物の転送前後 SHA-256 `ce7df85e89d73fa53da9267d54cb24e9187e778ebce549b9bacfe950549ba185` を照合し、内部ステージングへ配置した。コンテナは healthy、ホスト公開ポートなし、内部 `/healthz` と `/security` は200、internal lifecycle API は移行ゲートで503。公開 API と UI も503を維持した。旧 DB は47ユーザー、新認証 DBは0ユーザーのままである。`AUTH_MIGRATION_READY` と lifecycle 同期フラグは有効化していない。

同日、ユーザー採番・プロフィール同期を追加したコミット `9f270c5b20e0edbfaa16e3fa1f10edd7bc356b30` は PR と手動ワークフロー `35946698786` で Go テスト、静的解析、独立 MySQL の冪等な採番、プロフィール更新、メール競合、退会消去を含む全スモーク、イメージ作成が成功した。成果物の転送前後 SHA-256 `21a8d6a780335bd5ee3e75c035c0e4c0098f664c8329e446205c90add2e6af98` を照合し、内部ステージングへ配置した。コンテナは healthy、ホスト公開ポートなし、internal user API と公開 UI/API は移行ゲートで503。旧 DB は47ユーザー、新認証 DBは0ユーザーで、実データと切り替えフラグは変更していない。

同じイメージで新規の `taskly_auth_rehearsal_20260924` に本番ソースの読み取り専用スナップショットをコピーした。`users`, `totp_secrets`, `recovery_codes`, `passkeys`, `login_codes`, `sessions`, `blocks` は順に `47, 1, 10, 1, 23, 69, 2` 行で、各テーブルの SHA-256 がコピー元と一致した。直後の read-only verify でも7テーブルが一致した。本番の切り替え先 `taskly_auth.users` は0行のままで、書き込み停止をしていないため、この結果は最終コピーの承認ではない。

## ロールバック

Taskly Web の OAuth リンクを非表示にし、旧ログインを維持します。新認証 DB 側で行われたパスワード変更、復旧コード消費、TOTP 設定、メールログイン設定などがあれば、旧 DB に安全に反映してから旧方式を正本に戻します。source と target のバックアップを保持し、単純な上書き復元で新しい更新を失わないようにします。
