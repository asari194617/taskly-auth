# パスキー登録・管理仕様

認証設定の変更条件は [追加確認の仕様](security-step-up.md) に従います。以下の「ログインから10分以内」は `AUTH_SECURITY_STEP_UP_REQUIRED=0` の条件です。`1` ではログイン時刻に代わり、同じ有効な認証セッションでの追加確認から5分以内を要求します。現在のパスワードや TOTP コードなど、操作固有の確認条件も引き続き必要です。

Status: implemented behind migration gate; existing-passkey device login verified in an isolated database (2026-09-25)

## 目的と互換性

認証 UI `https://auth.taskly.iy0306.com` で、移行済みの既存パスキーによるログインに加え、新規登録・一覧・名前変更・削除を提供する。WebAuthn RP ID は旧 Taskly と同じ `taskly.iy0306.com` に固定し、登録・認証の検証 origin は `AUTH_UI_URL` と完全一致させる。ユーザーハンドは移行元の十進ユーザー ID、クレデンシャル ID は従来の base64 形式、公開鍵は `webauthn:v1:` 形式で保存する。既存行を上書き・再登録しない。

## 操作と認可

- 管理画面は認証設定内の `GET /security?section=passkeys`。既存の `GET /passkeys` とログインフローの `next=passkeys` はこの画面へ転送する。認証 API の有効な `taskly_auth_session` がなければ、管理画面からログインし直す。任意 URL への遷移は許可しない。
- `GET /auth/passkeys` はログイン済みの本人の ID・表示名・作成日時だけを返す。公開鍵・署名カウンタは返さない。
- `POST /auth/passkey/register/begin` は本人の既存クレデンシャルを exclude list に含む登録オプションと5分の一回限りの要求 ID を返す。
- `POST /auth/passkey/register/finish` は要求 ID とブラウザの attestation を検証し、同一ユーザーの新規行として保存する。要求 ID は成功・失敗を問わず一度だけ使える。
- `POST /auth/passkey/rename` と `POST /auth/passkey/delete` は本人のパスキー ID だけを対象にする。表示名は空白除去後1〜64文字。最後のパスキーを削除する際は、パスワードまたはメールコードでログイン可能な場合に限る。
- 登録・名前変更・削除は直近10分以内に認証サービスへログインしたセッションだけを許可する。期限切れの場合、管理画面から再ログインする。
- 全 API は認証 UI と一致する `Origin` と `X-Auth-UI: 1` を要求する。認証・移行ゲート `AUTH_MIGRATION_READY=0` の本番環境では503を返す。

## 失敗と展開

パスキー登録の署名・origin・RP ID・チャレンジ不一致は登録を拒否する。DB 保存エラーは Discord に通知し、ユーザーに公開鍵や秘密情報を返さない。既存パスキーの認証・登録は開発環境の CI と内部ステージングで確認する。2026-09-25 に隔離 DB の既存パスキーで新 origin からの実端末ログインを確認した（[検証記録](production-migration.md)）。新規登録・管理の実端末検証と、旧 Taskly 設定画面から新管理画面への導線は公開切り替え前に確認する。旧 API のパスキー登録・名前変更・削除・署名カウンタ更新を停止または auth DB 側へ移管するまで、本番の認証データはコピーせず公開503を維持する。
