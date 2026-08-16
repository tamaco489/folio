# backend

English: [README.md](README.md)

Go 単一モジュール (`github.com/tamaco489/folio/backend`)。パイプラインの Lambda 5 本と、その共有層・純ロジックを持つ。
全体像は [docs/README.ja.md](../docs/README.ja.md) を参照。

## 前提

- Go は ルートの `.tool-versions` のバージョン ([asdf](https://asdf-vm.com/) で管理)
- `golangci-lint` も `.tool-versions` で固定している。CI の golangci-lint Action の `version:` と同じ値にする

## コマンド

タスクランナーは [just](https://github.com/casey/just)。`backend/` に移動して実行する。

```sh
cd backend
just fmt              # go fmt ./...
just vet              # go vet ./...
just lint             # golangci-lint run ./... (設定は .golangci.yml、CI と同じバージョン)
just test             # go test ./...
just fix-diff         # go fix -diff ./... (ドライラン)
just fix              # go fix ./...
just modernize        # gopls の modernize 解析器 (errorsastype など) の提案を表示する (ドライラン)
just modernize-fix    # modernize の提案を適用する
just cmds             # ビルド対象を列挙する (scripts/cmds.sh)
just build            # 全 Lambda をクロスコンパイルする (scripts/build.sh)
just build-one <cmd>  # 単一の Lambda をビルドする (scripts/build.sh <cmd>、例: pipeline/validator)
just package          # bin/{関数名}.zip に固める (scripts/package.sh、先に build を実行する)
just clean            # bin/ 配下の成果物を削除する (scripts/clean.sh)
just upload           # package のうえで bin/*.zip を artifacts バケットの lambda/ へアップロードする (scripts/upload.sh、実行はユーザー。反映は infra の just apply)
just upload-layer     # layers/pdf-processor/pdf-processor.zip を layers/ へアップロードする (先に build.sh で作る)
```

PR を出す前に `just fmt` `just vet` `just lint` `just test` に加え、`just fix-diff` と `just modernize` の提案が 0 件であることを確認する。

justfile にはシェルの処理を書かない。単一コマンドで済まないレシピは `scripts/` のスクリプトを呼ぶ。スクリプトは shellcheck で検査でき、just を介さず単体でも実行できる (自身で `backend/` へ移動するためカレントディレクトリは問わない)。

## ビルド

ビルド対象は `cmd/` 配下の `main.go` を探索して動的に決まるため、Lambda を追加しても justfile やスクリプトを変更する必要はない。

ビルドは `provided.al2023` / `arm64`、出力は `bin/{関数名}/bootstrap`。
`provided` ランタイムは実行ファイル名が `bootstrap` で固定される。
`bin/` は git 管理外で、成果物は手で編集せず `just build` / `just package` で再生成する。

## Lambda Layer

poppler のネイティブバイナリを Layer として配布する。

```sh
cd backend/layers/pdf-processor
./build.sh
```

詳細は [layers/pdf-processor/README.ja.md](layers/pdf-processor/README.ja.md) を参照。

## テスト

実 AWS を呼ばない。S3 と DynamoDB はフェイク (`internal/awsx/s3/s3test`、`internal/awsx/dynamo/dynamotest`)、Textract と Bedrock は `testdata/` の記録済みレスポンスの再生、Crossref は `internal/pipeline/verify/testdata/` の記録の再生で検証する。
規約は `.claude/rules/go/testing.md` を参照。

CI (`.github/workflows/ci-backend.yml`) では `go.mod` の依存関係に対して `trivy fs --scanners vuln` も実行し、HIGH 以上の脆弱性があれば失敗する。ローカルでは `backend/` で `trivy fs --scanners vuln --severity HIGH,CRITICAL --exit-code 1 .` を実行すると同じ検査ができる (Trivy の版はルートの `.tool-versions` で固定)。
