# backend

日本語: [README.ja.md](README.ja.md)

Single Go module (`github.com/tamaco489/folio/backend`) holding the five pipeline Lambda functions, their shared layer, and the pure logic packages.
See [docs/README.md](../docs/README.md) for the overall picture.

## Prerequisites

- Go version pinned in the root `.tool-versions` (managed with [asdf](https://asdf-vm.com/))
- `golangci-lint` is pinned in `.tool-versions` as well; keep it equal to the `version:` of the golangci-lint Action in CI

## Commands

The task runner is [just](https://github.com/casey/just). Change into `backend/` first.

```sh
cd backend
just fmt              # go fmt ./...
just vet              # go vet ./...
just lint             # golangci-lint run ./... (config: .golangci.yml, same version as CI)
just test             # go test ./...
just fix-diff         # go fix -diff ./... (dry run)
just fix              # go fix ./...
just modernize        # gopls modernize analyzers, e.g. errorsastype (dry run)
just modernize-fix    # Apply the modernize suggestions
just cmds             # List build targets (scripts/cmds.sh)
just build            # Cross-compile all Lambda functions (scripts/build.sh)
just build-one <cmd>  # Build a single function (scripts/build.sh <cmd>, e.g. pipeline/validator)
just package          # Archive into bin/{function}.zip (scripts/package.sh, runs build first)
just clean            # Remove artifacts under bin/ (scripts/clean.sh)
just upload           # package, upload bin/*.zip to lambda/ in the artifacts bucket, then swap each function's code with update-function-code (scripts/upload.sh; run by the user)
just upload-layer     # Upload layers/pdf-processor/pdf-processor.zip to layers/ (build it with build.sh first; applied with infra's just apply)
just submit <pdf>     # Put a PDF at uploads/<sha256>/original.pdf in the documents bucket to start the pipeline (scripts/submit.sh; incurs Textract / Bedrock charges, run by the user)
just status [jobId]   # List recent executions; with a jobId, show that execution's status, failure causes, DynamoDB item and S3 intermediates (scripts/status.sh; read-only)
just cleanup <jobId>  # Remove the DynamoDB item and uploads/ work/ outputs/ under the jobId so the PDF can be resubmitted (scripts/cleanup.sh; asks for confirmation, run by the user)
```

`submit`, `status` and `cleanup` take `env` as the second argument (default `dev`). `status` and `cleanup` use `AWS_REGION` (default `us-east-1`) rather than the profile's default region.

Before opening a PR, run `just fmt` `just vet` `just lint` `just test`, and confirm that `just fix-diff` and `just modernize` report nothing.

The justfile holds no shell logic. Recipes that need more than a single command call a script under `scripts/`, so the scripts can be checked with shellcheck and run without just (they change into `backend/` themselves, so the current directory does not matter).

## Build

Build targets are discovered by searching for `main.go` under `cmd/`, so adding a Lambda function does not require editing the justfile or the scripts.

Builds target `provided.al2023` on `arm64` and output to `bin/{function}/bootstrap`.
The `provided` runtime requires the executable to be named `bootstrap`.
`bin/` is not tracked by git; never edit artifacts by hand, regenerate them with `just build` / `just package`.

## Lambda Layer

The poppler native binaries are distributed as a Layer.

```sh
cd backend/layers/pdf-processor
./build.sh
```

See [layers/pdf-processor/README.md](layers/pdf-processor/README.md) for details.

## Tests

Tests never call real AWS. S3 and DynamoDB use fakes (`internal/awsx/s3/s3test`, `internal/awsx/dynamo/dynamotest`); Textract and Bedrock replay recorded responses under `testdata/`; Crossref replays recordings under `internal/pipeline/verify/testdata/`.
See `.claude/rules/go/testing.md` for the conventions.

CI (`.github/workflows/ci-backend.yml`) also runs `trivy fs --scanners vuln` against `go.mod` and fails on HIGH or CRITICAL vulnerabilities in dependencies; run `trivy fs --scanners vuln --severity HIGH,CRITICAL --exit-code 1 .` in `backend/` for the same check locally (Trivy is pinned in the root `.tool-versions`).
