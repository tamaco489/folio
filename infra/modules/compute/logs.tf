# Lambda の実行ロールに logs:CreateLogGroup を付けないため、関数が初回起動時に自動作成する経路には頼らない
# 関数側の logging_config.log_group がこのロググループを指し、依存関係で関数より先に作られる
resource "aws_cloudwatch_log_group" "pipeline" {
  for_each = local.functions

  name              = "/aws/lambda/${local.function_names[each.key]}"
  retention_in_days = local.log_retention_days
}
