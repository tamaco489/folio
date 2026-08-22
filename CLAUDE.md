# CLAUDE.md

このファイルは Claude Code (claude.ai/code) がこのリポジトリで作業する際のガイダンスを提供します。

- `.claude/rules/general/` — 応答、作業の進め方、justfile の規約
- `.claude/rules/github/` — コミットと PR の規約
- `.claude/rules/go/` — Go のコーディングとテストの規約
- `.claude/rules/terraform/` — Terraform のコーディング規約
- [docs/README.ja.md](docs/README.ja.md) — プロジェクトの構成・アーキテクチャ
- [backend/README.ja.md](backend/README.ja.md) — backend の構成、Lambda とツールの役割
- [infra/README.ja.md](infra/README.ja.md) — Terraform の前提 (state バケット、`TF_VAR_account_id`) と使い方

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
  - アカウント ID は `infra/envs/*/terraform.tfvars` に書かず、環境変数 `TF_VAR_account_id` で渡す
- 絵文字の使用禁止 (明示的に求められた場合を除く)
- **インフラ適用・AWS リソース操作の禁止** — 以下はユーザーのみが実行する。Claude が実行してはならない:
  - `terraform apply` / `terraform destroy` (`terraform fmt` / `validate` / `plan` は可)
  - `aws lambda update-function-code` (`just upload` 経由を含む)
  - `aws s3 cp` (Lambda 成果物・Layer のアップロード)
  - AWS の読み取り (`aws sts get-caller-identity`、`describe-*` / `get-*` / `ls`) は確認目的で行ってよい
- **課金が発生する API の実呼び出し禁止** — Textract と Bedrock はユーザーの承認を得てから実行する。検証は記録済みレスポンスの再生で行う
