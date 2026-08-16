output "textract_completion_topic_arn" {
  description = "ARN of the SNS topic that receives Textract job completion notifications (passed to Lambda as FOLIO_TEXTRACT_SNS_TOPIC_ARN and to the IAM module for the publish role policy)."
  value       = aws_sns_topic.textract_completion.arn
}

output "upload_trigger_rule_arn" {
  description = "ARN of the EventBridge rule that starts the pipeline on PDF uploads."
  value       = aws_cloudwatch_event_rule.upload_trigger.arn
}
