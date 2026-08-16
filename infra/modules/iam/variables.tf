variable "env" {
  description = "Environment identifier used as the resource name prefix (dev, stg, or prd)."
  type        = string

  validation {
    condition     = contains(["dev", "stg", "prd"], var.env)
    error_message = "env must be one of dev, stg, prd."
  }
}

variable "region" {
  description = "AWS region used to build region-scoped ARNs (Bedrock inference profile, Step Functions and Textract source ARNs in trust policies)."
  type        = string
}

variable "account_id" {
  description = "AWS account ID used to build ARNs and to restrict which account may assume the service roles."
  type        = string

  validation {
    condition     = can(regex("^[0-9]{12}$", var.account_id))
    error_message = "account_id must be a 12-digit AWS account ID."
  }
}

variable "documents_bucket_arn" {
  description = "ARN of the documents bucket; object-level S3 statements are built from it per prefix (uploads/, work/, outputs/)."
  type        = string
}

variable "jobs_table_arn" {
  description = "ARN of the DynamoDB jobs table used as the resource of PutItem and UpdateItem statements."
  type        = string
}

variable "textract_completion_topic_arn" {
  description = "ARN of the SNS topic that the Textract publish role is allowed to publish completion notifications to."
  type        = string
}

variable "lambda_function_arns" {
  description = "Map of Lambda function ARNs that the state machine role may invoke, keyed by validator, preprocessor, textract-parser, bedrock-parser, finalizer."
  type        = map(string)

  validation {
    condition     = length(setsubtract(["validator", "preprocessor", "textract-parser", "bedrock-parser", "finalizer"], keys(var.lambda_function_arns))) == 0
    error_message = "lambda_function_arns must contain the keys validator, preprocessor, textract-parser, bedrock-parser, finalizer."
  }
}

variable "lambda_log_group_arns" {
  description = "Map of CloudWatch Logs log group ARNs (without a trailing :*) that each Lambda role may write to, keyed by validator, preprocessor, textract-parser, bedrock-parser, finalizer."
  type        = map(string)

  validation {
    condition     = length(setsubtract(["validator", "preprocessor", "textract-parser", "bedrock-parser", "finalizer"], keys(var.lambda_log_group_arns))) == 0
    error_message = "lambda_log_group_arns must contain the keys validator, preprocessor, textract-parser, bedrock-parser, finalizer."
  }
}
