# キーは lambda/{関数名}.zip と layers/pdf-processor.zip の固定名で、compute モジュールが data "aws_s3_object" で参照する
# documents と分けるのは、EventBridge 通知の対象から外し、失効ルールと権限 (Lambda ロールはこのバケットに触らない) を混ぜないため
# アクセスログは documents と同じ判断で有効化しない
#trivy:ignore:AVD-AWS-0089
resource "aws_s3_bucket" "artifacts" {
  bucket        = local.artifacts_bucket_name
  force_destroy = local.is_disposable
}

resource "aws_s3_bucket_public_access_block" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# documents と同じく SSE-S3 にし、KMS は使わない (zip は公開リポジトリのビルド成果物で機密ではない)
#trivy:ignore:AVD-AWS-0132
resource "aws_s3_bucket_server_side_encryption_configuration" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# バージョニングを有効化する (documents とは逆の判断)
# zip は固定キーへ上書きで再アップロードするため、キーだけでは Terraform が差し替えを検出できない
# 上書きのたびに新しい version_id が振られ、compute が s3_object_version に渡すことで plan が関数と Layer の更新を検出する
resource "aws_s3_bucket_versioning" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id

  versioning_configuration {
    status = "Enabled"
  }
}

# 非現行バージョンのルールはバージョニングが有効になってから適用する必要があり、両リソースは属性で結ばれないため depends_on で順序を固定する
resource "aws_s3_bucket_lifecycle_configuration" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id

  depends_on = [aws_s3_bucket_versioning.artifacts]

  # 現行バージョンは消さず、上書きで非現行になった旧 zip だけを失効させる
  rule {
    id     = "expire-noncurrent-versions"
    status = "Enabled"

    filter {}

    noncurrent_version_expiration {
      noncurrent_days = local.artifacts_noncurrent_expiration_days
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
