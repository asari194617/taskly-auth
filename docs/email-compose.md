# 認証メールの構成と配信

taskly-auth は Taskly の `email-compose` サービスに gRPC でメール本文を依頼し、
返された件名、HTML、プレーンテキストを `multipart/alternative` メールとして SMTP で送信する。
ログインコード、認証設定の追加確認コード、メールアドレス確認コードはそれぞれ専用の
テンプレートを使う。言語は認証 DB の `users.notification_lang` (`ja` / `en`) に従う。
コードはすべて10分で失効し、テンプレートにも10分を指定する。

本番では `EMAIL_COMPOSE_GRPC_ADDR=email-compose:50051` を使う。
両サービスは Docker の `infra_default` ネットワークに接続する。開発時に別の
アドレスを使う場合だけこの環境変数を変更する。composer が利用できない、または
件名・HTML・テキストのいずれかが空の場合、送信せずリクエストを503で終了する。
不正確な代替文面への切り替えは行わない。

Taskly 側の新しい `email-compose` イメージを先にデプロイし、
`ComposeSecurityCode` と `ComposeEmailVerificationCode` の提供を確認してから
taskly-auth をデプロイする。旧 Taskly サーバーの `ComposeLoginCode` 呼び出しは
`expiry_minutes` を省略するため、従来どおり5分の表記になる。
