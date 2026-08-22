# infra

日本語: [README.ja.md](README.ja.md)

Terraform configuration for Folio's AWS resources. Environments are separated by directory, not by workspace.

## Layout

```text
infra/
├── envs/
│   └── dev/            dev environment: provider, backend, variables, and module calls
├── modules/
│   ├── storage/        S3 (documents / artifacts buckets) and DynamoDB (jobs table)
│   ├── messaging/      EventBridge rule and SNS topic (Textract completion)
│   ├── compute/        Lambda functions and layer
│   ├── pipeline/       Step Functions state machine
│   └── iam/            Roles and policies for Lambda, Step Functions, and Textract
├── scripts/            Shell scripts called by the justfile (validate, lint)
├── .tflint.hcl         TFLint configuration shared by CI and just lint
└── justfile            Recipe declarations only
```

Resource names follow `{env}-folio-{name}` and are assembled inside the modules. The environment directory only passes values (`env`, `account_id`, `region`); it never builds names.
Tags (`Project`, `Environment`, `ManagedBy`) are applied once through the provider's `default_tags` in the environment directory, not per resource in modules.

Phase 1 has a single environment, `dev`. No `stg` or `prd` directory is created.

## Prerequisites

| Item         | Detail                                                                                                                                             |
| ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| Terraform    | Version pinned in the root `.tool-versions` (managed with [asdf](https://asdf-vm.com/))                                                            |
| tflint       | Version pinned in `.tool-versions` as well; CI reads the same line. `just lint` runs `tflint --init`, which downloads the aws ruleset from GitHub  |
| Trivy        | Version pinned in `.tool-versions` as well; CI reads the same line. `just scan` fetches the check bundle at scan time                              |
| AWS auth     | Credentials for the target account (e.g. `AWS_PROFILE`). Resources live in `us-east-1`                                                             |
| Account ID   | Set the 12-digit ID in `TF_VAR_account_id` (used in the documents bucket name). Never written to a file                                            |
| State bucket | `{env}-folio-tfstate` (`ap-northeast-1`) must exist per environment (`dev-folio-tfstate` for dev). It is outside Terraform and created by the user |

`terraform.tfvars` holds only `env` and `bedrock_model_id`. `account_id` comes from `TF_VAR_account_id` and `region` from the default in `variables.tf` (`us-east-1`).
`plan` fails early if `TF_VAR_account_id` does not match the account of the current credentials.

## State

State lives in a per-environment S3 bucket `{env}-folio-tfstate`, keyed as `envs/{env}/terraform.tfstate`.
The state bucket is in `ap-northeast-1` while resources are in `us-east-1`; the backend `region` refers to the bucket's location and is independent of the provider `region`.
Locking uses S3 native locking (`use_lockfile = true`); no DynamoDB lock table is used.

## Usage

Change into `infra/` and run `just`. Select the environment with the `env` variable (default `dev`).

```sh
cd infra
just init          # terraform -chdir=envs/dev init
just plan          # terraform -chdir=envs/dev plan
just fmt           # terraform fmt -recursive
just fmt-check     # terraform fmt -check -recursive
just validate      # scripts/validate.sh: init -backend=false + validate for envs/dev and every modules/*
just lint          # scripts/lint.sh: tflint --init + tflint --recursive with .tflint.hcl
just scan          # trivy config (severity MEDIUM and above, dev tfvars applied)
```

`just apply` and `just destroy` are run by the user only. They do not pass `-auto-approve`; confirmation is left to Terraform's own prompt.

Commit `envs/dev/.terraform.lock.hcl`, which the first `just init` generates.
`just validate` also validates each module standalone; the lock files it writes under `modules/*/` are ignored by git.
In a checkout that has already run `just init` against the S3 backend, the `init -backend=false` inside `just validate` calls STS while checking the existing backend, so valid credentials are needed (not in CI or in a fresh checkout).

## Wiring the modules and the apply order

`envs/dev/main.tf` wires the five modules and passes values through outputs.
Some modules reference each other's outputs (iam <-> messaging, iam <-> compute, messaging <-> pipeline); the resource-level graph has no cycle, so plan succeeds. Do not add `depends_on` to `module` blocks.

The environment-level variables in `terraform.tfvars` are only `env` and `bedrock_model_id` (both safe to publish). `account_id` comes from `TF_VAR_account_id`, and the Crossref contact `crossref_mailto` is an email address, so pass it with `TF_VAR_crossref_mailto` if needed (empty means the Lambda gets no such variable).

### Placing the zips

The compute module takes the Lambda zips and the Layer zip from **fixed keys** in the artifacts bucket (`{env}-folio-artifacts-{account_id}`, versioning enabled).
Plan fails when a zip is missing, so upload first.

| Key                              | How to build                                                                |
| -------------------------------- | --------------------------------------------------------------------------- |
| `lambda/pipeline-{name}.zip` (5) | `cd backend && just package` -> `backend/bin/pipeline-{name}.zip`           |
| `layers/pdf-processor.zip`       | `backend/layers/pdf-processor/build.sh` (Docker; only when poppler changes) |

```sh
cd backend
just upload          # package -> bin/pipeline-*.zip to lambda/, then swap each function's code with update-function-code (scripts/upload.sh)
just upload-layer    # layers/pdf-processor/pdf-processor.zip to layers/ (build it with layers/pdf-processor/build.sh first); apply with just plan -> just apply
```

The bucket is `{env}-folio-artifacts-{account_id}`; the account ID comes from `TF_VAR_account_id` (or `aws sts get-caller-identity` when unset). The upload is run by the user.
`just upload` swaps each function's code with `aws lambda update-function-code`; Terraform manages only the function configuration (role, timeout, environment variables, ...). Neither `s3_object_version` nor `source_code_hash` is set, so `just plan` shows no diff after an upload.
Layer versions are immutable and the functions' `layers` reference must follow, so the Layer keeps `data "aws_s3_object"`'s `version_id` in `s3_object_version` and is applied with `just plan` -> `just apply` after `just upload-layer`.

### First apply

The artifacts bucket is created by the storage module, so only the first run takes two steps.

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

From then on, function code needs only `just upload`. When the Layer changes: `just upload-layer` -> `just plan` -> `just apply`.
