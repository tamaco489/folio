output "documents_bucket_name" {
  description = "Name of the S3 bucket that holds uploads/, work/, and outputs/."
  value       = module.storage.documents_bucket_name
}

output "jobs_table_name" {
  description = "Name of the DynamoDB jobs table."
  value       = module.storage.jobs_table_name
}
