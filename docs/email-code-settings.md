# メールコード許可設定仕様

Status: implementation in progress; production migration gate remains closed (2026-09-22)

## 目的

`users.email_code_allowed` の本人向け変更先を認証 DB に移す。この値はメールコードの発行・検証と、パスワード認証後にメールコードを第2要素として使えるかの両方に影響する。既存のユーザー ID、パスワード、パスキー、TOTP、復旧コードは変更しない。

## API と画面

- `GET /auth/email-code/settings` は有効・無効だけを返す。`POST /auth/email-code/settings` は `{"enabled":true|false}` の厳密な JSON を受ける。
- 読み取りは有効な認証サービスセッション、更新は直近10分以内にログインしたセッションを要求する。どちらも認証 UI と完全一致する `Origin` と `X-Auth-UI: 1` を要求する。
- 無効化は、同じユーザーにパスワードまたはパスキーがある場合に限る。TOTP と復旧コードだけは代替手段と数えない。ユーザー行とパスキー行をロックして判定・更新し、無効化時は認証サービスで未使用のメールコードを同じトランザクションで消費済みにする。
- 変更が失敗した場合は許可設定とコードの状態を戻す。DB エラーは Discord に通知する。画面は `/security` に設定を表示する。
- 本番 `AUTH_MIGRATION_READY=0` の API は503を返す。

## 切り替え条件

Taskly Web は `PUBLIC_AUTH_EMAIL_CODE_URL=https://auth.taskly.iy0306.com/security` のビルドで本人設定へのリンクを表示し、旧トグルを隠す。Taskly API の `AUTH_EMAIL_SETTINGS_EXTERNAL=1` は本人用 GraphQL `setAuthSettings` と REST `POST /settings/auth` を拒否する。両設定は既定で無効にする。管理者の `adminUserUpdate` など旧 DB へ書く経路、旧ログインとコード使用経路が残る間は最終コピーと公開切り替えを行わない。
