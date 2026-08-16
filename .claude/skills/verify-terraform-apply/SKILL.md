---
name: verify-terraform-apply
description: "just apply の後に、Terraform の定義どおりに AWS リソースが作られたかを読み取り専用の API で確認し、項目 / 期待値 / 実体 / 判定の表で報告するスキル。結果は tmp/verify-terraform-apply-result/ に Markdown で保存する。「apply 後の確認をして」「リソースが定義どおりか確認して」「AWS の実体を Terraform と突き合わせて」などのリクエストでトリガーする。"
---

# Verify Terraform Apply — apply 後の AWS リソース確認スキル

`infra/` の `just apply` の後に、Terraform の定義 (`.tf` と `terraform state list`) から期待値を読み、AWS の実体を読み取り専用の API で取って突き合わせる。
期待値をこの手順書に固定値で書かない。値が変わっても手順が陳腐化しないように、**定義のどこを読むか**だけを書く。

## 処理ステップ概要

| Step | 内容        | 概要                                                                                                                                               |
| ---- | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1    | 入力の確定  | `env` `account_id` `region` `AWS_PROFILE` を決め、認証情報のアカウントと一致を見る                                                                 |
| 2    | plan の確認 | `just plan` が `No changes` であることを確認する (state と定義が一致している前提)                                                                  |
| 3    | 対象の列挙  | `terraform state list` で確認対象を列挙し、種別ごとの節に振り分ける                                                                                |
| 4    | 突き合わせ  | 種別ごとに読み取り API を実行し、定義の値と比べる                                                                                                  |
| 5    | 結果報告    | 「項目 / 期待値 (定義) / 実体 / 判定」の表で報告する。差異は先頭にまとめる。同じ内容を `tmp/verify-terraform-apply-result/` に Markdown で保存する |

## 制約事項

- **AWS への操作は読み取りだけにする。** 使ってよいのは `get-*` `describe-*` `list-*` (`s3api get-*` と `list-objects-v2`、`sts get-caller-identity`、`stepfunctions validate-state-machine-definition` を含む) のみ。`put-*` `create-*` `update-*` `delete-*` `tag-*` `invoke` `start-execution` は使わない
- **差異があっても実体を手で直さない。** 修正は Terraform の定義側で行い、`terraform apply` はユーザーが実行する
- Terraform のコマンドは `just plan` と `terraform -chdir=envs/{env} state list` / `state show` / `output` (state の読み取り) だけを使う。`apply` `destroy` `import` `state rm` `state mv` は使わない
- 期待値は定義から読む。この手順書にも報告にも固定の期待値を書かない。アカウント ID や ARN を含む値はチャットの報告と `tmp/verify-terraform-apply-result/` (`.gitignore` の `tmp/` で git 管理外) のレポートに留め、git 管理下のファイルに書かない
- 実行主体は AWS CLI でも AWS MCP (`call_aws` / `run_script`) でもよく、渡すコマンド文字列は同じにする。MCP のトークンが切れたら `/mcp` で再認証する
- Textract / Bedrock のような課金 API は呼ばない (このスキルは構成の確認だけを行い、動作確認は行わない)

## 入力

| 名前          | 取り方                                                                                                                            |
| ------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| `ENV`         | ユーザー指定。既定は `dev` (`infra/envs/{env}/terraform.tfvars` の `env`)                                                         |
| `ACCOUNT_ID`  | 環境変数 `TF_VAR_account_id`。未設定なら `aws sts get-caller-identity --query Account --output text` で取る。ファイルには書かない |
| `REGION`      | `infra/envs/{env}/variables.tf` の `region` の `default` (tfvars に上書きがあればそちら)                                          |
| `AWS_PROFILE` | 対象アカウントの認証プロファイル (ユーザー指定)                                                                                   |

> [!IMPORTANT]
> `AWS_PROFILE` の既定リージョンが `REGION` と違うことがある (既定リージョンのまま `describe-table` を実行して `ResourceNotFoundException` になった実例がある)。
> すべてのコマンドに `--region $REGION` を付けるか、`AWS_REGION` を `REGION` に揃えてから実行する。S3 の `s3api` はリージョンをまたいでも応答するため、この取り違えに気付きにくい。

Step 4 の各コマンドは次の変数を前提にする。

```bash
ENV=dev
ACCOUNT_ID=${TF_VAR_account_id:-$(aws sts get-caller-identity --query Account --output text)}
REGION=<infra/envs/${ENV}/variables.tf の region の default>
export AWS_PROFILE=<対象アカウントのプロファイル> AWS_REGION=$REGION
P=${ENV}-folio   # リソース名の接頭辞 (modules/*/main.tf の local.name_prefix)
```

---

## 各ステップの詳細

### Step 1: 入力の確定

上の表に従って値を決め、認証情報のアカウントが `ACCOUNT_ID` と一致することを見る。

