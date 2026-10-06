# アカウント情報

`/profile` は表示名の設定と現在のメールアドレスの確認を担当する。メール確認コードはログイン中のユーザーのアドレスにのみ送る。確認済み状態と未使用コードは `auth_email_verifications` に保存し、アドレス変更・退会・データ消去時に削除する。

表示名の更新には直近10分以内の認証が必要。auth DB を更新してから `TASKLY_PROFILE_SYNC_URL` の Taskly API へ `AUTH_INTERNAL_TOKEN` 付きで shadow row 同期を送る。同期失敗時は 503 `profile_sync_pending` を返し、同じ表示名の再保存で再試行できる。

本番でプロフィールを公開する前に Taskly 側のユーザー移行を完了し、`AUTH_MIGRATION_READY=1`、`AUTH_USERS_EXTERNAL=1`、`AUTH_PROFILE_EXTERNAL=1` と Web の `PUBLIC_AUTH_PROFILE_URL` を一緒に切り替える。移行未完了の環境では認証 API が 503 を返すので、この画面への本番導線を有効化しない。
