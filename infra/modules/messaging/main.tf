locals {
  name_prefix = "${var.env}-folio"

  # 起動対象のキーの形 (backend/internal/awsx/s3/keys.go の OriginalPDFKey と対応)
  # uploads/{jobId}/original.pdf だけを起点にし、work/ や outputs/ への書き込みでパイプラインが再起動する無限ループを防ぐ
  upload_key_wildcard = "uploads/*/original.pdf"

  # DLQ の保持期間 (秒)
  # dev は評価用で、起動失敗の調査は直後にしか行わないため 3 日にする (ロググループの保持と同じ)
  dlq_message_retention_seconds = 259200
}