```bash
aws sts get-caller-identity --query '{Account:Account,Arn:Arn}'
```

`Account` が `ACCOUNT_ID` と違えばここで止め、`AWS_PROFILE` か `TF_VAR_account_id` の取り違えをユーザーに伝える (`envs/{env}/main.tf` の `data.aws_caller_identity.current` の postcondition が plan でも同じ検査をする)。

### Step 2: plan の確認

```bash
cd infra
just env=$ENV plan
```

`No changes. Your infrastructure matches the configuration.` で終わることを確認する。
差分が出た場合はこのスキルの前提 (state と定義の一致) が崩れているので、差分の内容を報告して止める (apply はユーザーが判断する)。

### Step 3: 対象の列挙

```bash
terraform -chdir=envs/$ENV state list   # infra/ で実行する (Step 2 と同じ)
```

出力の各行 (state のアドレス) を下の表で種別に振り分ける。`data.` で始まる行 (データソース) は実体を持たないので対象外にする。
アドレスの `module.<name>` は `envs/{env}/main.tf` の `module "<name>"` ブロックに対応し、`.tf` の定義はそのモジュールのディレクトリで読む。

| state のアドレス                                                                                                                                                             | Step 4 の節                      |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------- |
| `module.storage.aws_s3_bucket.*` とその `aws_s3_bucket_public_access_block` `_server_side_encryption_configuration` `_versioning` `_lifecycle_configuration` `_notification` | S3 バケット                      |
| `module.storage.aws_dynamodb_table.jobs`                                                                                                                                     | DynamoDB テーブル                |
| `module.iam.aws_iam_role.*` `module.iam.aws_iam_role_policy.*` `module.messaging.aws_iam_role.eventbridge_invoke` `module.messaging.aws_iam_role_policy.eventbridge_invoke`  | IAM ロール                       |
| `module.compute.aws_lambda_function.pipeline["<key>"]` `module.compute.aws_lambda_layer_version.pdf_processor`                                                               | Lambda 関数と Layer              |
| `module.compute.aws_cloudwatch_log_group.pipeline["<key>"]` `module.pipeline.aws_cloudwatch_log_group.pipeline`                                                              | CloudWatch Logs ロググループ     |
| `module.pipeline.aws_sfn_state_machine.pipeline`                                                                                                                             | Step Functions ステートマシン    |
| `module.messaging.aws_cloudwatch_event_rule.upload_trigger` `module.messaging.aws_cloudwatch_event_target.upload_trigger`                                                    | EventBridge ルールとターゲット   |
| `module.messaging.aws_sqs_queue.upload_trigger_dlq` `module.messaging.aws_sqs_queue_policy.upload_trigger_dlq`                                                               | SQS (DLQ)                        |
| `module.messaging.aws_sns_topic.textract_completion` `_topic_policy` `_topic_subscription` `module.messaging.aws_lambda_permission.textract_completion`                      | SNS トピックと Lambda permission |

state に無いアドレスの節は飛ばす。state にあるのに下の表に無い種別が出たら、報告の「懸念」に書く。

ARN や Layer のバージョン番号のように定義から直接は読めない値は `terraform -chdir=envs/$ENV state show <アドレス>` で state から読む (読み取りのみ)。

### Step 4: 突き合わせ

各節の表は「見る属性 / コマンドと出力の場所 / 定義側の対応」の 3 列で、期待値は右列に書いた定義を読んで決める。
各節のコマンドと出力の形は 2026-08-16 に dev の全リソース (S3、DynamoDB、IAM、Lambda、Logs、Step Functions、EventBridge、SQS、SNS) で実測して確定したもの。
リソース名は `modules/*/main.tf` の `local` (`"${var.env}-folio-..."`) から組み立てる。

#### 共通: タグ

タグは `envs/{env}/providers.tf` の `default_tags` (`Project` `Environment` `ManagedBy`) で一括付与しており、モジュール内では `tags` を書かない。
タグを持てる種別はすべてこの 3 つが付いていることを見る (値は `providers.tf` から読む)。読み取りコマンドは種別ごとの表に含める。

#### S3 バケット (storage)

対象は `modules/storage/main.tf` の `aws_s3_bucket.documents` と `aws_s3_bucket.artifacts` で、名前は `local.documents_bucket_name` / `local.artifacts_bucket_name` (`${P}-documents-${ACCOUNT_ID}` / `${P}-artifacts-${ACCOUNT_ID}`)。

