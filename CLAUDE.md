# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## リポジトリの現状

`folio` はまだアプリケーションコードを持たない**ブートストラップ段階**のリポジトリ。
存在するのは Claude Code 設定 (`.claude/`)、GitHub テンプレート (`.github/`)、MCP 設定 (`.mcp.json`)、ドキュメントのみ。

ビルド・lint・テストのコマンドはまだ存在しない。
タスクランナーは Make ではなく [just](https://github.com/casey/just) を使う。
justfile はルートに置かず、`backend/` と `infra/` のそれぞれの直下に置く方針のため、実行は各ディレクトリに移動してから行う (例: `cd backend && just test`)。
最初にツールチェーンを追加する際はこの前提に合わせること。

## ディレクトリ構成

未作成。実装時は以下に従う。

```text
folio/
├── backend/                Go 単一モジュール (github.com/tamaco489/folio/backend)
│   ├── cmd/
│   │   ├── pipeline/       validator, preprocessor, textract-parser, bedrock-parser, finalizer
│   │   └── api/            public, admin (未作成)
│   ├── internal/
│   │   ├── config/         共有層
│   │   ├── domain/         共有層 — 構造化 JSON のスキーマ
│   │   ├── awsx/           共有層 — s3, dynamo, textract, bedrock
│   │   ├── pipeline/       pdf, extract, normalize, verify
│   │   └── api/            router, middleware, public, admin (未作成)
│   ├── tools/              fetch-corpus, build-truth, evaluate (デプロイ対象外)
│   ├── layers/             Lambda Layer のビルド定義 (Dockerfile + build.sh)
│   ├── justfile
│   └── go.mod
└── infra/                  Terraform
    ├── modules/            storage, messaging, compute, pipeline, iam
    ├── envs/               dev, stg, prd (workspace ではなくディレクトリ分離)
    └── justfile
```

- `cmd/` はサブシステム単位でグループ化し、階層を 2 段に揃える。片方だけフラットにしない
- Lambda 関数名は `cmd/` 以下のパスをハイフンで連結して導出する (`cmd/pipeline/validator` → `dev-folio-pipeline-validator`)
- `internal/` も同じ分け方。`config` `domain` `awsx` を共有層として最上位に置き、サブシステム固有は `pipeline/` `api/` にまとめる
- `internal/pipeline/` の 4 つは Step Functions の State に対応する (`pdf` 前処理、`extract` 抽出、`normalize` スキーマ正規化、`verify` 検証)
- `main.go` には `lambda.Start()` とハンドラの組み立てのみを置き、ロジックは `internal/` に配置する
- `pkg/` は設けない。外部から import される想定がないため
- `layers/` は中身で命名し、サブシステム軸では分けない。対応する Go パッケージは作らない
- ビルドは `provided.al2023` / `arm64`、出力は `bin/{関数名}/bootstrap`

命名規則やアーキテクチャの詳細は Notion の Develop データベース (親ページ: AWS AIP-C01) を参照する。

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

`md-linter` はプロジェクトルートの `.markdownlint.json` を設定として参照するが、まだ存在しない (未作成時は markdownlint-cli2 のデフォルトが適用される)。

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
