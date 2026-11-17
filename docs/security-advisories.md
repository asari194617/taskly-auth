# 依存関係のアドバイザリ確認（2026-11-17）

- UI の `deepmerge@4.3.1` に対する [GHSA-gcqx-w48m-w2h4](https://github.com/advisories/GHSA-gcqx-w48m-w2h4) は 2026-11-16 に撤回された。修正版は公開されておらず、テーマライブラリを破壊的にダウングレードする根拠もなくなったため、既存バージョンを維持した。UI の型チェック、26件のテスト、ビルドは Node.js 26 で成功した。
- `golang.org/x/net/html` の [GO-2026-6866](https://pkg.go.dev/vuln/GO-2026-6866) と [GO-2026-6860](https://pkg.go.dev/vuln/GO-2026-6860) は v0.62.0 より前に該当する。認証コードの gRPC クライアントを削除して `go mod tidy` を実行した結果、`x/net` は依存グラフからなくなった。
- `golang.org/x/crypto/openpgp` の [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) はパッケージ自体が非推奨で、全バージョンに既知の問題がある。auth のコードは `openpgp` を import していない。最新 main で更新済みの `x/crypto v0.59.0` を維持した。暗号ライブラリ全体の警告だけを理由に `bcrypt` を外さない。

ローカルで `go test ./...` と `go vet ./...` を実行済み。`npm audit` は外部 npm registry へ非公開アプリの依存メタデータを送るため自動承認で拒否され、実行していない。`govulncheck` はこの環境にインストールされていないため再実行していない。運用側で許可した診断環境から両方を再実行する。
