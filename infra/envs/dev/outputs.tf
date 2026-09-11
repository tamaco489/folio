output "documents_bucket_name" {
  description = "Name of the S3 bucket that holds uploads/, work/, and outputs/."
  value       = module.storage.documents_bucket_name
}

output "jobs_table_name" {
  description = "Name of the DynamoDB jobs table."
  value       = module.storage.jobs_table_name
}

output "artifacts_bucket_name" {
  description = "Name of the versioned S3 bucket to upload the Lambda zips (lambda/*.zip) and the Layer zip (layers/pdf-processor.zip) to."
  value       = module.storage.artifacts_bucket_name
}

output "state_machine_arn" {
  description = "ARN of the pipeline state machine."
  value       = module.pipeline.state_machine_arn
}

output "github_actions_role_arn" {
  description = "ARN of the role that cd-backend.yml assumes via OIDC; register it as the GitHub secret AWS_ROLE_ARN."
  value       = module.iam.github_actions_role_arn
}
