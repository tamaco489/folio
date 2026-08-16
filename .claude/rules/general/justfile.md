# justfile の規約

`backend/justfile` と、作る場合の `infra/justfile` に共通で適用する。

## justfile にシェルスクリプトを書かない

justfile はレシピの一覧と依存関係 (`package: build` など) を宣言する場所に留める。

- レシピは **単一コマンド 1 行** (`go test ./...` など) か **`scripts/` 配下のスクリプト呼び出し 1 行** にする
- `#!/usr/bin/env bash` のシェバン付きレシピや、複数行のシェル処理を書かない
- 複数行が必要な処理は `scripts/xxx.sh` を新設し、レシピはそれを呼ぶだけにする
- レシピ間の呼び出し (`{{ just_executable() }} other-recipe`) をシェル処理の中で使わない。スクリプト同士の直接呼び出しか関数化にする

## スクリプトの書き方

- `set -euo pipefail` を付け、実行権限 (`chmod +x`) を与え、shellcheck が通る状態にする
- 先頭でスクリプト自身の位置を基準に `cd` し、どこから呼ばれても壊れないようにする
- 冒頭コメントに使い方と前提を書く (手本: `backend/layers/pdf-processor/build.sh`)
