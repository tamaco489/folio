# Terraform のコーディング規約

## ディレクトリの分け方

- 環境は workspace ではなく `infra/envs/{env}/` のディレクトリで分ける。Phase 1 は `dev` のみ
- 部品は `infra/modules/{name}/` に置く。`envs/{env}/main.tf` はモジュールを呼び出すだけで、リソース定義を書かない
- provider の設定 (`provider "aws" {}`、`default_tags`) と backend は環境ディレクトリにだけ書く。モジュールは `required_providers` で要件を宣言するだけにする

## モジュールのファイル構成

| ファイル       | 置くもの                                                                        |
| -------------- | ------------------------------------------------------------------------------- |
| `versions.tf`  | `terraform { required_version, required_providers }`。provider の設定は書かない |
| `variables.tf` | 入力変数。**すべてに `description` と `type`**、任意のものだけ `default`        |
| `main.tf`      | `locals` とリソース定義                                                         |
| `outputs.tf`   | 出力値。**すべてに `description`**                                              |

- リソースが多くて `main.tf` が肥大化するときは役割ごとに `<役割>.tf` へ分け (iam なら `statemachine.tf` `lambda_parser.tf` `textract_publish.tf`)、`main.tf` には `locals` と複数の役割で共有する部品 (信頼ポリシーなど) だけを残す。1 ファイル 1 つの関心にし、区切り線のコメントでセクションを作らない
- Terraform 内のリソース識別子は役割名にする (`aws_s3_bucket.documents`、`aws_dynamodb_table.jobs`)。同種が 1 つでも `this` にしない
- 使わない変数・出力を作らない。他モジュールが必要とする ARN は出力し、派生形 (`${arn}/*`、`${arn}/index/*`) は使う側で組み立てる

## 命名と値の渡し方

- リソース名は **モジュール内で `"${var.env}-folio-${local.name}"` として組み立てる**。環境ディレクトリは値 (`env` `account_id` `region`) を渡すだけで名前を組み立てない
- S3 バケット名だけ末尾に `-${var.account_id}` を付ける (全アカウントで一意にするため)
- `env` は `validation` で `dev` / `stg` / `prd` に限る。`account_id` は `validation` で 12 桁の数字を検査する
- **アカウント ID・API キー・接続情報・メールアドレスをファイルに書かない。** アカウント ID は `TF_VAR_account_id`、Crossref の連絡先は `TF_VAR_crossref_mailto` のように環境変数で渡し、`terraform.tfvars` には `env` `bedrock_model_id` のような公開してよい値だけを置く
- モジュール間の受け渡しは outputs 経由で行う。モジュール同士が互いの output を参照してもリソース単位の依存が循環しなければ plan は通るので、**`module` ブロックに `depends_on` を書かない** (モジュール全体の依存になり循環する)。値の参照は本当に使うリソースに閉じ、`for_each` のキーに他モジュールの output を使わない
- タグは環境ディレクトリの provider `default_tags` (`Project` `Environment` `ManagedBy`) で一括付与し、モジュール内で `tags` を書かない

## 書き方

- `terraform fmt` の整形に従う (2 スペースインデント、連続する引数の `=` を揃える、ブロック間は空行 1 つ)。`just fmt-check` を通す
- 非推奨の書き方をしない。`aws_s3_bucket` の中の `versioning` / `server_side_encryption_configuration` / `lifecycle_rule` ではなく個別リソース (`aws_s3_bucket_versioning` など) を使う。DynamoDB の GSI は `hash_key` / `range_key` ではなく `key_schema` を使う。provider の版に対する正しい書き方はドキュメントで確認する
- バージョンは `~>` で固定する (環境側は `required_version = "~> 1.15.0"`、provider は `~> 6.60`。モジュール側は `>= 1.15.0` の下限で足りる)。`.terraform.lock.hcl` はコミットする

## コメント

- 「なぜ」が自明でないときだけ書く。「何をしているか」は書かない
- 日本語で書き、句点 (。) を含めない。文の途中で改行せず 1 行で書く。長くなるなら 1 行 1 論点で行を分ける
- Issue の「要否を決める」に対して決めた内容は、**理由をリソースの直前にコメントで残す** (例: バージョニングを無効にする理由、`work/` の失効日数、KMS を使わない理由)
- `description` (変数・出力) は英語 1 文で、Terraform のドキュメント慣習に合わせる。コメントは日本語、`description` は英語と使い分ける
- 外部ツールや設計ドキュメントの所在をコメントに書かない
- 意図した無効化 (バージョニングや PITR を持たないなど) は、理由コメントの直後・リソース (またはネストしたブロック) の直前に `#trivy:ignore:<id>` を並べて `just scan` を通す。ignore 行はブロック直前に連続して置き、間に空行や別のコメント行を挟まない

## 実行

- `terraform fmt` / `validate` / `plan` は実行してよい。**`terraform apply` / `destroy` はユーザーだけが実行する**
- `just lint` (tflint) と `just scan` (trivy config) は AWS に触れないためローカルで実行してよい。PR を出す前に `just fmt-check` `just validate` `just lint` `just scan` を通す
- backend を変えた直後の `init` は `-reconfigure` を使う (旧 backend に state が無いことを確認したうえで)
- Lambda のコードは artifacts バケットの固定キーに置いた zip を `data "aws_s3_object"` の `version_id` (`s3_object_version`) で参照し、差し替えは `plan` / `apply` で反映する。`source_code_hash` と `aws lambda update-function-code` は使わない (Terraform を唯一の真実に保つ)。アップロード (`cd backend && just upload` / `just upload-layer`) はユーザーが行う
- justfile にシェルの処理を書かない (`.claude/rules/general/justfile.md`)
