variable "env" {
  description = "Environment identifier used as the resource name prefix (dev, stg, or prd)."
  type        = string

  validation {
    condition     = contains(["dev", "stg", "prd"], var.env)
    error_message = "env must be one of dev, stg, prd."
  }
}

variable "account_id" {
  description = "AWS account ID appended to the S3 bucket name to make it globally unique."
  type        = string

  validation {
    condition     = can(regex("^[0-9]{12}$", var.account_id))
    error_message = "account_id must be a 12-digit AWS account ID."
  }
}
