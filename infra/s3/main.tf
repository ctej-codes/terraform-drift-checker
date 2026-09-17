terraform {
  required_version = ">= 1.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 4.0"
    }
  }
}

provider "aws" {
  region = var.region
}

variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "bucket_name" {
  description = "Name of the S3 bucket to create"
  type        = string
}

variable "tags" {
  description = "Tags to apply to the S3 bucket"
  type        = map(string)
  default     = {}
}

resource "aws_s3_bucket" "this" {
  bucket = var.bucket_name

  tags = merge({
    "CreatedBy" = "tf-drift-detector",
  }, var.tags)

  versioning {
    enabled = true
  }

  lifecycle_rule {
    id      = "cleanup"
    enabled = true
    expiration {
      days = 365
    }
  }
}

output "bucket_arn" {
  value = aws_s3_bucket.this.arn
}
