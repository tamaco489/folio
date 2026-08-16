variable "env" {
  description = "Environment identifier used as the resource name prefix (dev, stg, or prd)."
  type        = string

  validation {
    condition     = contains(["dev", "stg", "prd"], var.env)
    error_message = "env must be one of dev, stg, prd."
  }
}

variable "statemachine_role_arn" {
  description = "ARN of the IAM role assumed by the state machine (lambda:InvokeFunction on the five pipeline functions and CloudWatch Logs delivery)."
  type        = string
}

# キーは compute の function_arns と同じ 5 つに固定する
# 足りないキーは templatefile の展開で "Invalid index" になり原因が読み取りにくいため、ここで検査して止める
variable "function_arns" {
  description = "Map of Lambda function ARNs keyed by validator, preprocessor, textract-parser, bedrock-parser, and finalizer (the compute module's function_arns output)."
  type        = map(string)

  validation {
    condition     = alltrue([for k in ["validator", "preprocessor", "textract-parser", "bedrock-parser", "finalizer"] : contains(keys(var.function_arns), k)])
    error_message = "function_arns must contain the keys validator, preprocessor, textract-parser, bedrock-parser, and finalizer."
  }
}
