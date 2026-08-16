# Lambda に付けるロールではなく、StartDocumentAnalysis の NotificationChannel.RoleArn として Textract に渡す
resource "aws_iam_role" "textract_publish" {
  name               = "${local.name_prefix}-textract-publish-role"
  assume_role_policy = data.aws_iam_policy_document.textract_publish_assume.json
}

data "aws_iam_policy_document" "textract_publish" {
  statement {
    sid       = "PublishCompletion"
    actions   = ["sns:Publish"]
    resources = [var.textract_completion_topic_arn]
  }
}

resource "aws_iam_role_policy" "textract_publish" {
  name   = "${local.name_prefix}-textract-publish-policy"
  role   = aws_iam_role.textract_publish.name
  policy = data.aws_iam_policy_document.textract_publish.json
}
