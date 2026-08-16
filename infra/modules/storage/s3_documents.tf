# PDF (uploads/)・中間データ (work/)・成果物 (outputs/) を 1 バケットに置き、プレフィックスで分ける

# バージョニングは有効化しない (aws_s3_bucket_versioning を定義しない)
# jobId が PDF の SHA-256 なので同じキーには同じ内容しか来ず、outputs/ の上書きは finalizer の再実行 (同じ結果) だけのため
# 世代を保つ理由がなく、有効化すると work/ の失効ルールに noncurrent 版の扱いが加わって複雑になる
# アクセスログも有効化しない (aws_s3_bucket_logging を定義しない)
# 評価段階では監査の要件がなく、ログの保存先バケットとその失効管理が増えるだけのため
#trivy:ignore:AVD-AWS-0090
#trivy:ignore:AVD-AWS-0089
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
#trivy:ignore:AVD-AWS-0132
resource "aws_s3_bucket_server_side_encryption_configuration" "documents" {
  bucket = aws_s3_bucket.documents.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

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
