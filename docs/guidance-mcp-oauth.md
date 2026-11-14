# ChatGPT 企業ガイダンス連携の認証

既存の Taskly-auth を認可サーバーとして使う。ChatGPT が受け取るトークンは Guidance MCP 専用であり、Taskly のブラウザーセッションや既存 Web クライアントのトークンを共有しない。

| 用途 | 固定クライアント ID | 権限・対象 |
| --- | --- | --- |
| ChatGPT | `nnct-guidance-chatgpt` | PKCE S256、登録済み callback 完全一致、MCP resource のみ |
| Guidance サーバー | `nnct-guidance-mcp-backend` | 専用 Basic 認証。MCP introspection と限定 token exchange のみ |

ChatGPT の認可 scope は `guidance:read` を必須とし、`guidance:bookmarks:write` と `guidance:tasks:write` を個別に要求できる。ログイン済みでも `/consent` 画面で許可・拒否を選ぶ。認可要求はユーザー・クライアント・scope・resource・期限に束縛され、既存 UI origin、`X-Auth-UI`、使い捨て CSRF トークンを検証する。許可を消費するトランザクションで一度だけ認可コードを作る。

## 設定と配備順序

新しい設定をすべて未指定にした状態では無効。既存のログイン動作を維持して先にコードと nullable カラムを配備できる。

| 環境変数 | 設定 |
| --- | --- |
| `OAUTH_MCP_ENABLED` | 有効化時だけ `1`。既定 `0` |
| `OAUTH_MCP_REDIRECT_URI` | Plugin 管理画面に表示された正確な HTTPS callback |
| `OAUTH_MCP_RESOURCE` | Guidance API の canonical URL + `/mcp` |
| `OAUTH_MCP_TASKLY_RESOURCE` | Taskly API の canonical URL + `/integrations/nnct-guidance/tasks` |
| `OAUTH_MCP_BACKEND_SECRET` | 独立した32文字以上のランダム値。既存の OAuth secret と共有しない |

無効時に他の新設定が残っている場合、または有効時に設定が欠けている場合は起動を拒否する。本番では HTTPS のみ許可し、callback、resource の query・fragment・URL 内の資格情報を拒否する。値は環境設定に保存し、リポジトリやログに含めない。

Auth、Taskly の専用 bridge、Guidance MCP、Plugin の順に整える。認証 UI の R2 配備には新しい `/consent` ページを含める。callback が確定するまでは新しい認可を有効にしない。CIMD・DCR は実装しておらず、metadata に宣言しない。

## トークンの扱い

認可要求・認可コード・access token・refresh token に resource を保存し、コード交換・更新での対象差し替えを拒否する。access token は15分、ローテーションする refresh token は30日。既存クライアントの `taskly:login` と `taskly-api` audience は維持し、新しい MCP クライアントでは使えない。

Guidance サーバーは MCP token を検証してから、Taskly の完了状態が必要な場合だけ RFC 8693 の token exchange を行う。交換先は設定済み Taskly bridge のみ、scope は `guidance:tasks:read` または `guidance:tasks:write` のどちらか一つ。書き込みには親の書き込み許可も必要。交換トークンの寿命は親の残り時間と60秒の短い方で、refresh token を発行しない。

交換トークンは親 access token のハッシュと family を保持する。Taskly 用 introspection は親の期限・失効・scope・resource・同一ユーザー・family、およびアカウント削除／ブロックを毎回確認する。親 access token の失効、refresh family の失効や再利用検知で交換トークンも利用できなくなる。通常の認証 UI のログアウトは従来どおり SSO セッションを終了し、発行済み OAuth grant の取り消しとは分かれる。

## DB 更新と検証

`schema.sql` と既存の `ensureOAuthSchema` 起動時更新を使う。Atlas は導入していない。追加する7列はすべて nullable で、既存行は変更しない。

- `oauth_authorization_requests`: `resource_uri`、`consent_user_id`、`consent_csrf_hash`
- `oauth_codes`: `resource_uri`
- `oauth_access_tokens`: `resource_uri`、`parent_token_hash`
- `oauth_refresh_tokens`: `resource_uri`

旧バージョンへ戻す場合も列を削除しない。旧 API は新しい MCP クライアントを認証できないため連携は停止するが、既存 Web の grant は維持される。

通常の検査は `go test -race ./...` と `go vet ./...`。実 MySQL 検査は loopback 上の専用 `taskly_auth_mcp_test` database を用意し、`AUTH_MCP_TEST_DSN` を設定して `go test -race -count=1 -coverprofile=mcp-coverage.out ./...` を実行する。テストは本番・通常開発 DB 名を拒否する。認可の二重送信、CSRF、別ユーザー、PKCE、resource の差し替え、refresh 再利用、親と family の失効、ブロック・期限切れを確認する。

CI の MySQL job はファイルによる実行制限を設けず、すべての PR・main push で実行する。新しい MCP OAuth の statement coverage を80%以上で検査し、既存コードを含む全体値と分けて PR と job summary に表示する。UI の型検査・単体テスト・R2 静的ビルドも既存 CI で実行する。

参照: [OpenAI の認証仕様](https://developers.openai.com/plugins/build/auth)、[MCP Authorization](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization)、[RFC 8693](https://www.rfc-editor.org/rfc/rfc8693.html)。