```bash
for B in ${P}-documents-${ACCOUNT_ID} ${P}-artifacts-${ACCOUNT_ID}; do
  aws s3api get-bucket-location --bucket "$B"
  aws s3api get-public-access-block --bucket "$B"
  aws s3api get-bucket-encryption --bucket "$B"
  aws s3api get-bucket-versioning --bucket "$B"
  aws s3api get-bucket-lifecycle-configuration --bucket "$B"
  aws s3api get-bucket-notification-configuration --bucket "$B"
  aws s3api get-bucket-logging --bucket "$B"
  aws s3api get-bucket-tagging --bucket "$B"
  aws s3api list-objects-v2 --bucket "$B" --query 'Contents[].Key'
done
```

| 見る属性           | コマンドと出力の場所                                                                                                                                                                                                                  | 定義側の対応                                                                                                                                                          |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| リージョン         | `get-bucket-location` の `LocationConstraint` (`us-east-1` は `null` で返る)                                                                                                                                                          | `envs/{env}/variables.tf` の `region`                                                                                                                                 |
| パブリックアクセス | `get-public-access-block` の `PublicAccessBlockConfiguration.{BlockPublicAcls,IgnorePublicAcls,BlockPublicPolicy,RestrictPublicBuckets}`                                                                                              | `aws_s3_bucket_public_access_block.<bucket>` の 4 引数                                                                                                                |
| 暗号化             | `get-bucket-encryption` の `ServerSideEncryptionConfiguration.Rules[0].ApplyServerSideEncryptionByDefault.SSEAlgorithm` (`KMSMasterKeyID` が無いこと。`BucketKeyEnabled` は未指定なら `false`)                                        | `aws_s3_bucket_server_side_encryption_configuration.<bucket>` の `sse_algorithm`                                                                                      |
| バージョニング     | `get-bucket-versioning` の `Status`。一度も有効にしていないバケットは出力が空 (`--query Status` は `null`)                                                                                                                            | `aws_s3_bucket_versioning.<bucket>` の `versioning_configuration.status`。定義が無いバケットは空であること                                                            |
| ライフサイクル     | `get-bucket-lifecycle-configuration` の `Rules[].{ID,Status,Filter.Prefix,Expiration.Days,NoncurrentVersionExpiration.NoncurrentDays,AbortIncompleteMultipartUpload.DaysAfterInitiation}` (`filter {}` は `Filter.Prefix: ""` で返る) | `aws_s3_bucket_lifecycle_configuration.<bucket>` の `rule` (`id` `status` `filter.prefix` と日数の `local`)。ルール数も一致すること                                   |
| EventBridge 通知   | `get-bucket-notification-configuration` に `EventBridgeConfiguration: {}` があること。定義が無いバケットは出力が空                                                                                                                    | `aws_s3_bucket_notification.<bucket>` の `eventbridge`                                                                                                                |
| アクセスログ       | `get-bucket-logging` の出力が空であること                                                                                                                                                                                             | `aws_s3_bucket_logging` を定義しない (理由は `aws_s3_bucket.documents` 直前のコメント)                                                                                |
| タグ               | `get-bucket-tagging` の `TagSet[].{Key,Value}`                                                                                                                                                                                        | `envs/{env}/providers.tf` の `default_tags`                                                                                                                           |
| 中身               | `list-objects-v2` の `Contents[].Key` (空なら `null`)                                                                                                                                                                                 | 作成直後は空。artifacts は `modules/compute/main.tf` の `lambda_key_prefix` / `layer_key` の zip がアップロード後に並ぶ (`s3_object_version` は各 zip の `VersionId`) |

`force_destroy` は Terraform 側の挙動で API からは読めないため対象外にする。

#### DynamoDB テーブル (storage)

対象は `modules/storage/main.tf` の `aws_dynamodb_table.jobs` で、名前は `local.jobs_table_name` (`${P}-jobs`)。

```bash
T=${P}-jobs
aws dynamodb describe-table --table-name "$T" \
  --query 'Table.{TableStatus:TableStatus,BillingMode:BillingModeSummary.BillingMode,KeySchema:KeySchema,AttributeDefinitions:AttributeDefinitions,GSI:GlobalSecondaryIndexes[].{IndexName:IndexName,KeySchema:KeySchema,Projection:Projection,IndexStatus:IndexStatus},DeletionProtectionEnabled:DeletionProtectionEnabled,SSEDescription:SSEDescription,TableArn:TableArn}'
aws dynamodb describe-continuous-backups --table-name "$T"
aws dynamodb describe-time-to-live --table-name "$T"
aws dynamodb list-tags-of-resource --resource-arn "$(aws dynamodb describe-table --table-name "$T" --query Table.TableArn --output text)"
```

