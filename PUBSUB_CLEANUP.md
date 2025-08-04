# PubSub エミュレーター クリーンアップガイド

## 概要
PubSubエミュレーターの全トピックとサブスクリプションを削除するためのcurlコマンド集です。
トラブルシューティングや初期化時に使用します。

## 前提条件
- PubSubエミュレーターが `localhost:8681` で動作している
- プロジェクトID: `test-project`

## 1. 現在の状態確認

### トピック一覧表示
```bash
curl -s http://localhost:8681/v1/projects/test-project/topics
```

### サブスクリプション一覧表示
```bash
curl -s http://localhost:8681/v1/projects/test-project/subscriptions
```

## 2. 全削除手順

### ステップ1: 全サブスクリプション削除
**注意**: サブスクリプションを先に削除する必要があります

```bash
# blockchain-requests-sub削除
curl -X DELETE http://localhost:8681/v1/projects/test-project/subscriptions/blockchain-requests-sub

# blockchain-results-sub削除
curl -X DELETE http://localhost:8681/v1/projects/test-project/subscriptions/blockchain-results-sub

# newlodev-blockchain-request-sub削除（存在する場合）
curl -X DELETE http://localhost:8681/v1/projects/test-project/subscriptions/newlodev-blockchain-request-sub

# newlodev-blockchain-result-sub削除（存在する場合）
curl -X DELETE http://localhost:8681/v1/projects/test-project/subscriptions/newlodev-blockchain-result-sub
```

### ステップ2: 全トピック削除

```bash
# blockchain-requests削除
curl -X DELETE http://localhost:8681/v1/projects/test-project/topics/blockchain-requests

# blockchain-results削除
curl -X DELETE http://localhost:8681/v1/projects/test-project/topics/blockchain-results

# newlodev-blockchain-request削除（存在する場合）
curl -X DELETE http://localhost:8681/v1/projects/test-project/topics/newlodev-blockchain-request

# newlodev-blockchain-result削除（存在する場合）
curl -X DELETE http://localhost:8681/v1/projects/test-project/topics/newlodev-blockchain-result
```

## 3. 削除確認

### 削除完了確認
```bash
# トピック確認（空の結果が返る）
curl -s http://localhost:8681/v1/projects/test-project/topics

# サブスクリプション確認（空の結果が返る）
curl -s http://localhost:8681/v1/projects/test-project/subscriptions
```

期待される結果:
```json
{}
```

## 4. 再作成（必要に応じて）

### トピック作成
```bash
# blockchain-requestsトピック作成
curl -X PUT http://localhost:8681/v1/projects/test-project/topics/blockchain-requests

# blockchain-resultsトピック作成
curl -X PUT http://localhost:8681/v1/projects/test-project/topics/blockchain-results
```

### サブスクリプション作成
```bash
# blockchain-requests-sub作成（ackDeadline: 15秒）
curl -X PUT http://localhost:8681/v1/projects/test-project/subscriptions/blockchain-requests-sub \
  -H "Content-Type: application/json" \
  -d '{"topic": "projects/test-project/topics/blockchain-requests", "ackDeadlineSeconds": 15}'

# blockchain-results-sub作成（ackDeadline: 10秒）
curl -X PUT http://localhost:8681/v1/projects/test-project/subscriptions/blockchain-results-sub \
  -H "Content-Type: application/json" \
  -d '{"topic": "projects/test-project/topics/blockchain-results", "ackDeadlineSeconds": 10}'
```

## 5. ワンライナースクリプト

### 全削除ワンライナー
```bash
# サブスクリプション全削除
curl -X DELETE http://localhost:8681/v1/projects/test-project/subscriptions/blockchain-requests-sub; \
curl -X DELETE http://localhost:8681/v1/projects/test-project/subscriptions/blockchain-results-sub; \
curl -X DELETE http://localhost:8681/v1/projects/test-project/subscriptions/newlodev-blockchain-request-sub; \
curl -X DELETE http://localhost:8681/v1/projects/test-project/subscriptions/newlodev-blockchain-result-sub

# トピック全削除
curl -X DELETE http://localhost:8681/v1/projects/test-project/topics/blockchain-requests; \
curl -X DELETE http://localhost:8681/v1/projects/test-project/topics/blockchain-results; \
curl -X DELETE http://localhost:8681/v1/projects/test-project/topics/newlodev-blockchain-request; \
curl -X DELETE http://localhost:8681/v1/projects/test-project/topics/newlodev-blockchain-result
```

### 再作成ワンライナー
```bash
# トピック作成
curl -X PUT http://localhost:8681/v1/projects/test-project/topics/blockchain-requests; \
curl -X PUT http://localhost:8681/v1/projects/test-project/topics/blockchain-results

# サブスクリプション作成
curl -X PUT http://localhost:8681/v1/projects/test-project/subscriptions/blockchain-requests-sub \
  -H "Content-Type: application/json" \
  -d '{"topic": "projects/test-project/topics/blockchain-requests", "ackDeadlineSeconds": 15}'; \
curl -X PUT http://localhost:8681/v1/projects/test-project/subscriptions/blockchain-results-sub \
  -H "Content-Type: application/json" \
  -d '{"topic": "projects/test-project/topics/blockchain-results", "ackDeadlineSeconds": 10}'
```

## 6. トラブルシューティング

### よくある問題
1. **サブスクリプションが削除できない**: トピックを先に削除しようとしている
   - 解決策: サブスクリプションを先に削除する

2. **404エラー**: リソースが存在しない
   - 解決策: 正常な状態（既に削除済み）

3. **接続エラー**: PubSubエミュレーターが動作していない
   - 解決策: エミュレーターを起動する

### エラー対処
```bash
# PubSubエミュレーター状態確認
curl -s http://localhost:8681/v1/projects/test-project

# 期待される結果（エミュレーターが動作中）
# HTTP 200 OK
```

## 使用例

### 問題が発生した場合の完全リセット
```bash
echo "PubSub エミュレーター完全リセット開始..."

# 1. 全サブスクリプション削除
echo "サブスクリプション削除中..."
curl -s -X DELETE http://localhost:8681/v1/projects/test-project/subscriptions/blockchain-requests-sub
curl -s -X DELETE http://localhost:8681/v1/projects/test-project/subscriptions/blockchain-results-sub
curl -s -X DELETE http://localhost:8681/v1/projects/test-project/subscriptions/newlodev-blockchain-request-sub
curl -s -X DELETE http://localhost:8681/v1/projects/test-project/subscriptions/newlodev-blockchain-result-sub

# 2. 全トピック削除
echo "トピック削除中..."
curl -s -X DELETE http://localhost:8681/v1/projects/test-project/topics/blockchain-requests
curl -s -X DELETE http://localhost:8681/v1/projects/test-project/topics/blockchain-results
curl -s -X DELETE http://localhost:8681/v1/projects/test-project/topics/newlodev-blockchain-request
curl -s -X DELETE http://localhost:8681/v1/projects/test-project/topics/newlodev-blockchain-result

# 3. 削除確認
echo "削除確認中..."
curl -s http://localhost:8681/v1/projects/test-project/topics
curl -s http://localhost:8681/v1/projects/test-project/subscriptions

echo "完全リセット完了!"
```