# infra

English: [README.md](README.md)

Folio の AWS リソースを Terraform で管理する。環境は workspace ではなくディレクトリで分離する。

## ディレクトリ構成

```text
infra/
├── envs/
│   └── dev/            dev 環境。provider・backend・変数を持ち、modules/ を呼び出す
├── modules/
│   ├── storage/        S3 (documents / artifacts バケット) と DynamoDB (jobs テーブル)
│   ├── messaging/      EventBridge ルールと SNS (Textract 完了通知)
│   ├── compute/        Lambda 関数と Layer
│   ├── pipeline/       Step Functions ステートマシン
│   └── iam/            Lambda・Step Functions・Textract のロールとポリシー
├── scripts/            justfile から呼ぶシェルスクリプト (validate, lint)
├── .tflint.hcl         TFLint の設定 (CI と just lint で共用)
└── justfile            レシピの宣言のみ
```

リソース名は `{env}-folio-{name}` の形でモジュール内で組み立てる。環境ディレクトリは `env` `account_id` `region` の値を渡すだけで、名前は組み立てない。
タグ (`Project` `Environment` `ManagedBy`) は環境ディレクトリの provider `default_tags` で一括付与し、モジュール内では個別に付けない。

Phase 1 の環境は `dev` のみ。`stg` `prd` のディレクトリは作らない。

## 前提

| 項目           | 内容                                                                                                                                            |
| -------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| Terraform      | ルートの `.tool-versions` のバージョン ([asdf](https://asdf-vm.com/) で管理)                                                                    |
| tflint         | 同じく `.tool-versions` で固定し、CI も同じ行を読む。`just lint` の `tflint --init` が aws ルールセットを GitHub から取得する                   |
| Trivy          | 同じく `.tool-versions` で固定し、CI も同じ行を読む。`just scan` はチェック定義をスキャン時に取得する                                           |
| AWS 認証       | `AWS_PROFILE` などで対象アカウントの認証情報を用意する。リージョンは `us-east-1`                                                                |
| アカウント ID  | 環境変数 `TF_VAR_account_id` に 12 桁で設定する (documents バケット名に使う)。ファイルには書かない                                              |
| state バケット | 環境ごとの `{env}-folio-tfstate` (`ap-northeast-1`) が存在すること (dev は `dev-folio-tfstate`)。Terraform の管理外で、ユーザーが事前に作成する |

`terraform.tfvars` には `env` と `bedrock_model_id` だけを置く。`account_id` は `TF_VAR_account_id` から、`region` は `variables.tf` の default (`us-east-1`) から入る。
plan の段階で `TF_VAR_account_id` と認証情報のアカウントが一致することを検査する。

## state

state は環境ごとの S3 バケット `{env}-folio-tfstate` に置き、key は `envs/{env}/terraform.tfstate` とする。
state バケットは `ap-northeast-1`、リソースは `us-east-1` にある。backend の `region` は state バケットの所在を指し、provider の `region` とは独立している。
ロックは S3 ネイティブロック (`use_lockfile = true`) を使い、DynamoDB のロックテーブルは使わない。

## 使い方

`infra/` に移動して `just` を実行する。環境は `env` 変数で切り替える (既定は `dev`)。

```sh
cd infra
just init          # terraform -chdir=envs/dev init
just plan          # terraform -chdir=envs/dev plan
just fmt           # terraform fmt -recursive
just fmt-check     # terraform fmt -check -recursive
just validate      # scripts/validate.sh: envs/dev と modules/* を init -backend=false のうえで validate する
just lint          # scripts/lint.sh: tflint --init と tflint --recursive (.tflint.hcl)
just scan          # trivy config (MEDIUM 以上、dev の tfvars を適用)
```

`just apply` と `just destroy` はユーザーが実行する。`-auto-approve` は付けておらず、確認は Terraform 側の対話に任せる。

初回の `just init` で生成される `envs/dev/.terraform.lock.hcl` はコミットする。
`just validate` は各モジュールも単体で validate する。その際に `modules/*/` に書かれる lock ファイルは git 管理外にしている。
一度 S3 backend で `just init` した checkout では、`just validate` の `init -backend=false` が既存 backend の確認で STS を呼ぶため、有効な認証情報が要る (CI や未 init の checkout では不要)。

## モジュールの結線と apply の順序

`envs/dev/main.tf` は 5 つのモジュールを結線し、値は outputs で受け渡す。
モジュール同士が互いの出力を参照する箇所 (iam ↔ messaging、iam ↔ compute、messaging ↔ pipeline) があるが、リソース単位の依存は循環しないため plan は通る。`module` ブロックに `depends_on` を書かないこと。

環境側の変数は `terraform.tfvars` の `env` と `bedrock_model_id` (公開してよい値) のみで、`account_id` は `TF_VAR_account_id`、Crossref の連絡先 `crossref_mailto` はメールアドレスなので必要なら `TF_VAR_crossref_mailto` で渡す (空なら Lambda に環境変数を設定しない)。

### zip の配置

compute モジュールは Lambda の zip と Layer の zip を artifacts バケット (`{env}-folio-artifacts-{account_id}`、バージョニング有効) の**固定キー**に置く。
zip が無いと plan の段階で失敗するので、先に置く。

| キー                                | 作り方                                                                 |
| ----------------------------------- | ---------------------------------------------------------------------- |
| `lambda/pipeline-{name}.zip` (5 本) | `cd backend && just package` → `backend/bin/pipeline-{name}.zip`       |
| `layers/pdf-processor.zip`          | `backend/layers/pdf-processor/build.sh` (Docker、poppler の更新時だけ) |

```sh
cd backend
just upload          # package → bin/pipeline-*.zip を lambda/ へ置き、各関数のコードを update-function-code で差し替える (scripts/upload.sh)
just upload-layer    # layers/pdf-processor/pdf-processor.zip を layers/ へ (先に layers/pdf-processor/build.sh)。反映は just plan → just apply
```

バケット名は `{env}-folio-artifacts-{アカウント ID}` で、アカウント ID は `TF_VAR_account_id` (未設定なら `aws sts get-caller-identity`) から取る。アップロードはユーザーが実行する。
関数のコードは `just upload` が `aws lambda update-function-code` で差し替え、Terraform は関数の設定 (ロール、timeout、環境変数など) だけを管理する。`s3_object_version` と `source_code_hash` を書かないため、upload 後の `just plan` に差分は出ない。
Layer は版が不変で関数側の `layers` の更新が要るため、`data "aws_s3_object"` の `version_id` を `s3_object_version` に渡し、`just upload-layer` の後に `just plan` → `just apply` で反映する。

### 初回の apply

artifacts バケットは storage モジュールが作るため、初回だけ 2 段階になる。

```sh
aws sso login --profile <profile>
export AWS_PROFILE=<profile>
export TF_VAR_account_id=$(aws sts get-caller-identity --query Account --output text)

cd infra
just init
terraform -chdir=envs/dev apply -target=module.storage

cd ../backend
just upload
just upload-layer

cd ../infra
just plan
just apply
```

2 回目以降は関数のコードなら `just upload` だけでよい。Layer を変えたときは `just upload-layer` → `just plan` → `just apply`。
