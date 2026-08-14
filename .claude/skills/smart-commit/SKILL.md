---
name: smart-commit
description: "git の差分を適切な粒度でグループ化し、コミットメッセージを生成してコミット・push・PR 作成まで行うスキル。「コミットして」「変更をコミットして push して」「PR 作成して」などのリクエストでトリガーする。"
---

# Smart Commit — 差分グループ化・自動コミットスキル

## 処理ステップ概要

| Step | 内容                   | 概要                                                                            |
| ---- | ---------------------- | ------------------------------------------------------------------------------- |
| 1    | 差分の取得             | `git status` / `git diff` で変更ファイルを把握する                              |
| 2    | ブランチ確認           | 現在のブランチを確認し、必要に応じてユーザーに確認を取る                        |
| 3    | グループ化             | 変更ファイルを機能・ディレクトリ・変更種別で適切にグループ化する                |
| 4    | コミットメッセージ生成 | テンプレートに従い各グループのコミットメッセージを生成する                      |
| 5    | コミット実行           | グループ単位で `git add` → `git commit` を順に実行する                          |
| 6    | push                   | Bash の `git push` で現在のブランチを push する                                 |
| 7    | PR 確認・作成 (任意)   | main ブランチ以外の場合、既存 PR を確認してから必要に応じて作成・ラベル設定する |
| 8    | 結果報告               | コミット数・push 結果・PR URL をユーザーに報告する                              |

## 制約事項

> [!IMPORTANT]
>
> - **コミットは必ず1グループずつ順番に実行する** (並列実行禁止)
> - `.env` やシークレットを含む可能性があるファイルはコミット前にユーザーに確認する
>   [!NOTE]
> - コミットメッセージは `.claude/rules/github/commit-types.md` と
>   `.claude/rules/github/commit-subject.md` に従う
> - push には GitHub MCP の `push_files` ではなく Bash の `git push` を使う
> - PR 作成は GitHub MCP (`create_pull_request`) を使う
> - PR のタイトル・本文は `.claude/rules/github/pr-description.md` に従う

## ブランチ別の動作

| 現在のブランチ | 確認事項                                                          |
| -------------- | ----------------------------------------------------------------- |
| `main`         | main へ直接コミットしてよいか確認する (y/n)                       |
| `main` 以外    | push 後に既存 PR を確認し、なければ PR を作成するか確認する (y/n) |

どちらも **No** の場合はスキルを中断し、ユーザーに次の対応を案内する。

---

## 各ステップの詳細

### Step 1: 差分の取得

以下のコマンドで変更状況を把握する。

```bash
git status
git diff --stat HEAD
```

- 未追跡ファイル・変更済みファイル・削除済みファイルをすべて把握する
- 変更がない場合はスキルを中断してユーザーに報告する

### Step 2: ブランチ確認

```bash
git branch --show-current
```

現在のブランチに応じてユーザーへ確認を取る。

- **main の場合**: 「現在 `main` ブランチです。main へ直接コミットしてもよいですか？ (y/n)」
- **main 以外の場合**: コミット・push 後に「PR を作成しますか？ (y/n)」と確認する (Step 6 完了後)

**No** の場合: 処理を中断する。main の場合は新規ブランチ作成を案内する。

### Step 3: グループ化

変更ファイルを以下の優先順位でグループ化する。

**グループ化の基準:**

| 優先度 | 基準                   | 例                                    |
| ------ | ---------------------- | ------------------------------------- |
| 1      | 論理的なまとまり       | 新機能1つ、バグ修正1つ、など目的単位  |
| 2      | ディレクトリのまとまり | `.claude/rules/` 配下の変更をまとめる |
| 3      | 変更種別のまとまり     | 設定ファイル群、ドキュメント群など    |

**グループ化のルール:**

- 1コミットに含めるファイルは「同じ目的・同じ文脈」のものに限る
- 関係のないファイルを1つのコミットに混在させない
- 1ファイルのみの変更でもそのままグループ単体でコミットしてよい

グループ化の結果をユーザーに提示し、問題がなければ Step 4 へ進む。

**提示フォーマット:**

```text
以下のグループでコミットします。よろしいですか？

[グループ 1] feat: AppSync Subscription ハンドラを追加
  - backend/lambda/handler.go

[グループ 2] chore: commit rules を追加
  - .claude/rules/github/commit-types.md
  - .claude/rules/github/commit-subject.md
```

### Step 4: コミットメッセージ生成

`.claude/rules/github/commit-types.md` と
`.claude/rules/github/commit-subject.md` に従いメッセージを生成する。

### Step 5: コミット実行

グループ単位で順番にコミットする。

```bash
git add <対象ファイル...>
git commit -m "<type>: <subject>"
```

**注意事項:**

> [!IMPORTANT]
>
> - `git add -A` や `git add .` は使わず、ファイルを明示的に指定する
> - コミット前に `.env` やシークレットらしいファイルが含まれている場合はユーザーに確認する

### Step 6: push

全コミット完了後、Bash で push する。

```bash
git push origin <current-branch>
```

main ブランチへの初回 push の場合は `-u` フラグを付ける。

> [!NOTE]
>
> main ブランチへの force push は絶対に行わない。

### Step 7: PR 確認・作成 (任意)

push 完了後、GitHub MCP (`list_pull_requests`) で現在のブランチの PR が既に存在するか確認する。

```text
既存 PR あり → PR の URL をユーザーに報告して終了（新規作成しない）
既存 PR なし → 「PR を作成しますか？ (y/n)」とユーザーに確認する
```

**No** の場合はそのまま Step 8 へ進む。

**Yes** の場合は GitHub MCP (`create_pull_request`) で PR を作成する。

PR のタイトル・本文は `.claude/rules/github/pr-description.md` に従う。

- base ブランチは `main` に固定する
- PR 作成後、GitHub MCP (`issue_write`) で `.claude/rules/github/labels.md` に従いラベルを設定する
- PR の URL をユーザーに報告する

### Step 8: 結果報告

以下をユーザーに報告する。

- 作成したコミット数とその一覧 (hash・メッセージ)
- push 先のブランチ名
- PR URL (作成した場合のみ)
- エラーが発生した場合はその内容と原因