| 見る属性   | コマンドと出力の場所                                                                                                                                                               | 定義側の対応                                                                              |
| ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| 状態       | `describe-table` の `Table.TableStatus` が `ACTIVE`                                                                                                                                | —                                                                                         |
| 課金モード | `Table.BillingModeSummary.BillingMode`                                                                                                                                             | `billing_mode`                                                                            |
| キー       | `Table.KeySchema` (`AttributeName` `KeyType`) と `Table.AttributeDefinitions` (`AttributeName` `AttributeType`)                                                                    | `hash_key` と `attribute` ブロック (キーに使う属性だけ。個数も一致すること)               |
| GSI        | `Table.GlobalSecondaryIndexes[].{IndexName,KeySchema,Projection.ProjectionType,IndexStatus}` (`IndexStatus` は `ACTIVE`)                                                           | `global_secondary_index` の `name` `key_schema` (`HASH` → `RANGE` の順) `projection_type` |
| 削除保護   | `Table.DeletionProtectionEnabled`                                                                                                                                                  | `deletion_protection_enabled` (`local.is_disposable` の否定)                              |
| 暗号化     | `Table.SSEDescription` が無い (`null`) こと = AWS 所有キーの既定                                                                                                                   | `server_side_encryption` を定義しない (理由はリソース直前のコメント)                      |
| PITR       | `describe-continuous-backups` の `ContinuousBackupsDescription.PointInTimeRecoveryDescription.PointInTimeRecoveryStatus` (`ContinuousBackupsStatus` は常に `ENABLED` なので見ない) | `point_in_time_recovery.enabled` (`true` → `ENABLED`、`false` → `DISABLED`)               |
| TTL        | `describe-time-to-live` の `TimeToLiveDescription.TimeToLiveStatus`                                                                                                                | `ttl` を定義しない → `DISABLED`                                                           |
| タグ       | `list-tags-of-resource` の `Tags[].{Key,Value}`                                                                                                                                    | `envs/{env}/providers.tf` の `default_tags`                                               |

#### IAM ロール (iam, messaging)

対象は `modules/iam/*.tf` の `aws_iam_role` 6 つ (`lambda_validate` `lambda_preprocess` `lambda_parser` `lambda_finalize` `statemachine` `textract_publish`) と `modules/messaging/eventbridge.tf` の `aws_iam_role.eventbridge_invoke`。
名前は各リソースの `name` (`${P}-<役割>-role`)、インラインポリシーは同じファイルの `aws_iam_role_policy` の `name` (`${P}-<役割>-policy`)。

```bash
R=${P}-<役割>-role
aws iam get-role --role-name "$R" --query 'Role.{Arn:Arn,AssumeRolePolicyDocument:AssumeRolePolicyDocument}'
aws iam list-role-policies --role-name "$R"
aws iam get-role-policy --role-name "$R" --policy-name "${P}-<役割>-policy" --query PolicyDocument
aws iam list-attached-role-policies --role-name "$R"
aws iam list-role-tags --role-name "$R"
```

`AssumeRolePolicyDocument` と `PolicyDocument` は API では URL エンコードした文字列だが、CLI が JSON に展開して返す (展開されずに文字列で返ったら `jq -r` で URL デコードしてから読む)。

| 見る属性           | コマンドと出力の場所                                                                                                                        | 定義側の対応                                                                                                                                                                                                                                                                                              |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 信頼ポリシー       | `get-role` の `Role.AssumeRolePolicyDocument` の `Statement[].{Principal.Service,Action,Condition}`                                         | `assume_role_policy` に渡す `data.aws_iam_policy_document.*_assume` (`modules/iam/main.tf` の `lambda_assume` `statemachine_assume` `textract_publish_assume`、`modules/messaging/eventbridge.tf` の `eventbridge_assume`)。`principals.identifiers` と `condition` (`test` `variable` `values`) を比べる |
| インラインポリシー | `list-role-policies` の `PolicyNames` (1 件で名前が一致)。`get-role-policy` の `PolicyDocument.Statement[].{Sid,Action,Resource,Condition}` | `aws_iam_role_policy.<役割>` の `policy` に渡す `data.aws_iam_policy_document.<役割>` の各 `statement` (`sid` `actions` `resources` `condition`)。`resources` の `local` (`uploads_objects` など) は `modules/iam/main.tf` で展開して読む。ステートメント数も一致すること                                 |
| 管理ポリシー       | `list-attached-role-policies` の `AttachedPolicies` が空                                                                                    | `aws_iam_role_policy_attachment` を定義しない                                                                                                                                                                                                                                                             |
| タグ               | `list-role-tags` の `Tags[].{Key,Value}` (`get-role` の `Role.Tags` はタグが無いと省略されるので使わない)                                   | `envs/{env}/providers.tf` の `default_tags`                                                                                                                                                                                                                                                               |

`Action` と `Resource` は要素が 1 つだと文字列、複数だと配列で返るので、比べるときは配列に揃える。

#### Lambda 関数と Layer (compute)

対象は `modules/compute/lambda.tf` の `aws_lambda_function.pipeline` (`for_each = local.functions` の 5 本) と `modules/compute/layer.tf` の `aws_lambda_layer_version.pdf_processor`。
関数名は `local.function_names` (`${P}-<functions.<key>.name>`)、Layer 名は `${P}-pdf-processor`。

