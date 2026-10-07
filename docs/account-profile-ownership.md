# アカウント情報

`/profile` は表示名の設定と現在のメールアドレスの確認を担当する。メール確認コードはログイン中のユーザーのアドレスにのみ送る。確認済み状態と未使用コードは `auth_email_verifications` に保存し、アドレス変更・退会・データ消去時に削除する。

認証サービスのログイン用メールコードの照合に成功したときも、現在のユーザー ID とメールアドレスを行ロックで確認し、コード消費と同じトランザクションで確認済みにする。パスワード・パスキー・認証アプリ・復旧コードだけのログインではメール到達を証明しない。移行済みの旧 `login_codes` はユーザー ID を持たず、メール変更や退会でも `consumed_at` が設定されるため、一括で確認済みへ戻す根拠として使用しない。

表示名の更新には直近10分以内の認証が必要。auth DB を更新してから `TASKLY_PROFILE_SYNC_URL` の Taskly API へ `AUTH_INTERNAL_TOKEN` 付きで shadow row 同期を送る。同期失敗時は 503 `profile_sync_pending` を返し、同じ表示名の再保存で再試行できる。本番の Docker network では Taskly API のサービス別名 `server` を使い、`TASKLY_PROFILE_SYNC_URL=http://server:8080` とする。公開 URL を経由せず、`AUTH_INTERNAL_TOKEN` は両サービスで一致させる。

本番でプロフィールを公開する前に Taskly 側のユーザー移行を完了し、`AUTH_MIGRATION_READY=1`、`AUTH_USERS_EXTERNAL=1`、`AUTH_PROFILE_EXTERNAL=1` と Web の `PUBLIC_AUTH_PROFILE_URL` を一緒に切り替える。移行未完了の環境では認証 API が 503 を返すので、この画面への本番導線を有効化しない。
