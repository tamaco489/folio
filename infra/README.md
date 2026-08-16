# infra

日本語: [README.ja.md](README.ja.md)

Terraform configuration for Folio's AWS resources. Environments are separated by directory, not by workspace.

## Layout

```text
infra/
├── envs/
│   └── dev/            dev environment: provider, backend, variables, and module calls
├── modules/
│   ├── storage/        S3 (documents bucket) and DynamoDB (jobs table)
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

`terraform.tfvars` holds only `env`. `account_id` comes from `TF_VAR_account_id` and `region` from the default in `variables.tf` (`us-east-1`).
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
just validate      # scripts/validate.sh: init -backend=false + validate for envs/dev and every modules/* (no AWS credentials)
just lint          # scripts/lint.sh: tflint --init + tflint --recursive with .tflint.hcl
just scan          # trivy config (severity MEDIUM and above, dev tfvars applied)
```

`just apply` and `just destroy` are run by the user only. They do not pass `-auto-approve`; confirmation is left to Terraform's own prompt.

Commit `envs/dev/.terraform.lock.hcl`, which the first `just init` generates.
`just validate` also validates each module standalone (including ones not yet wired into `envs/dev`); the lock files it writes under `modules/*/` are ignored by git.

## CI

`.github/workflows/ci-infra.yml` runs on pull requests that touch `infra/**`, `.tool-versions`, or the workflow itself, with `permissions: contents: read` and no AWS credentials, so it also runs for pull requests from forks.
Three jobs run in parallel: `terraform fmt -check` and `just validate`, `just lint` (tflint with the `terraform` recommended preset and the `aws` ruleset), and `trivy config` (misconfiguration checks, failing on MEDIUM or higher).
`terraform plan` is intentionally not part of CI.
Settings disabled on purpose are suppressed with `#trivy:ignore:<id>` placed right after the reason comment above the resource, so `just scan` and CI report zero findings.
The check bundle is fetched at scan time; if a newly added check fails the job, either fix the configuration or add an ignore with a reason.
