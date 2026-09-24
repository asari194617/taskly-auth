# ユーザー作成・プロフィール同期

## 目的

認証 DB 分離後の利用者 ID とメールアドレスの一意性を認証サービスで確定し、Taskly の管理者作成、CSV 一括作成、メール変更が二つの DB で異なる利用者を指さないようにする。Taskly DB は同じ ID のプロフィール・権限・同意・業務データ用 shadow 行を保持する。

## Internal API

OAuth client secret とは別の `AUTH_INTERNAL_TOKEN` を Bearer token として使用する。`AUTH_MIGRATION_READY=1` になるまで既存の internal API と同様に503を返す。

### 作成または再取得

`POST /internal/users`

```json
{
  "email": "user@example.com",
  "display_name": "Example User",
  "notification_lang": "ja"
}
```

- メールは trim と小文字化を行い、形式と255文字上限を検証する。
- 表示名は255文字以下、通知言語は `ja` または `en` とする。
- 新規なら認証 DB の auto increment で ID を採番し、201で `{ "id": 48, "created": true }` を返す。
- 同じメールが存在する場合は行を変更せず、200で既存 ID と `created: false` を返す。Taskly は同じ ID の shadow 行があれば再送成功、異なる ID の同メール行があれば整合性エラーとして扱う。
- 応答喪失後の再送で別 ID を採番しない。認証行だけ作成された状態から Taskly shadow 作成を再試行できる。

### 更新

`PATCH /internal/users/{id}`

本文は `email`, `display_name`, `notification_lang`, `email_code_allowed` のうち一つ以上を含む。

- 対象行をロックし、メール一意性と値を検証して更新する。同じ要求の再送は成功する。
- メール変更時は新旧メールの未使用コードを使用済みにし、旧 Taskly session、認証 session、challenge、WebAuthn session、OAuth code を削除し、OAuth access / refresh token を失効する。再認証後だけ新メールで利用できる。
- 存在しない ID は404、メール競合は409、不正値は400とする。

Taskly は認証 DB を先に更新し、その後 app DB を更新する。後段が失敗した場合は認証側の安全な状態を戻さず「同期保留」を返す。同じ管理操作の再送で app DB を収束させる。

## 段階導入

- Taskly: `AUTH_USERS_EXTERNAL=0`, `AUTH_USER_SYNC_URL`, `AUTH_INTERNAL_TOKEN`
- 既定値は `0` とし、開発環境と切り替え前の本番では従来の app DB 採番を維持する。
- 最終コピー後、認証 DB の auto increment が既存最大 ID より先を指すことを確認してから有効化する。
- `AUTH_USERS_EXTERNAL=1` と lifecycle、credential の各所有権フラグを同じ切り替え手順で管理する。

## 確認項目

1. 単一作成、同じメールの再送、CSV 内重複、既存メール、並行作成で ID が分岐しない。
2. Taskly shadow 作成失敗後の再送で同じ ID に収束する。
3. メール変更と再送、重複メール、Taskly 更新失敗を確認する。
4. メール変更後に旧 session、code、OAuth token が利用できない。
5. internal API の認証失敗、redirect、unsafe production URL、5xx の Discord アラートを確認する。