```bash
F=${P}-pipeline-<name>
aws lambda get-function-configuration --function-name "$F" \
  --query '{Runtime:Runtime,Architectures:Architectures,Handler:Handler,PackageType:PackageType,MemorySize:MemorySize,Timeout:Timeout,EphemeralStorage:EphemeralStorage.Size,Environment:Environment.Variables,Layers:Layers[].Arn,LoggingConfig:LoggingConfig,Role:Role}'
aws lambda list-tags --resource "$(aws lambda get-function-configuration --function-name "$F" --query FunctionArn --output text)"
aws lambda list-layer-versions --layer-name ${P}-pdf-processor \
  --query 'LayerVersions[].{Arn:LayerVersionArn,Version:Version,Runtimes:CompatibleRuntimes,Architectures:CompatibleArchitectures}'
aws lambda get-policy --function-name "$F"   # textract-parser 以外は ResourceNotFoundException になるのが正しい (SNS の節を参照)
```

| 見る属性                    | コマンドと出力の場所                                                                                                 | 定義側の対応                                                                                                                                                                                                                                                                                                                                                             |
| --------------------------- | -------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| ランタイム / アーキテクチャ | `get-function-configuration` の `Runtime` `Architectures` `Handler` `PackageType`                                    | `aws_lambda_function.pipeline` の `runtime` `architectures` `handler` `package_type` (5 本共通)                                                                                                                                                                                                                                                                          |
| メモリ / タイムアウト       | `MemorySize` `Timeout`                                                                                               | `local.functions.<key>.memory_size` / `.timeout`                                                                                                                                                                                                                                                                                                                         |
| /tmp                        | `EphemeralStorage.Size`                                                                                              | `local.functions.<key>.ephemeral_storage` (`local.pdf_ephemeral_storage_mb` / `local.default_ephemeral_storage_mb`)                                                                                                                                                                                                                                                      |
| 環境変数                    | `Environment.Variables` (キーの集合と値)                                                                             | `merge({ FOLIO_ENV = var.env }, local.functions.<key>.environment)`。`FOLIO_DOCUMENTS_BUCKET` `FOLIO_JOBS_TABLE` `FOLIO_TEXTRACT_SNS_TOPIC_ARN` `FOLIO_TEXTRACT_ROLE_ARN` は他モジュールの output の実値 (S3 / DynamoDB / SNS / IAM の節で取った名前と ARN) と一致すること。finalizer の `FOLIO_CROSSREF_MAILTO` は `var.crossref_mailto` が空なら**キー自体が無い**こと |
| Layer                       | `Layers[].Arn` (付いていない関数は `Layers` が省略され、`--query` では `null` になる)                                | `local.functions.<key>.layers` (validator と preprocessor だけ `aws_lambda_layer_version.pdf_processor.arn`、他は空)。ARN は `state show module.compute.aws_lambda_layer_version.pdf_processor` の `arn` と一致し、`list-layer-versions` の `LayerVersions[]` にその `LayerVersionArn` があること                                                                        |
| ログ                        | `LoggingConfig.LogFormat` `LoggingConfig.LogGroup`                                                                   | `logging_config` の `log_format` と `log_group` (`aws_cloudwatch_log_group.pipeline[<key>].name` = `/aws/lambda/<関数名>`)                                                                                                                                                                                                                                               |
| 実行ロール                  | `Role`                                                                                                               | `local.functions.<key>.role_arn` (iam モジュールの output。textract-parser と bedrock-parser は同じ `lambda_parser` ロール)                                                                                                                                                                                                                                              |
| タグ                        | `list-tags` の `Tags`                                                                                                | `envs/{env}/providers.tf` の `default_tags`                                                                                                                                                                                                                                                                                                                              |
| Layer の互換性              | `list-layer-versions` で関数が参照している `LayerVersionArn` の要素の `CompatibleRuntimes` `CompatibleArchitectures` | `aws_lambda_layer_version.pdf_processor` の `compatible_runtimes` `compatible_architectures`                                                                                                                                                                                                                                                                             |
| リソースポリシー            | `get-policy` が textract-parser 以外では `ResourceNotFoundException` になること (textract-parser は SNS の節で見る)  | `aws_lambda_permission` は `modules/messaging/sns.tf` の textract-parser 向け 1 つだけ                                                                                                                                                                                                                                                                                   |

`s3_bucket` `s3_key` `s3_object_version` は API の応答に含まれない (取り込み時に使われるだけ) ため、`state show` で読むに留める。

#### CloudWatch Logs ロググループ (compute, pipeline)

