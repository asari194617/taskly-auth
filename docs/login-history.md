# ログイン履歴の取得と開示用 CSV

認証成立時に `auth_login_history` とセッションを同一トランザクションで保存する。日時は UTC、ログイン方法はサーバーが検証した方法、IP と位置はその時点のリクエストから記録する。User-Agent はクライアントの申告値として最大 1024 文字を記録する。`GET /auth/login-history` はログイン中の本人の直近 50 件を表示し、`GET /auth/login-history/export` は本人の**保存済み全件**を時系列の UTF-8 CSV として返す。両 API は UI Origin、`X-Auth-UI: 1`、本人の有効なセッションを要する。CSV にはアカウントのユーザー ID、イベント ID、UTC 日時、認証方法、IP、IP の取得元、都市・地域・国、位置情報の取得元、User-Agent を含める。CSV のテキスト値は表計算ソフトで数式として実行されないよう処理する。

従来はユーザーごとに 50 件を超えると古い履歴を削除していた。新しい実装はこの削除を停止する。**既に削除された過去の履歴は復元できない。** 既存行には IP・位置・User-Agent がなく、CSV では空欄となる。退会時は[アカウント退会・消去の同期](account-lifecycle-sync.md)に従って履歴を削除する。

## IP と位置の信頼境界

本番の Go サービスは `127.0.0.1:3070` の Nginx 経由で公開する。`deploy/nginx/taskly-auth-client-geo.conf` を Nginx の HTTP コンテキスト (`/etc/nginx/conf.d/`) に、更新した `deploy/nginx/taskly-auth-api.conf` を有効な vhost に配置する。Nginx は接続元が[Cloudflare 公開 IP 範囲](https://www.cloudflare.com/ips/)内か確認し、その場合だけ Cloudflare が設定する `CF-Connecting-IP` を訪問者 IP として転送する。その他の接続では実際の TCP 接続元を転送し、位置は空欄とする。Cloudflare の Pseudo IPv4 を **Overwrite Headers** にしている場合、`CF-Connecting-IP` は疑似 IPv4 になるため、この設定を無効にするか正規の IPv6 を取得できる信頼済み経路を別途設計する。Cloudflare の IP 範囲は[公式 IPv4](https://www.cloudflare.com/ips-v4)・[公式 IPv6](https://www.cloudflare.com/ips-v6)と定期的に照合する。

受信した `X-Forwarded-For` やクライアント送信の `X-Auth-*` は信用せず、Nginx がすべての `X-Auth-*` を上書きする。さらに共有 Docker ネットワークの他コンテナからの偽装を防ぐため、Nginx と Go は 32 文字以上のランダムな `AUTH_HISTORY_PROXY_TOKEN` を共有する。Go はプライベート／ループバック接続元かつトークン一致のときだけプロキシ用ヘッダーを受け入れる。`/etc/nginx/snippets/taskly-auth-history-proxy-secret-<任意>.conf` を root 所有・mode 0600 で作り、`set $auth_history_proxy_token "<トークン>";` を置く。Go の `.env.production` に同じ値を `AUTH_HISTORY_PROXY_TOKEN` として設定する。トークンや snippet を Git に追加しない。本番の Go はトークンが 32 文字未満なら起動しない。Nginx 側の値が空ならトークンヘッダーを送らず、Go は IP・位置用ヘッダーを無視する。

位置は Cloudflare の [Add visitor location headers Managed Transform](https://developers.cloudflare.com/rules/transform/managed-transforms/reference/) により訪問者 IP から推定された `cf-ipcity`、`cf-region`、`cf-ipcountry` を使う。Cloudflare の[設定手順](https://developers.cloudflare.com/rules/transform/managed-transforms/configure/)に従い、ゾーンの `add_visitor_location_headers` を有効にする。Cloudflare は国のみの [IP Geolocation 設定](https://developers.cloudflare.com/network/ip-geolocation/)も提供するが、都市と地域には Managed Transform が必要。位置は IP からの概算であり、VPN、通信事業者の出口、位置データの誤差などで実際の所在地と一致しない。User-Agent も端末が自由に申告できるため、端末の確証にはならない。

**位置情報の転送は初期状態で無効。** `taskly-auth-client-geo.conf` の `auth_history_geo_enabled` が `0` の間は、Cloudflare 経由でも位置は保存しない。Cloudflare の設定状態を API の [List Managed Transforms](https://developers.cloudflare.com/rules/transform/managed-transforms/configure/) で確認し、オリジンで次の偽装テストを行ってから `1` にする。

1. Nginx に一時的な診断ログを作り、`/healthz` のテストリクエストについて `$remote_addr`、`$auth_history_source`、`$auth_history_ip`、`$http_cf_ipcity`、`$http_cf_region`、`$http_cf_ipcountry` だけを記録する。Cookie、URL のクエリ、認証情報は記録しない。
2. Cloudflare の公開ホスト宛に、偽の `CF-IPCity`・`CF-Region`・`CF-IPCountry` ヘッダーを付けてリクエストする。ログの値が偽値と一致せず、Cloudflare が算出した値か空欄であることを確認する。
3. サーバー IP へ直接接続し同じヘッダーと偽の `CF-Connecting-IP` を付ける。`$auth_history_source=origin_socket`、`$auth_history_ip` が実際の接続元、位置が空欄となることを確認する。
4. 一時ログを削除し、`auth_history_geo_enabled` を `1` に変更する。`nginx -t` 後に reload し、実際の新規ログインで IP・位置・端末情報が履歴と CSV に記録されることを確認する。

Cloudflare の Managed Transform が無効、またはヘッダー上書きを確認できない場合は `0` を維持し、画面と CSV に不確かな場所を表示しない。
