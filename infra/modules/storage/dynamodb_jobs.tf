# 処理状態と冪等性を管理するジョブテーブル

# 属性は設計で確定した 6 つ (jobId, status, filename, createdAt, updatedAt, errorReason) に限る
# attribute ブロックに書くのはキーに使う 3 つだけ (DynamoDB はキー以外のスキーマを持たず、キーに使わない属性を書くと plan が収束しない)
# 暗号化は AWS 所有キーの既定のままにし、CMK は使わない (S3 と同じく鍵の分離や監査の要件がなく、kms:Decrypt の配布と KMS API の課金を避けるため)
#trivy:ignore:AVD-AWS-0025
resource "aws_dynamodb_table" "jobs" {
  name         = local.jobs_table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "jobId"

  # jobId は PDF の SHA-256 で、1 ジョブ = 1 レコードなのでソートキーは設けない
  attribute {
    name = "jobId"
    type = "S"
  }

  attribute {
    name = "status"
    type = "S"
  }

  # updatedAt は桁数固定の RFC 3339 文字列で、辞書順が時刻順に一致する
  attribute {
    name = "updatedAt"
    type = "S"
  }

  # status ごとのジョブを updatedAt の新しい順に引く
  # 一覧表示で filename と errorReason まで要るため ALL にする (KEYS_ONLY だと 1 件ごとにテーブルを読み戻すことになる)
  # status は値が 4 種類しかなくパーティションが偏るが、件数が増えるまでは複合キーにしない
  # GSI 内の hash_key / range_key は provider v6 で非推奨のため key_schema で書く (HASH を RANGE より先に置く必要がある)
  global_secondary_index {
    name            = "gsi-status-updatedAt"
    projection_type = "ALL"

    key_schema {
      attribute_name = "status"
      key_type       = "HASH"
    }

    key_schema {
      attribute_name = "updatedAt"
      key_type       = "RANGE"
    }
  }

  # TTL は設定しない
  # レコードが消えた後に同じ PDF が投入されると重複処理が走る
  # 文書は数か月後に再投入されることがあり、短期の TTL では冪等性を保てない

  # 状態テーブルは同じ PDF の再投入で復元できるため、使い捨ての dev では PITR を持たない
  #trivy:ignore:AVD-AWS-0024
  point_in_time_recovery {
    enabled = !local.is_disposable
  }

  deletion_protection_enabled = !local.is_disposable
}