対象は `modules/compute/logs.tf` の `aws_cloudwatch_log_group.pipeline` (5 つ、名前は `/aws/lambda/<関数名>`) と `modules/pipeline/main.tf` の `aws_cloudwatch_log_group.pipeline` (名前は `/aws/vendedlogs/states/${P}-pipeline`)。

```bash
aws logs describe-log-groups --log-group-name-prefix /aws/lambda/${P}-pipeline- \
  --query 'logGroups[].{Name:logGroupName,Retention:retentionInDays,Arn:arn,Kms:kmsKeyId}'
aws logs describe-log-groups --log-group-name-prefix /aws/vendedlogs/states/${P}-pipeline \
  --query 'logGroups[].{Name:logGroupName,Retention:retentionInDays,Arn:arn,Kms:kmsKeyId}'
aws logs list-tags-for-resource --resource-arn <logGroupArn>
```

| 見る属性 | コマンドと出力の場所                                                                            | 定義側の対応                                                                                                |
| -------- | ----------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| 存在     | `describe-log-groups` の `logGroups[].logGroupName` (Lambda 用 5 つ + Step Functions 用 1 つ)   | `name` (`/aws/lambda/${local.function_names[key]}` と `/aws/vendedlogs/states/${local.state_machine_name}`) |
| 保持日数 | `logGroups[].retentionInDays`                                                                   | `retention_in_days` (`local.log_retention_days`。compute と pipeline で別々に定義しているので両方読む)      |
| 暗号化   | `logGroups[].kmsKeyId` が無いこと                                                               | `kms_key_id` を定義しない                                                                                   |
| タグ     | `list-tags-for-resource` の `tags` (`--resource-arn` には末尾 `:*` の無い `logGroupArn` を渡す) | `envs/{env}/providers.tf` の `default_tags`                                                                 |

#### Step Functions ステートマシン (pipeline)

対象は `modules/pipeline/main.tf` の `aws_sfn_state_machine.pipeline` で、名前は `local.state_machine_name` (`${P}-pipeline`)。

```bash
SM=$(aws stepfunctions list-state-machines --query "stateMachines[?name=='${P}-pipeline'].stateMachineArn" --output text)
aws stepfunctions describe-state-machine --state-machine-arn "$SM" \
  --query '{name:name,status:status,type:type,roleArn:roleArn,loggingConfiguration:loggingConfiguration,tracingConfiguration:tracingConfiguration}'
aws stepfunctions describe-state-machine --state-machine-arn "$SM" --query definition --output text > "$TMPDIR/deployed.asl.json"
aws stepfunctions validate-state-machine-definition --definition "file://$TMPDIR/deployed.asl.json" --type STANDARD --severity WARNING
aws stepfunctions list-tags-for-resource --resource-arn "$SM"
```

`--severity` の既定は `ERROR` で warning が出ないため、`WARNING` を付けて全部を見る。取り出した定義はアカウント ID を含むので、リポジトリの外 (`$TMPDIR` など) に置く。
リポジトリの `modules/pipeline/definition.asl.json` は `${...}` を残したままでも `validate-state-machine-definition` を通る (差し込み先が `Arguments.FunctionName` の文字列だけのため) ので、apply 前の構文確認にも同じコマンドを使える。

| 見る属性     | コマンドと出力の場所                                                                                                                   | 定義側の対応                                                                                                                                                                                                                                |
| ------------ | -------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 種別 / 状態  | `describe-state-machine` の `type` と `status` (`ACTIVE`)                                                                              | `type`                                                                                                                                                                                                                                      |
| ロール       | `roleArn`                                                                                                                              | `role_arn` (iam モジュールの `aws_iam_role.statemachine` の ARN)                                                                                                                                                                            |
| ログ         | `loggingConfiguration.{level,includeExecutionData,destinations[0].cloudWatchLogsLogGroup.logGroupArn}`                                 | `logging_configuration` の `level` `include_execution_data` `log_destination` (`aws_cloudwatch_log_group.pipeline.arn` に `:*` を付けたもの)                                                                                                |
| X-Ray        | `tracingConfiguration.enabled` が `false`                                                                                              | `tracing_configuration` を定義しない                                                                                                                                                                                                        |
| 定義の妥当性 | `validate-state-machine-definition` の `result` が `OK` (`diagnostics[].{severity,code,message,location}` は `WARNING` だけであること) | `definition` (`templatefile` で関数 ARN を差し込んだ `definition.asl.json`)                                                                                                                                                                 |
| 定義の内容   | 取り出した `definition` の `States` のキー集合と各 State の `Type` `Resource` `TimeoutSeconds`、`Arguments.FunctionName` の ARN        | `modules/pipeline/definition.asl.json` (`jq -S` で整形して比べる)。`${validator_arn}` などの 5 つの差し込み先が Lambda の節で取った関数 ARN に置き換わっており、`${` が残っていないこと。`Comment` `QueryLanguage` `StartAt` も一致すること |
| タグ         | `list-tags-for-resource` の `tags[].{key,value}`                                                                                       | `envs/{env}/providers.tf` の `default_tags`                                                                                                                                                                                                 |

