# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## ツールチェーン

asdf で管理し、ルートの `.tool-versions` で固定している。

```text
golang    1.26.5
terraform 1.15.8
```

## コマンド

タスクランナーは Make ではなく [just](https://github.com/casey/just) を使う。
justfile はルートに置かず `backend/` と `infra/` の直下に置くため、実行は各ディレクトリに移動してから行う。

```sh
cd backend
just fmt              # go fmt ./...
just vet              # go vet ./...
just test             # go test ./...
just cmds             # ビルド対象を列挙する
just build            # 全 Lambda をクロスコンパイルする
just build-one <cmd>  # 単一の Lambda をビルドする (例: pipeline/validator)
just package          # bin/{関数名}.zip に固める
just clean            # bin/ 配下の成果物を削除する
```

## ディレクトリ構成

```text
folio/
├── backend/                Go 単一モジュール (github.com/tamaco489/folio/backend)
│   ├── cmd/
│   │   ├── pipeline/       validator, preprocessor, textract-parser, bedrock-parser, finalizer
│   │   └── api/            public, admin (Phase 1 の対象外)
│   ├── internal/
│   │   ├── config/         共有層 — 環境変数の読み込みと検証
│   │   ├── domain/         共有層 — 構造化 JSON のスキーマ
│   │   ├── awsx/           共有層 — s3, dynamo, textract, bedrock
│   │   ├── pipeline/       pdf, extract, normalize, verify
│   │   └── api/            router, middleware, public, admin (Phase 1 の対象外)
│   ├── tools/              fetch-corpus, build-truth, evaluate (デプロイ対象外)
│   ├── testdata/           textract/ と bedrock/ に記録済みレスポンス
│   ├── layers/             Lambda Layer のビルド定義 (Dockerfile + build.sh)
│   ├── justfile
│   ├── .golangci.yml
│   └── go.mod
└── infra/                  Terraform
    ├── modules/            storage, messaging, compute, pipeline, iam
    ├── envs/               dev, stg, prd (workspace ではなくディレクトリ分離)
    └── justfile
```

## コードの規約

共有層の実装で確立した型がある。新しいパッケージを書くときも、既存を変更するときもこれに揃える。

### AWS SDK のラップ

`internal/awsx/` の 4 パッケージは同じ形をしている。

- **SDK のクライアントではなく `API` インタフェースを受け取る。** 使うメソッドだけを列挙し、`New(api API, ...)` で組み立てる。`*dynamodb.Client` などは `API` を満たすのでそのまま渡せる
- **テストはフェイクで行い、実 AWS を呼ばない。** `s3test.Fake` のように別パッケージへ出す場合と、`fake_test.go` としてパッケージ内に閉じる場合がある。前者は他パッケージから使う想定があるとき
- **エラーはセンチネルで公開する** (`ErrNotFound` `ErrJobNotFound` `ErrJobInProgress` など)。呼び出し側は `errors.Is` / `errors.As` で判定でき、SDK の型に依存しない
- **バケット名・テーブル名は `New` に注入する。** パッケージ内で環境変数を読まない

### 課金 API の扱い

Textract と Bedrock は**記録・再生をインタフェース境界のデコレータとして実装している**。

`Recorder` は実 API を通しつつレスポンスを溜め、`Replayer` は記録から返す。
どちらも `API` (Bedrock は `Converser`) を満たすので、`New(replayer)` と差し替えるだけでページング畳み込みやリトライを含めた経路全体を実 API なしで検証できる。

記録は `backend/testdata/{textract,bedrock}/` に置く。
**実レスポンスでない記録には `note` フィールドでその旨を明記する。** 手書きや合成のフィクスチャを実物と誤認すると、通っているテストが何も保証しなくなる。

### テストで実時間・乱数に依存しない

待機・時刻・乱数は注入できる形にする。

- Bedrock の指数バックオフは `WithSleeper` と `WithRandN` で差し替え、テストは実待機しない (テスト全体で 0.2 秒未満)
- DynamoDB の `updatedAt` は `WithClock` で固定できる

### 外部バイナリの扱い

`internal/pipeline/pdf` は poppler を `os/exec` で呼ぶ。

- 実行ファイルのパスをハードコードせず、`WithBinDir()` → 環境変数 → `/opt/bin` → `PATH` の順で解決する
- CI にバイナリが無い可能性があるため、`exec.LookPath` で不在を検出したらテストを `t.Skip` する
- **S3 の読み書きをこの層に持ち込まない。** ローカルのファイルパスを受け渡し、S3 との橋渡しは Lambda 側 (`cmd/`) が担う

### 依存の扱い

`internal/awsx/` の 4 パッケージを並列に実装すると `go.sum` が競合するため、必要な AWS SDK 6 モジュールは先行して `go.mod` に載せてある。
以降の実装で新しい依存を追加しないこと。追加が必要なら理由を PR に書く。

`backend/tools/tools.go` は `//go:build tools` を付けた依存保持専用のファイル。
`aws-lambda-go` はどこからも参照されない間 `go mod tidy` で削除されるため、これで保持している。
**最初の `main.go` が入って参照先ができたら、このファイルは削除する。**

## Git / GitHub ワークフロー

コミットと PR の規約は `.claude/rules/github/` 配下に分割されている。プロジェクト指示として自動読み込みされるため、内容をここに再掲しない。

- `commit-types.md` — commit type の一覧
- `commit-subject.md` — subject の書き方 (日本語・50 文字以内・句点なし)
- `labels.md` — PR タイトルのラベル
- `pr-description.md` — PR タイトル/本文のフォーマットと 5W1H 方針

コミットから PR 作成までは `smart-commit` スキルが一連の手順 (差分のグループ化 → コミット → push → PR) を定義している。
push は Bash の `git push`、PR 作成は GitHub MCP を使う (`push_files` は使わない)。

## MCP / スキル

- `.mcp.json` — プロジェクト共有の MCP サーバは `context7` のみ。`.claude/settings.json` の `enabledMcpjsonServers` で有効化されている。
- `.claude/settings.json` — チーム共有の権限設定 (git/gh コマンドと GitHub MCP の一部を許可)。個人設定は `.claude/settings.local.json` (gitignore 済み)。
- スキル: `smart-commit` (コミット〜PR)、`md-linter` (Markdown 静的解析・修正)。

`md-linter` はプロジェクトルートの `.markdownlint.json` を設定として参照する (無い場合は markdownlint-cli2 のデフォルトが適用される)。

## 制約事項

> [!IMPORTANT]
>
> - **即時性は求めない。時間をかけてでも根拠に基づく正確なアウトプットを行う**
> - **公式ドキュメントや関連資料の調査はメインコンテキストを汚さないよう、別途調査用エージェントに委譲する**

- **コード変更前に必ずファイルを Read ツールで読む**
- **変更は diff 形式で提示し、承認 (y) を得てから実行する**
- **git commit はユーザーの承認を得てから実行する**
- 応答は日本語・簡潔・直接的
- コメントは「なぜ」が自明でない場合のみ書く (「何をしているか」は書かない)
- コメントに句点 (。) を含めない

## 禁止事項

- `rm -rf` の使用禁止 — ファイル削除は `rm -f` を使う
- 明示的な指示なしの変更禁止
- Git フック・署名のスキップ禁止 (`--no-verify`, `--no-gpg-sign`)
- `main` ブランチへの直接 push 禁止
- ビルド成果物の手動編集禁止 — `backend/bin/` と Layer の zip は `just` で再生成する
- 機密情報のハードコーディング禁止 (AWS アカウント ID, API キー, 接続情報)
  - `infra/envs/*/terraform.tfvars` はアカウント ID を含みうるため、値は環境変数から渡す
- 絵文字の使用禁止 (明示的に求められた場合を除く)
- **インフラ適用・AWS リソース操作の禁止** — 以下はユーザーのみが実行する。Claude が実行してはならない:
  - `terraform apply` / `terraform destroy` (`terraform plan` は可)
  - `aws lambda update-function-code`
  - `aws s3 cp` (Lambda 成果物・Layer のアップロード)
- **課金が発生する API の実呼び出し禁止** — Textract と Bedrock はユーザーの承認を得てから実行する。検証は記録済みレスポンスの再生で行う
