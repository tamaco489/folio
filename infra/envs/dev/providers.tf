provider "aws" {
  region = var.region

  # タグはここで一括付与し、モジュール内では個別に付けない
  default_tags {
    tags = {
      Project     = "folio"
      Environment = var.env
      ManagedBy   = "terraform"
    }
  }
}
