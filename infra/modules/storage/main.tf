locals {
  name_prefix = "${var.env}-folio"

  # バケット名は全アカウントで一意である必要があるため、末尾にアカウント ID を付ける
  documents_bucket_name = "${local.name_prefix}-documents-${var.account_id}"
  jobs_table_name       = "${local.name_prefix}-jobs"

  # S3 キーの第 1 階層 (backend/internal/awsx/s3/keys.go と対応)
  #   uploads/{jobId}/original.pdf  受領した PDF (イベント発火点)
  #   work/{jobId}/...              ページ画像・Textract 生出力・中間 JSON (finalizer 完了後は参照されない)
  #   outputs/{jobId}/...           両経路の抽出結果と比較結果
  # バケットを分けずプレフィックスで役割を分けるのは、通知を uploads/ に限定できれば十分なため
  # ライフサイクルで参照するのは work/ だけなので、他 2 つは local にしない
  prefix_work = "work/"

  # work/ の失効日数
  # work/ は 1 論文で数十 MB のページ画像を含み、finalizer 完了後は読まれない
  # FAILED からの再投入は work/ の中間結果を finalizer が拾うため、失効後の再投入は Parallel からの作り直しになるが、動作としては正しい
  # 評価用の再投入は数日から数週間の範囲に収まる想定なので 30 日とし、変数にはしない
  work_expiration_days = 30

  # 中断したマルチパートアップロードの残骸を消すまでの日数
  # 残骸は一覧に出ないまま課金され続けるため、プレフィックスによらずバケット全体に掛ける
  abort_incomplete_multipart_days = 7

  # dev は評価用の使い捨て環境として扱い、それ以外は誤削除から保護する
  # dev でも消えて困るのは outputs/ だが、jobId が SHA-256 で決まるため同じ PDF の再投入で同じ結果を作り直せる
  is_disposable = var.env == "dev"
}

# ------------------------------------------------------------------------------
# S3: PDF・中間データ・成果物を 1 バケットに置く
# ------------------------------------------------------------------------------

resource "aws_s3_bucket" "documents" {
  bucket        = local.documents_bucket_name
  force_destroy = local.is_disposable
}

resource "aws_s3_bucket_public_access_block" "documents" {
  bucket = aws_s3_bucket.documents.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# SSE-S3 (AES256) を既定にし、KMS は使わない
# 評価段階では鍵の分離や監査の要件がなく、SSE-KMS にすると Lambda と Textract のロールに kms:Decrypt を配る必要が生じる
# ページ画像のような小さなオブジェクトの大量書き込みで KMS API の課金も乗る
resource "aws_s3_bucket_server_side_encryption_configuration" "documents" {
  bucket = aws_s3_bucket.documents.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# バージョニングは有効化しない (aws_s3_bucket_versioning を定義しない)
# jobId が PDF の SHA-256 なので同じキーには同じ内容しか来ず、outputs/ の上書きは finalizer の再実行 (同じ結果) だけのため
# 世代を保つ理由がなく、有効化すると work/ の失効ルールに noncurrent 版の扱いが加わって複雑になる

# EventBridge へのイベント送信を有効化する
# S3 側では通知を uploads/ に絞れない (EventBridge 通知はバケット全体が対象)
# uploads/ プレフィックスと .pdf サフィックスのフィルタは #28 (messaging) の EventBridge ルールのイベントパターン (detail.object.key の prefix / suffix) で行う
# フィルタを怠ると work/ や outputs/ への書き込みがパイプラインを再起動して無限ループになる
resource "aws_s3_bucket_notification" "documents" {
  bucket      = aws_s3_bucket.documents.id
  eventbridge = true
}

resource "aws_s3_bucket_lifecycle_configuration" "documents" {
  bucket = aws_s3_bucket.documents.id

  # work/ だけを失効させ、uploads/ (原本) と outputs/ (成果物) は残す
  rule {
    id     = "expire-work"
    status = "Enabled"

    filter {
      prefix = local.prefix_work
    }

    expiration {
      days = local.work_expiration_days
    }
  }

  rule {
    id     = "abort-incomplete-multipart-upload"
    status = "Enabled"

    filter {}

    abort_incomplete_multipart_upload {
      days_after_initiation = local.abort_incomplete_multipart_days
    }
  }
}

# ------------------------------------------------------------------------------
# DynamoDB: 処理状態と冪等性を管理するジョブテーブル
# ------------------------------------------------------------------------------

# 属性は設計で確定した 6 つ (jobId, status, filename, createdAt, updatedAt, errorReason) に限る
# attribute ブロックに書くのはキーに使う 3 つだけ (DynamoDB はキー以外のスキーマを持たず、キーに使わない属性を書くと plan が収束しない)
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
  point_in_time_recovery {
    enabled = !local.is_disposable
  }

  deletion_protection_enabled = !local.is_disposable
}
