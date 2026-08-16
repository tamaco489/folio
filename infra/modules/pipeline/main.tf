locals {
  name_prefix = "${var.env}-folio"

  state_machine_name = "${local.name_prefix}-pipeline"

  # ロググループの保持期間 (日)
  # dev は評価用で、実行データを含むログは 2 週間もあれば調査に足りる
  log_retention_days = 14
}

# ------------------------------------------------------------------------------
# CloudWatch Logs: ステートマシンの実行ログ
# ------------------------------------------------------------------------------

# 名前は /aws/vendedlogs/ で始める (Step Functions のログ配信は CloudWatch Logs のリソースポリシーを使い、この接頭辞なら 5,120 文字の上限に当たらず配信が許可される)
# 暗号化は AWS 管理の既定のままにし CMK は使わない (storage と同じく鍵の分離や監査の要件がなく、Step Functions 側の鍵権限も増えるため)
resource "aws_cloudwatch_log_group" "pipeline" {
  name              = "/aws/vendedlogs/states/${local.state_machine_name}"
  retention_in_days = local.log_retention_days
}

# ------------------------------------------------------------------------------
# Step Functions: パイプラインの制御
# ------------------------------------------------------------------------------

# 制御はここに置き、処理は Lambda に置く (順序・分岐・並列・再試行を ASL に宣言し、Lambda は個々の処理だけを担う)
# State 間で受け渡すのは S3 キーと小さな判定結果だけにする (State 間の入出力は 256KB が上限)
#
# ASL は JSONata で書く (definition.asl.json)
# 経路 B の Map はページ番号 1..pageCount を回す必要があり、preprocessor はページの配列を出力しない (256KB 対策で pageCount とプレフィックスだけ)
# JSONata なら Items に範囲式 [1..$pageCount] を書けるが、JSONPath では配列を作れず別の Lambda か Distributed Map が要る
#
# templatefile で差し込むのは関数 ARN 5 つと Map の MaxConcurrency だけにする
# 定義中の JSONata 式 ({% ... %}) と $states は templatefile の ${ } と %{ } に当たらないため、そのまま書ける
# ただし {% の直後は必ず空白にする ({%{ と続けると %{ がディレクティブと解釈される)
#
# Textract は Step Functions の直接統合ではなく textract-parser を lambda:invoke.waitForTaskToken で呼ぶ
# Lambda が StartDocumentAnalysis を起動してタスクトークンを S3 に退避し、SNS 経由の完了通知を受けた同じ Lambda が SendTaskSuccess / SendTaskFailure で返す
# 通知が届かない事故で無期限に待たないよう、この Task には TimeoutSeconds 3600 を置く (経路 B の結果だけで finalizer に進める)
#
# 各 Task の TimeoutSeconds は Lambda 側の timeout より大きく取り、Lambda 自身のタイムアウトが Step Functions のタイムアウトより先に観測されるようにする
#   Validate 360 (Lambda 300 想定) / Preprocess 960 と Finalize 960 (Lambda の上限 900) / BedrockPage 660 (Lambda 600 想定)
#
# 経路 B の Map の Retry は bedrock-parser が内部で最大 5 回の指数バックオフを持つため控えめにし (10 秒 / 2 回 / 倍率 2)、再試行しても直らない InvalidInputError と PageDecodeError は MaxAttempts 0 で除外する
# 経路 A の Retry は起動時の RetryableError (Textract のスロットリング等) だけを対象にし、PermanentError と SendTaskFailure の TextractJobFailed / ExtractFailed、States.Timeout は Catch でブランチの出力に落とす
# 片方の経路だけが失敗しても他方の結果で finalizer に進み、両方失敗したときは finalizer が NoResultError を返して実行を失敗させる
resource "aws_sfn_state_machine" "pipeline" {
  name     = local.state_machine_name
  type     = "STANDARD"
  role_arn = var.statemachine_role_arn

  definition = templatefile("${path.module}/definition.asl.json", {
    validator_arn               = var.function_arns["validator"]
    preprocessor_arn            = var.function_arns["preprocessor"]
    textract_parser_arn         = var.function_arns["textract-parser"]
    bedrock_parser_arn          = var.function_arns["bedrock-parser"]
    finalizer_arn               = var.function_arns["finalizer"]
    bedrock_map_max_concurrency = var.bedrock_map_max_concurrency
  })

  # dev では ALL と実行データを残す (State 間を流れるのは S3 キーと判定結果だけで機密は含まれない)
  # X-Ray は使わない (tracing_configuration を定義しない)
  logging_configuration {
    log_destination        = "${aws_cloudwatch_log_group.pipeline.arn}:*"
    include_execution_data = true
    level                  = "ALL"
  }
}
