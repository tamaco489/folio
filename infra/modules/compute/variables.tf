variable "env" {
  description = "Environment identifier used as the resource name prefix (dev, stg, or prd); also passed to every function as FOLIO_ENV."
  type        = string

  validation {
    condition     = contains(["dev", "stg", "prd"], var.env)
    error_message = "env must be one of dev, stg, prd."
  }
}

variable "documents_bucket_name" {
  description = "Name of the S3 bucket that holds uploads/, work/, and outputs/ (passed to every function as FOLIO_DOCUMENTS_BUCKET)."
  type        = string
}

variable "jobs_table_name" {
  description = "Name of the DynamoDB jobs table (passed to validator and finalizer as FOLIO_JOBS_TABLE)."
  type        = string
}

variable "artifacts_bucket_name" {
  description = "Name of the versioned S3 bucket that holds lambda/*.zip and layers/pdf-processor.zip; the objects must exist before plan."
  type        = string
}

variable "lambda_validate_role_arn" {
  description = "ARN of the execution role for the validator function."
  type        = string
}

variable "lambda_preprocess_role_arn" {
  description = "ARN of the execution role for the preprocessor function."
  type        = string
}

variable "lambda_parser_role_arn" {
  description = "ARN of the execution role shared by the textract-parser and bedrock-parser functions."
  type        = string
}

variable "lambda_finalize_role_arn" {
  description = "ARN of the execution role for the finalizer function."
  type        = string
}

variable "textract_completion_topic_arn" {
  description = "ARN of the SNS topic that Textract notifies on completion (passed to textract-parser as FOLIO_TEXTRACT_SNS_TOPIC_ARN)."
  type        = string
}

variable "textract_publish_role_arn" {
  description = "ARN of the role Textract assumes to publish to the completion topic (passed to textract-parser as FOLIO_TEXTRACT_ROLE_ARN)."
  type        = string
}

variable "bedrock_model_id" {
  description = "Bedrock model ID or inference profile ID used for structuring (passed to textract-parser and bedrock-parser as FOLIO_BEDROCK_MODEL_ID)."
  type        = string
}

variable "textract_feature_types" {
  description = "Comma-separated Textract FeatureTypes passed to textract-parser as FOLIO_TEXTRACT_FEATURE_TYPES."
  type        = string
  default     = "LAYOUT,TABLES"
}

variable "crossref_mailto" {
  description = "Contact address for the Crossref polite pool (passed to finalizer as FOLIO_CROSSREF_MAILTO); leave empty to omit the variable and use the public pool."
  type        = string
  default     = ""
}
