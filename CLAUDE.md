# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## リポジトリの現状

`folio` はまだアプリケーションコードを持たない**ブートストラップ段階**のリポジトリ。
コミットは `first commit` の 1 件のみで、存在するのは Claude Code 設定 (`.claude/`)、GitHub テンプレート (`.github/`)、MCP 設定 (`.mcp.json`) のみ。

ビルド・lint・テストのコマンドはまだ存在しない。
`.github/PULL_REQUEST_TEMPLATE.md` が `make test` を前提にしているため、ルートに `Makefile` を置いてタスクを集約する方針と読み取れる。
最初にツールチェーンを追加する際はこの前提に合わせること。

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