定義の比較は、リポジトリの `definition.asl.json` の 5 つの `${...}` を関数 ARN で置き換えたものと取り出した `definition` を `jq -S .` で正規化して `diff` する。差が出た箇所だけ報告する。

#### EventBridge ルールとターゲット (messaging)

対象は `modules/messaging/eventbridge.tf` の `aws_cloudwatch_event_rule.upload_trigger` (名前は `${P}-upload-trigger`) と `aws_cloudwatch_event_target.upload_trigger`。

```bash
RULE=${P}-upload-trigger
aws events describe-rule --name "$RULE"
aws events list-targets-by-rule --rule "$RULE"
aws events list-tags-for-resource --resource-arn "$(aws events describe-rule --name "$RULE" --query Arn --output text)"
```

| 見る属性         | コマンドと出力の場所                                                                                                                    | 定義側の対応                                                                                                                                                                                                            |
| ---------------- | --------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 状態 / バス      | `describe-rule` の `State` (`ENABLED`) と `EventBusName` (`default`)                                                                    | `state` `event_bus_name` を定義しない (既定)                                                                                                                                                                            |
| イベントパターン | `EventPattern` (JSON 文字列。`jq` で開いて `source` `detail-type` `detail.bucket.name` `detail.object.key[0].wildcard` を見る)          | `event_pattern` の `jsonencode` の中身 (`var.documents_bucket_name` は S3 の節のバケット名、`local.upload_key_wildcard` は `modules/messaging/main.tf`)                                                                 |
| ターゲット       | `list-targets-by-rule` の `Targets` が 1 件で `Arn` `RoleArn` `DeadLetterConfig.Arn`。`InputTransformer` `Input` `InputPath` が無いこと | `aws_cloudwatch_event_target.upload_trigger` の `arn` (ステートマシン ARN) `role_arn` (`aws_iam_role.eventbridge_invoke`) `dead_letter_config.arn` (`aws_sqs_queue.upload_trigger_dlq`)。input transformer は定義しない |
| タグ             | `list-tags-for-resource` の `Tags[].{Key,Value}`                                                                                        | `envs/{env}/providers.tf` の `default_tags`                                                                                                                                                                             |

`aws_iam_role.eventbridge_invoke` と `aws_iam_role_policy.eventbridge_invoke` は IAM ロールの節で見る (信頼ポリシーの `Condition` にルール ARN、インラインポリシーの `Action` は `states:StartExecution`)。

#### SQS (messaging)

対象は `modules/messaging/eventbridge.tf` の `aws_sqs_queue.upload_trigger_dlq` (名前は `${P}-upload-trigger-dlq`) と `aws_sqs_queue_policy.upload_trigger_dlq`。

```bash
Q=$(aws sqs get-queue-url --queue-name ${P}-upload-trigger-dlq --query QueueUrl --output text)
aws sqs get-queue-attributes --queue-url "$Q" --attribute-names All
aws sqs list-queue-tags --queue-url "$Q"
```

| 見る属性       | コマンドと出力の場所                                                                                                    | 定義側の対応                                                                                                                                                             |
| -------------- | ----------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 保持期間       | `get-queue-attributes` の `Attributes.MessageRetentionPeriod` (秒の文字列)                                              | `message_retention_seconds` (`local.dlq_message_retention_seconds`)                                                                                                      |
| 暗号化         | `Attributes.SqsManagedSseEnabled` が `"true"`、`Attributes.KmsMasterKeyId` が無いこと                                   | `sqs_managed_sse_enabled`。`kms_master_key_id` を定義しない                                                                                                              |
| キューポリシー | `Attributes.Policy` (JSON 文字列。`jq` で開いて `Statement[].{Sid,Principal.Service,Action,Resource,Condition}` を見る) | `aws_sqs_queue_policy.upload_trigger_dlq` の `policy` に渡す `data.aws_iam_policy_document.upload_trigger_dlq` (`Condition` の `ArnEquals` `aws:SourceArn` はルール ARN) |
| 滞留           | `Attributes.ApproximateNumberOfMessages` (作成直後は `"0"`。増えていればターゲット起動の失敗があるので報告に書く)       | —                                                                                                                                                                        |
| タグ           | `list-queue-tags` の `Tags`                                                                                             | `envs/{env}/providers.tf` の `default_tags`                                                                                                                              |

#### SNS トピックと Lambda permission (messaging)

対象は `modules/messaging/sns.tf` の `aws_sns_topic.textract_completion` (名前は `${P}-textract-completion`)、`aws_sns_topic_policy.textract_completion`、`aws_sns_topic_subscription.textract_completion`、`aws_lambda_permission.textract_completion`。

