output "documents_bucket_name" {
  description = "Name of the S3 bucket that holds uploads/, work/, and outputs/ (passed to Lambda as FOLIO_DOCUMENTS_BUCKET)."
  value       = aws_s3_bucket.documents.bucket
}

output "documents_bucket_arn" {
  description = "ARN of the documents bucket; object-level IAM statements should append /* to it."
  value       = aws_s3_bucket.documents.arn
}

output "jobs_table_name" {
  description = "Name of the DynamoDB jobs table (passed to Lambda as FOLIO_JOBS_TABLE)."
  value       = aws_dynamodb_table.jobs.name
}

output "jobs_table_arn" {
  description = "ARN of the jobs table; Query on the GSI additionally requires the index ARN (this value with /index/* appended), which the IAM module derives itself."
  value       = aws_dynamodb_table.jobs.arn
}

output "artifacts_bucket_name" {
  description = "Name of the versioned S3 bucket that holds the Lambda zips (lambda/*.zip) and the Layer zip (layers/pdf-processor.zip)."
  value       = aws_s3_bucket.artifacts.bucket
}

output "artifacts_bucket_arn" {
  description = "ARN of the artifacts bucket; object-level IAM statements should append /* to it."
  value       = aws_s3_bucket.artifacts.arn
}
