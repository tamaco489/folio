output "statemachine_role_arn" {
  description = "ARN of the execution role for the Step Functions state machine."
  value       = aws_iam_role.statemachine.arn
}

output "lambda_validate_role_arn" {
  description = "ARN of the execution role for the validator Lambda."
  value       = aws_iam_role.lambda_validate.arn
}

output "lambda_preprocess_role_arn" {
  description = "ARN of the execution role for the preprocessor Lambda."
  value       = aws_iam_role.lambda_preprocess.arn
}

output "lambda_parser_role_arn" {
  description = "ARN of the execution role shared by the textract-parser and bedrock-parser Lambdas."
  value       = aws_iam_role.lambda_parser.arn
}

output "lambda_finalize_role_arn" {
  description = "ARN of the execution role for the finalizer Lambda."
  value       = aws_iam_role.lambda_finalize.arn
}

output "textract_publish_role_arn" {
  description = "ARN of the role that Textract assumes to publish completion notifications to SNS (passed to Lambda as FOLIO_TEXTRACT_ROLE_ARN)."
  value       = aws_iam_role.textract_publish.arn
}
