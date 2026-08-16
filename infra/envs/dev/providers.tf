provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Project     = "folio"
      Environment = var.env
      ManagedBy   = "terraform"
    }
  }
}
