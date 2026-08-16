locals {
  name_prefix = "${var.env}-folio"

  # 起動対象のキーの形 (backend/internal/awsx/s3/keys.go の OriginalPDFKey と対応)
  # uploads/{jobId}/original.pdf だけを起点にし、work/ や outputs/ への書き込みでパイプラインが再起動する無限ループを防ぐ
  upload_key_wildcard = "uploads/*/original.pdf"

  # DLQ の保持日数
  # 起動失敗の取りこぼしを後から調べられるよう SQS の上限 (14 日) まで残す
  dlq_message_retention_seconds = 1209600
}
