# infra

English: [README.md](README.md)

Folio の AWS リソースを Terraform で管理する。環境は workspace ではなくディレクトリで分離する。

## ディレクトリ構成

```text
infra/
├── envs/
│   └── dev/            dev 環境。provider・backend・変数を持ち、modules/ を呼び出す
├── modules/
│   ├── storage/        S3 (documents バケット) と DynamoDB (jobs テーブル)
│   ├── messaging/      EventBridge ルールと SNS (Textract 完了通知)
│   ├── compute/        Lambda 関数と Layer
│   ├── pipeline/       Step Functions ステートマシン
│   └── iam/            Lambda・Step Functions・Textract のロールとポリシー
└── justfile            レシピの宣言のみ
```

リソース名は `{env}-folio-{name}` の形でモジュール内で組み立てる。環境ディレクトリは `env` `account_id` `region` の値を渡すだけで、名前は組み立てない。
タグ (`Project` `Environment` `ManagedBy`) は環境ディレクトリの provider `default_tags` で一括付与し、モジュール内では個別に付けない。

Phase 1 の環境は `dev` のみ。`stg` `prd` のディレクトリは作らない。

## 前提

| 項目           | 内容                                                                                                                                            |
| -------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| Terraform      | ルートの `.tool-versions` のバージョン ([asdf](https://asdf-vm.com/) で管理)                                                                    |
| AWS 認証       | `AWS_PROFILE` などで対象アカウントの認証情報を用意する。リージョンは `us-east-1`                                                                |
| アカウント ID  | 環境変数 `TF_VAR_account_id` に 12 桁で設定する (documents バケット名に使う)。ファイルには書かない                                              |
| state バケット | 環境ごとの `{env}-folio-tfstate` (`ap-northeast-1`) が存在すること (dev は `dev-folio-tfstate`)。Terraform の管理外で、ユーザーが事前に作成する |

`terraform.tfvars` には `env` だけを置く。`account_id` は `TF_VAR_account_id` から、`region` は `variables.tf` の default (`us-east-1`) から入る。
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
just validate      # terraform -chdir=envs/dev validate
just plan          # terraform -chdir=envs/dev plan
just fmt           # terraform fmt -recursive
just fmt-check     # terraform fmt -check -recursive
```

`just apply` と `just destroy` はユーザーが実行する。`-auto-approve` は付けておらず、確認は Terraform 側の対話に任せる。

初回の `just init` で生成される `envs/dev/.terraform.lock.hcl` はコミットする。
