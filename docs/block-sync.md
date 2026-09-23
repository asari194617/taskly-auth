# ブロック状態の同期仕様

Status: implementation specification (2026-09-23)

## 目的

Taskly の管理画面で行うブロック・解除を、認証 DB の `blocks` に反映する。
認証サービスはログイン、OAuth token、userinfo、認証 UI session の各経路で同じ状態を参照し、
ブロック済み利用者へ新しい認証結果を発行しない。

## 境界と API

- 管理操作と監査ログの正本は Taskly app DB に残す。
- 認証判定用の投影を auth DB に保持する。
- Taskly API だけが `PUT /internal/users/{id}/block` を呼ぶ。
- `Authorization: Bearer <AUTH_INTERNAL_TOKEN>` を必須とする。token は OAuth client secret と分離し、
  Git、ログ、Discord 通知、レスポンスに含めない。
- JSON は `active`, `reason`, `expires_at` だけを受け付ける。
  `expires_at` は RFC 3339、無期限は `null` とする。解除時は reason と expires_at を送らない。
- 同じ要求を再送しても同じ有効状態になるようにする。有効な重複行は残さない。

## 更新順序と失敗時の扱い

Taskly は app DB の更新を確定してから auth API を呼ぶ。ブロック同期が失敗した場合、Taskly の
業務 API は先にブロックされる。解除同期が失敗した場合、認証サービス側のブロックが残る。
どちらもアクセスを広げない方向に失敗する。管理 mutation は同期失敗を成功扱いにせず、同じ操作を
再実行できるエラーを返す。

認証 API は要求された状態を冪等に適用する。ブロック時は認証 UI session、OAuth code、access token、
refresh token、認証 challenge を同じ transaction で失効させる。解除は新しいログインを許可するが、
以前に失効した token や session を復活させない。

## 段階導入

- Taskly の `AUTH_BLOCKS_EXTERNAL` は既定で `0` とする。
- `1` にする前に `AUTH_BLOCK_SYNC_URL` と両サービスの `AUTH_INTERNAL_TOKEN` を一致させる。
- 開発 Compose は内部 URL `http://auth:3067` を利用できる構成にする。本番では公開 FQDN を経由せず、
  同じ private Docker network の `http://auth:3067` を利用する。
- auth DB が空の内部 stage と現在の本番ではフラグを有効にしない。
- 最終コピー後、既存 `blocks` の照合、ブロック・解除・再送・通信失敗の確認を行ってから有効化する。

## 監視と秘密情報

認証サービスは不正な internal token を 401、対象利用者不在を 404、不正 JSON を 400、DB 失敗を
500で返す。5xx は既存の集約済み Discord alert 対象とする。通知には利用者 ID、理由、メール、token を
含めない。
