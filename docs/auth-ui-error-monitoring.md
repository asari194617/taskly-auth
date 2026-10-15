# 認証 UI のエラー監視

認証 UI は既存 Taskly の Sentry プロジェクトへ、`component:auth-ui` を付けて手動でエラーイベントを送る。UI の公開 DSN は `production-ui` environment の `VITE_SENTRY_DSN`、環境は `production`、release は `taskly-auth-ui@<commit SHA>`。ブラウザーへ公開する値に Sentry auth token や secret key を使わない。

## 収集対象

- パスキーの利用可否確認、開始、端末認証、完了の失敗（`auth.operation:passkey.*`）。
- 認証 API の通信失敗と HTTP 5xx（`api.network`、`api.server`）。
- ログイン送信やメールコード送信で捕捉した失敗。

利用できないブラウザーや `NotAllowedError`・`AbortError` は warning とする。`NotAllowedError` は利用者の中断・タイムアウト・ブラウザー制約を区別できないため、原因を断定しない。通常のパスワード不一致や既存セッションの期限切れなどの HTTP 401 を API 共通処理で一律に通知しない。パスキー・メールコード送信の失敗は個別の処理で HTTP 4xx も記録する。同一ページ内の同じ失敗を重複送信せず、API と画面で同じ例外を捕捉した場合も一度だけ送る。

調査時は `component:auth-ui`、`auth.operation`、`auth.error_name`、`auth.page`、`auth.endpoint`、`http.status_code`、`browser.name` とブラウザーバージョン・OS 名・release を使う。API パスとページは許可リストに一致するものだけを記録する。

## 送信しないデータ

パスワード、メールアドレス、認証コード、認証チャレンジ、パスキー署名・クレデンシャル、リクエスト・レスポンス本文、Cookie、URL クエリ・ハッシュ、元の例外メッセージ・スタック、User-Agent 全文は送らない。例外名も許可リストに限定する。

Sentry は独立した client と scope を使い、ブラウザー操作・通信の自動収集やセッションリプレイを有効化しない。`beforeSend` でイベントを許可した診断項目だけに再構成し、SDK によるユーザー情報・IP の推定も停止する。送信は Cookie を伴わず、Referer を送らない。Sentry の初期化や送信が失敗しても、画面の表示や認証処理は継続する。

## 検証

`npm --prefix apps/web run typecheck`、`npm --prefix apps/web test`、`npm --prefix apps/web run build` を実行する。単体テストは例外・URL・User-Agent・SDK scope に入れた機密値がイベントから除去されることを確認する。

ブラウザー検証では模擬 DSN と WebAuthn・認証 API を使い、パスキー非対応、端末認証の拒否、通信失敗、HTTP 5xx、HTTP 401 の非通知、重複抑制、Sentry 通信遮断時の操作継続を確認する。公開後は Cloudflare の CSP `connect-src` に上記 DSN の ingest origin が含まれ、送信が受理されることを確認する。Sentry の利用制限、拡張機能・広告ブロッカーやオフラインにより通知できない場合はある。
