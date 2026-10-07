# ログイン履歴の取得と開示用 CSV

認証成立時に `auth_login_history` とセッションを同一トランザクションで保存する。日時は UTC、ログイン方法はサーバーが検証した方法、IP と位置はその時点で記録する。User-Agent はクライアントの申告値として最大 1024 文字を保存する。`GET /auth/login-history` はログイン中の本人の直近 50 件を表示し、`GET /auth/login-history/export` は本人の**保存済み全件**を時系列の UTF-8 CSV として返す。両 API は UI Origin、`X-Auth-UI: 1`、本人の有効なセッションを要する。CSV にはアカウントのユーザー ID、イベント ID、UTC 日時、認証方法、IP、IP の取得元、都市・地域・国、位置情報データの取得元・版・MMDB SHA-256、User-Agent を含める。CSV のテキスト値は表計算ソフトで数式として実行されないよう処理する。

従来はユーザーごとに 50 件を超えると古い履歴を削除していた。新しい実装はこの削除を停止する。**既に削除された過去の履歴は復元できない。** 既存行には IP・位置・User-Agent がなく、CSV では空欄となる。退会時は[アカウント退会・消去の同期](account-lifecycle-sync.md)に従って履歴を削除する。

## IP の信頼境界

本番の Go サービスはホスト `127.0.0.1:3070` の Nginx 経由で公開する。既存の Nginx vhost は `X-Real-IP` を TCP 接続元で上書きし、Cloudflare の `CF-Connecting-IP` を透過する。`.env.production` に `AUTH_HISTORY_TRUSTED_PROXY_PEER=172.18.0.1` を設定する。この値は現在の本番 Docker gateway をホスト公開ポート経由の一時コンテナで確認した値であり、別環境へそのまま流用しない。Go はソケット接続元が設定した単一 IP と完全に一致するときだけ `X-Real-IP` を読む。`X-Real-IP` が[公式 Cloudflare IP 範囲](https://www.cloudflare.com/ips/)に含まれ、かつ `CF-Connecting-IP` が有効な公開 IP の場合、その値を訪問者 IP として記録する。Cloudflare 接続元なのに `CF-Connecting-IP` が欠落・不正なら IP は不明とする。Cloudflare 以外の接続元は `nginx_remote_addr` として IP を保存する。`X-Forwarded-For` と `X-Auth-*` は使わない。

Go は `AUTH_HISTORY_TRUSTED_PROXY_PEER` にプライベートまたはループバックの**単一 IP**だけを認める。共有 Docker ネットワークの別コンテナから直接接続しても接続元は gateway と異なるため、偽の `X-Real-IP` や `CF-Connecting-IP` を無視する。ホスト公開ポートは `127.0.0.1` に束縛されている。Docker ネットワーク構成を変更した際は gateway と実際の `RemoteAddr` を再確認する。Cloudflare の IP 範囲は Go に固定しており、[公式 IPv4](https://www.cloudflare.com/ips-v4)・[公式 IPv6](https://www.cloudflare.com/ips-v6)と定期的に照合する。新しい Cloudflare edge 範囲を認識するまでは、その edge IP が `nginx_remote_addr` として保存され得る。この取得元の IP からは位置を推定しない。

Cloudflare の Pseudo IPv4 **Overwrite Headers** 設定状態は未確認。`CF-Connecting-IP` が `240.0.0.0/4` の疑似 IPv4 なら、実際の利用者 IP として保存せず、IP と位置を不明にする。`CF-Connecting-IPv6` は信頼済み経路として検証していないため採用しない。

## ローカルの DB-IP City Lite による位置推定

位置は、サーバーが上記の方法で確定した `cloudflare` 由来の訪問者 IP、または真の公開 `direct_socket` 接続元だけを対象に、ローカルの [DB-IP City Lite MMDB](https://db-ip.com/db/download/ip-to-city-lite)で推定する。`nginx_remote_addr` は直結利用者と未認識の Cloudflare edge を区別できないため、IP は保存しても位置を不明にする。プライベート IP、ループバック、データベースに該当行がない IP も位置は不明とする。利用者 IP を外部の位置情報 API に送信しない。位置は IP からの概算で、VPN、通信事業者の出口、データの誤差により実際の所在地と一致しない。User-Agent も端末が自由に申告できるため、端末の確証にはならない。

DB-IP City Lite は月次更新、[CC BY 4.0](https://creativecommons.org/licenses/by/4.0/)で提供され、表示画面に [DB-IP](https://db-ip.com) への帰属リンクが必要。ログイン履歴ページにそのリンクを表示し、CSV にも `location_source_url` を含める。都市・地域は MMDB の英語名、国は ISO 3166-1 alpha-2 を保存する。位置を保存する行には `geo_source=dbip_city_lite`、MMDB のビルド日時から得た `geo_db_release=YYYY-MM`、読み込んだ**同じバイト列**の `geo_db_sha256` を固定する。CSV の出力時に現在の DB で位置を再計算しない。

### 本番導入と月次更新

本番の `compose.prod.yaml` はホストの `./geoip` をコンテナの `/geoip` に読み取り専用でマウントする。`.env.production` で `AUTH_GEOIP_MMDB_PATH=/geoip/dbip-city-lite-2026-10.mmdb` を設定する。未設定なら位置は不明のまま動作し、設定したファイルが欠損・破損・別種の MMDB の場合は起動を失敗させる。DB 本体を Git やイメージには含めない。

2026 年 10 月版を取得する例。公式ページに記載された SHA-1 は**解凍後の MMDB**に対する値であり、圧縮ファイルには一致しない。新しい月には URL の年月と公式ページのチェックサムを更新する。

```sh
cd /home/ubuntu/taskly-auth
mkdir -p geoip
curl -fL https://download.db-ip.com/free/dbip-city-lite-2026-10.mmdb.gz -o geoip/dbip-city-lite-2026-10.mmdb.gz
gzip -dc geoip/dbip-city-lite-2026-10.mmdb.gz > geoip/dbip-city-lite-2026-10.mmdb.tmp
printf '%s  %s\n' df17bd24390108ed1d24dcb7718dc60f6b9abd4f geoip/dbip-city-lite-2026-10.mmdb.tmp | sha1sum -c -
mv geoip/dbip-city-lite-2026-10.mmdb.tmp geoip/dbip-city-lite-2026-10.mmdb
chmod 0644 geoip/dbip-city-lite-2026-10.mmdb
```

月次更新では新しい**版名のファイル**を検証してから `.env.production` のパスを切り替え、`docker compose -f compose.prod.yaml up -d --no-deps --force-recreate --wait auth` で読み直す。前版は切り戻し用に保管できる。DB-IP のデータ形式は[公式 MMDB スキーマ](https://db-ip.com/db/format/ip-to-city-lite/mmdb.html)を参照。
