locals {
  name_prefix = "${var.env}-folio"

  # artifacts バケット内の zip のキー (backend の just package が作る bin/{関数名}.zip と Layer の pdf-processor.zip を同じ名前で置く)
  # キーは固定で、差し替えの検出はバケットのバージョニング (storage) と s3_object_version で行う
  lambda_key_prefix = "lambda/"
  layer_key         = "layers/pdf-processor.zip"

  # dev のロググループの保持日数
  # 評価用の環境で監査の要件がなく、失敗の調査は当日から週明けまでに行うため 3 日にし、変数にはしない
  log_retention_days = 3

  # /tmp の大きさ (MB) は Lambda の既定 512 が最小で、超えた分だけ GB 秒で課金される
  # validator と preprocessor は受け付ける上限の PDF (500 * 1000 * 1000 バイト = 477 MiB) を丸ごと /tmp に落とす
  # 既定のままだと上限付近の PDF で残りが数十 MB になり、preprocessor はさらにページ画像を 25 ページ分置くため溢れる
  # 両関数とも 1 ジョブに 1 回、数秒から数分の実行なので追加の課金は無視でき、上限まで受け付ける前提を /tmp の都合で崩さないため 1024 にする
  # 他 3 本は /tmp を使わないので既定のままにする
  default_ephemeral_storage_mb = 512
  pdf_ephemeral_storage_mb     = 1024

  # 5 関数の定義 (キーは他モジュールとの受け渡しに使う識別子で、for_each のキーもこの静的な文字列に限る)
  # name は cmd/pipeline/{dir} をハイフンで連結した backend の関数名で、zip の名前 (bin/{name}.zip) と一致する
  # environment は backend/internal/config の Require* に対応する必須の環境変数だけを持ち、全関数に共通の FOLIO_ENV は関数側で merge する
  #   AWS_REGION は Lambda ランタイムの予約変数で設定できない (未設定でも backend が us-east-1 を既定にする)
  #   poppler 用の FOLIO_POPPLER_BIN_DIR と FOLIO_RASTERIZE_DPI は Layer の /opt/bin と既定 DPI で自動解決するため設定しない
  # layers は poppler を呼ぶ 2 本にだけ付ける (preprocessor は pdftoppm と pdftotext、validator はページ数と暗号化の判定に pdfinfo を使う)
  # memory_size と timeout の根拠は各関数のコメントに書く
  # メモリは検証環境の評価対象 (8〜20 ページの論文) に合わせて小さく取る (CPU の割り当てはメモリに比例するため、CPU 依存の preprocessor だけ大きめ)
  functions = {
    # 数 MB の PDF を /tmp へ落として SHA-256 と pdfinfo を取るだけで、計算も転送も軽いため 512 MB
    # 上限の PDF の転送とハッシュ計算に DynamoDB の条件付き PutItem を含めても 300 秒あれば足りる想定 (評価対象の論文は数 MB なので通常は数秒)
    validator = {
      name              = "pipeline-validator"
      role_arn          = var.lambda_validate_role_arn
      memory_size       = 512
      timeout           = 300
      ephemeral_storage = local.pdf_ephemeral_storage_mb
      layers            = [aws_lambda_layer_version.pdf_processor.arn]
      environment = {
        FOLIO_DOCUMENTS_BUCKET = var.documents_bucket_name
        FOLIO_JOBS_TABLE       = var.jobs_table_name
      }
    }

    # pdftoppm によるラスタライズ (最大 200 ページ、150 DPI) と pdftotext を 1 回の起動で行う
    # 外部プロセス 1 回の上限が 10 分 (pdf.DefaultTimeout) なので Lambda 側は上限の 900 秒にし、Lambda のタイムアウトが先に来ないようにする
    # pdftoppm は CPU 依存で、メモリに比例する vCPU 割り当てが実行時間を決めるため 5 本で最も大きい 1024 MB にする (20 ページなら数十秒で終わる)
    preprocessor = {
      name              = "pipeline-preprocessor"
      role_arn          = var.lambda_preprocess_role_arn
      memory_size       = 1024
      timeout           = 900
      ephemeral_storage = local.pdf_ephemeral_storage_mb
      layers            = [aws_lambda_layer_version.pdf_processor.arn]
      environment = {
        FOLIO_DOCUMENTS_BUCKET = var.documents_bucket_name
      }
    }

    # 起動パスは StartDocumentAnalysis を呼んで即座に返るが、SNS 通知パスでは GetDocumentAnalysis のページングと Bedrock の構造化 (最大 5 回の指数バックオフ、待機は最大 20 秒) を 1 回の起動で行う
    # 同じ関数を両パスで使うため、長い方の通知パスに合わせて 600 秒にする
    textract-parser = {
      name              = "pipeline-textract-parser"
      role_arn          = var.lambda_parser_role_arn
      memory_size       = 512
      timeout           = 600
      ephemeral_storage = local.default_ephemeral_storage_mb
      layers            = []
      environment = {
        FOLIO_DOCUMENTS_BUCKET       = var.documents_bucket_name
        FOLIO_BEDROCK_MODEL_ID       = var.bedrock_model_id
        FOLIO_TEXTRACT_SNS_TOPIC_ARN = var.textract_completion_topic_arn
        FOLIO_TEXTRACT_ROLE_ARN      = var.textract_publish_role_arn
        FOLIO_TEXTRACT_FEATURE_TYPES = var.textract_feature_types
      }
    }

    # Map から 1 ページ = 1 起動で走り、ページ画像 1 枚を Converse にインライン送信して再試行する
    # 予約同時実行数は設定しない (並列度は Map の MaxConcurrency と関数内の指数バックオフで制御し、アカウントの同時実行枠を固定で切り出さない)
    bedrock-parser = {
      name              = "pipeline-bedrock-parser"
      role_arn          = var.lambda_parser_role_arn
      memory_size       = 512
      timeout           = 300
      ephemeral_storage = local.default_ephemeral_storage_mb
      layers            = []
      environment = {
        FOLIO_DOCUMENTS_BUCKET = var.documents_bucket_name
        FOLIO_BEDROCK_MODEL_ID = var.bedrock_model_id
      }
    }

    # 最大 200 ページ分の結果 JSON を読み、Crossref を 350ms 間隔で直列に呼ぶ
    # FOLIO_CROSSREF_MAILTO は任意で、空なら環境変数そのものを置かない (空文字を渡しても backend は public pool として扱うが、設定の有無で意図を明示する)
    finalizer = {
      name              = "pipeline-finalizer"
      role_arn          = var.lambda_finalize_role_arn
      memory_size       = 512
      timeout           = 300
      ephemeral_storage = local.default_ephemeral_storage_mb
      layers            = []
      environment = merge(
        {
          FOLIO_DOCUMENTS_BUCKET = var.documents_bucket_name
          FOLIO_JOBS_TABLE       = var.jobs_table_name
        },
        { for k, v in { FOLIO_CROSSREF_MAILTO = var.crossref_mailto } : k => v if v != "" },
      )
    }
  }

  function_names = { for key, f in local.functions : key => "${local.name_prefix}-${f.name}" }
}
