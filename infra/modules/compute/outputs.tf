output "function_arns" {
  description = "Map of function ARNs keyed by validator, preprocessor, textract-parser, bedrock-parser, finalizer."
  value       = { for key, f in aws_lambda_function.pipeline : key => f.arn }
}

output "function_names" {
  description = "Map of function names keyed by validator, preprocessor, textract-parser, bedrock-parser, finalizer."
  value       = { for key, f in aws_lambda_function.pipeline : key => f.function_name }
}

output "log_group_arns" {
  description = "Map of /aws/lambda/<function> log group ARNs keyed like function_arns; IAM statements for log streams should append :* to each value."
  value       = { for key, g in aws_cloudwatch_log_group.pipeline : key => g.arn }
}

output "pdf_processor_layer_arn" {
  description = "ARN of the current pdf-processor Layer version attached to validator and preprocessor."
  value       = aws_lambda_layer_version.pdf_processor.arn
}
