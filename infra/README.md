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
│   └── iam/            Roles and policies for Lambda, Step Functions, and Textract; OIDC provider and role for GitHub Actions
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

`terraform.tfvars` holds only `env`, `bedrock_model_id`, and `github_repository`. `account_id` comes from `TF_VAR_account_id` and `region` from the default in `variables.tf` (`us-east-1`).
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

The environment-level variables in `terraform.tfvars` are only `env`, `bedrock_model_id`, and `github_repository` (all safe to publish). `account_id` comes from `TF_VAR_account_id`, and the Crossref contact `crossref_mailto` is an email address, so pass it with `TF_VAR_crossref_mailto` if needed (empty means the Lambda gets no such variable).

### Placing the zips

The compute module reads the Lambda zips and the Layer zip from **fixed keys** in the artifacts bucket (`{env}-folio-artifacts-{account_id}`, versioning enabled) with `data "aws_s3_object"` and passes `version_id` to `s3_object_version`.
Plan fails when a zip is missing, so upload first.

| Key                              | How to build                                                                |
| -------------------------------- | --------------------------------------------------------------------------- |
| `lambda/pipeline-{name}.zip` (5) | `cd backend && just package` -> `backend/bin/pipeline-{name}.zip`           |
| `layers/pdf-processor.zip`       | `backend/layers/pdf-processor/build.sh` (Docker; only when poppler changes) |

```sh
cd backend
just upload          # package -> bin/pipeline-*.zip to lambda/ (scripts/upload.sh)
just upload-layer    # layers/pdf-processor/pdf-processor.zip to layers/ (build it with layers/pdf-processor/build.sh first)
```

The bucket is `{env}-folio-artifacts-{account_id}`; the account ID comes from `TF_VAR_account_id` (or `aws sts get-caller-identity` when unset). Overwriting the same key creates a new `version_id`, and the next `just plan` detects the function (and Layer) update. Apply it with `just apply`; do not use `aws lambda update-function-code` (Terraform stays the single source of truth).

`just upload` can also run from CI. Running `.github/workflows/cd-backend.yml` manually from Actions (`workflow_dispatch`, input `env`; only `dev` in Phase 1) checks out `main`, runs `just upload`, and re-uploads the zips under `lambda/`. Beforehand, register the value of `terraform -chdir=envs/dev output -raw github_actions_role_arn` as the GitHub secret `AWS_ROLE_ARN` (a secret, not a variable, because the ARN contains the account ID). The Layer is not part of CI (it needs a Docker build and changes only when poppler does; run `just upload-layer` locally). CI stops at S3 as well; applying is still `just plan` -> `just apply`.

### OIDC role for GitHub Actions

The iam module creates the OIDC provider (`token.actions.githubusercontent.com`, audience `sts.amazonaws.com`) and the role `{env}-folio-github-actions-role` that `cd-backend.yml` assumes. The trust policy is limited to `aud = sts.amazonaws.com` and `sub = repo:{github_repository}:ref:refs/heads/main`, so no other branch or repository can assume it. Its only permissions are `s3:PutObject` (plus `s3:AbortMultipartUpload` for multipart uploads) on `lambda/*` and `layers/*` in the artifacts bucket; it cannot touch Lambda or the Terraform state.
`github_repository` lives in `terraform.tfvars`. It is the part of the token's `sub` claim after `repo:`; this repository was created after 2026-07-15 and therefore uses the immutable subject claim format (`owner@id/repo@id`), so take the value of `gh api repos/tamaco489/folio/actions/oidc/customization/sub --jq .sub_claim_prefix` without the `repo:` prefix.
The OIDC provider ARN is derived from the URL, so an account can hold only one. When adding stg / prd to the same account, move the provider out of the iam module (Phase 1 is dev only). `thumbprint_list` is not set (GitHub's certificate is verified against AWS's library of trusted root CAs, so the value is unused).

### First apply

The artifacts bucket is created by the storage module, so only the first run takes two steps.

1. `just apply` with only `module "storage"` in `envs/dev/main.tf` (creates the artifacts bucket)
2. Upload the zips as above
3. `just plan` -> `just apply` with the remaining modules wired

From then on it is just "re-upload the zips -> `just plan` -> `just apply`".

## CI

`.github/workflows/ci-infra.yml` runs on pull requests that touch `infra/**`, `.tool-versions`, or the workflow itself, with `permissions: contents: read` and no AWS credentials, so it also runs for pull requests from forks.
Three jobs run in parallel: `terraform fmt -check` and `just validate`, `just lint` (tflint with the `terraform` recommended preset and the `aws` ruleset), and `trivy config` (misconfiguration checks, failing on MEDIUM or higher).
`terraform plan` is intentionally not part of CI.
Settings disabled on purpose are suppressed with `#trivy:ignore:<id>` placed right after the reason comment above the resource, so `just scan` and CI report zero findings.
The check bundle is fetched at scan time; if a newly added check fails the job, either fix the configuration or add an ignore with a reason.
