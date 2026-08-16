output "state_machine_arn" {
  description = "ARN of the pipeline state machine (the EventBridge rule target and the resource of states:StartExecution in the messaging module)."
  value       = aws_sfn_state_machine.pipeline.arn
}

output "state_machine_name" {
  description = "Name of the pipeline state machine."
  value       = aws_sfn_state_machine.pipeline.name
}
