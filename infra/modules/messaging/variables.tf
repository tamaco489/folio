variable "env" {
  description = "Environment identifier used as the resource name prefix (dev, stg, or prd)."
  type        = string

  validation {
    condition     = contains(["dev", "stg", "prd"], var.env)
    error_message = "env must be one of dev, stg, prd."
  }
}

variable "documents_bucket_name" {
  description = "Name of the documents bucket whose Object Created events start the pipeline (matched on detail.bucket.name)."
  type        = string
}

variable "state_machine_arn" {
  description = "ARN of the pipeline state machine that the upload-trigger rule starts."
  type        = string
}

variable "textract_publish_role_arn" {
  description = "ARN of the IAM role that Textract assumes to publish job completion notifications; granted sns:Publish in the topic policy."
  type        = string
}

variable "textract_parser_function_arn" {
  description = "ARN of the textract-parser Lambda function that subscribes to the completion topic."
  type        = string
}