```bash
TOPIC=arn:aws:sns:${REGION}:${ACCOUNT_ID}:${P}-textract-completion
aws sns get-topic-attributes --topic-arn "$TOPIC"
aws sns list-subscriptions-by-topic --topic-arn "$TOPIC"
aws sns list-tags-for-resource --resource-arn "$TOPIC"
aws lambda get-policy --function-name ${P}-pipeline-textract-parser --query Policy --output text | jq .
```

`Attributes.Policy` (SNS) と `Policy` (Lambda) は JSON を文字列にしたものなので、`jq` で開いて (`jq -r '.Attributes.Policy | fromjson'` など) から `Statement` を読む。

| 見る属性          | コマンドと出力の場所                                                                                                                                                                                  | 定義側の対応                                                                                                                                                                                                                                  |
| ----------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| トピックポリシー  | `get-topic-attributes` の `Attributes.Policy` (JSON 文字列。`jq` で開いて `Statement[].{Sid,Principal.AWS,Action,Resource}` を見る)                                                                   | `aws_sns_topic_policy.textract_completion` の `policy` に渡す `data.aws_iam_policy_document.textract_completion` (`principals.identifiers` は `var.textract_publish_role_arn`)。既定のポリシーが置き換わりステートメントが 1 つだけであること |
| 暗号化            | `Attributes.KmsMasterKeyId` が無いこと                                                                                                                                                                | `kms_master_key_id` を定義しない (理由はリソース直前のコメント)                                                                                                                                                                               |
| 購読              | `list-subscriptions-by-topic` の `Subscriptions[].{Protocol,Endpoint,SubscriptionArn}` が 1 件で、`SubscriptionArn` が `PendingConfirmation` でないこと。`Attributes.SubscriptionsConfirmed` が `"1"` | `aws_sns_topic_subscription.textract_completion` の `protocol` と `endpoint` (`var.textract_parser_function_arn`)                                                                                                                             |
| Lambda permission | `lambda get-policy` の `Policy` (JSON 文字列。`Statement[].{Sid,Principal.Service,Action,Resource,Condition.ArnLike."AWS:SourceArn"}` を見る)                                                         | `aws_lambda_permission.textract_completion` の `statement_id` `principal` `action` `function_name` `source_arn`                                                                                                                               |
| タグ              | `list-tags-for-resource` の `Tags[].{Key,Value}`                                                                                                                                                      | `envs/{env}/providers.tf` の `default_tags`                                                                                                                                                                                                   |

`aws_iam_role.textract_publish` (Textract がトピックへ発行するために assume するロール) は IAM ロールの節で見る。

### Step 5: 結果報告

リソースごとに次の表で報告する。差異 (`NG`) があるリソースを先頭にまとめ、`OK` だけのリソースは短くする。

```text
## 差異

| リソース | 項目 | 期待値 (定義) | 実体 | 判定 |
| -------- | ---- | ------------- | ---- | ---- |
| ...      | ...  | ...           | ...  | NG   |

## 確認済み

| リソース | 項目 | 期待値 (定義) | 実体 | 判定 |
| -------- | ---- | ------------- | ---- | ---- |
| ...      | ...  | ...           | ...  | OK   |

## 懸念

- state にあるが表に無い種別、API では確認できない引数 (force_destroy、s3_object_version など) の扱い
```

- 「期待値 (定義)」には値と一緒に読んだ場所 (`modules/storage/main.tf` の `local.work_expiration_days` など) を書く
- 差異があっても実体を直さない。定義側の修正案 (どのファイルのどの引数か) を添えて、apply の判断はユーザーに委ねる
- 報告はチャットに返し、**同じ内容を Markdown でリポジトリ直下の `tmp/verify-terraform-apply-result/{env}-{YYYYMMDD}-{HHMM}.md` に保存する** (例: `tmp/verify-terraform-apply-result/dev-20260816-2030.md`)。ユーザーに毎回指示させない
  - `tmp/` は `.gitignore` で git 管理外なので、アカウント ID や ARN をそのまま書いてよい。ファイル冒頭に「`tmp/` に置く理由 (git 管理外)、コミットしない」を 1 行書く
  - 先頭に入力 (`ENV` `REGION` `AWS_PROFILE` `ACCOUNT_ID`) と Step 2 の plan 結果、Step 3 の対象件数を置き、そのあとに上の「差異 / 確認済み / 懸念」を続ける。「確認済み」は Step 4 の節ごと (S3、DynamoDB、IAM、Lambda、Logs、Step Functions、EventBridge、SQS、SNS) に表を分ける
  - ディレクトリが無ければ作る。過去のレポートは消さない (時刻付きのファイル名で並べる)
- アカウント ID や ARN を含む表を git 管理下のファイルに書かない
