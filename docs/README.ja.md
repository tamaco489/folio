# folio

English: [README.md](README.md)

論文 PDF を構造化 JSON へ変換する、AWS 上のドキュメント処理パイプライン。

## 目的

論文は検証の題材であり、本来の適用先は組織内文書 (設計判断の経緯、障害対応記録など) である。
論文を選んだのは LaTeX ソースから正解データを機械生成でき、抽出精度を数値で評価できるため。

Phase 1 の到達点は、2 つの抽出経路の出力を比較できる状態にすること。

## アーキテクチャ

![folio のアーキテクチャ](images/architecture.svg)

図の編集元は [images/architecture.drawio](images/architecture.drawio)。更新したら SVG を再エクスポートする。

S3 へのアップロードを起点とするイベント駆動構成。処理は非同期に進む。

| 層     | 責務                               | 主なサービス      |
| ------ | ---------------------------------- | ----------------- |
| 受け口 | PDF の受領とイベント発火           | S3, EventBridge   |
| 制御   | 処理順序、分岐、並列制御、リトライ | Step Functions    |
| 処理   | 各ステップの実行                   | Lambda            |
| 抽出   | 文書からのテキストと構造の取得     | Textract, Bedrock |
| 保持   | 中間データ、処理状態、成果物       | S3, DynamoDB      |

制御と処理を分離している。Lambda は個々の処理だけを担い、順序・分岐・リトライは Step Functions が宣言的に持つ。

### 2 つの抽出経路

同一の PDF を 2 つの経路に通し、出力を比較する。
Choice による選択ではなく **Parallel による並走**とするのは、比較そのものが目的であるため。

| 観点       | 経路 A                              | 経路 B                             |
| ---------- | ----------------------------------- | ---------------------------------- |
| 処理の分担 | Textract で読み取り、Bedrock で解釈 | Bedrock がページ画像から一体で処理 |
| 表の構造   | 行列として保たれる                  | 崩れやすい                         |
| 二段組     | 段として分離できる                  | 読み順が乱れることがある           |
| コスト     | ページ単位で安価                    | トークン課金で高い                 |
| 日本語     | 扱えない                            | 扱える                             |
| 根拠の追跡 | 原本の座標が残る                    | 残らない                           |

Textract は日本語に対応しないため、日本語 PDF は経路 B のみを通る。

### Lambda

| 関数              | 責務                                         |
| ----------------- | -------------------------------------------- |
| `validator`       | 入力の妥当性判定と冪等性チェック             |
| `preprocessor`    | ラスタライズとテキストレイヤー抽出           |
| `textract-parser` | 経路 A。Textract の出力を Bedrock で構造化   |
| `bedrock-parser`  | 経路 B。ページ画像を Bedrock に投入 (Map 内) |
| `finalizer`       | スキーマ正規化、検証、永続化                 |

関数名は `cmd/` 以下のパスをハイフンで連結して導出する。
`cmd/pipeline/validator` から `dev-folio-pipeline-validator` となる。

### S3 のキー設計

同一バケット内でプレフィックスにより役割を分ける。

```text
uploads/{jobId}/original.pdf          受領した PDF。イベント発火点
work/{jobId}/pages/page-NNNN.png      ラスタライズ結果
work/{jobId}/textract/raw.json        Textract の生出力
work/{jobId}/textract/callback.json   Textract 完了通知への応答に要する情報 (タスクトークンなど)
work/{jobId}/bedrock/page-NNNN.json   経路 B のページ単位の抽出結果
work/{jobId}/text/layer.txt           テキストレイヤー抽出結果
outputs/{jobId}/result-textract.json  経路 A の抽出結果
outputs/{jobId}/result-bedrock.json   経路 B の抽出結果
outputs/{jobId}/comparison.json       両経路の差分と評価
```

イベント通知は `uploads/` プレフィックスと `.pdf` サフィックスの両方でフィルタする。
これを怠ると `work/` や `outputs/` への書き込みが自身を再起動し、無限ループとなる。

`jobId` はファイルのハッシュから導出する。同一ファイルの再アップロードが同じ `jobId` に落ち、冪等性の判定と噛み合う。

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

`internal/pipeline/` の 4 つは Step Functions の State に対応する。
`pdf` が前処理、`extract` が抽出、`normalize` がスキーマ正規化、`verify` が検証。

## ツールチェーン

[asdf](https://asdf-vm.com/) で管理し、ルートの `.tool-versions` で固定している。

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

ビルド対象は `cmd/` 配下の `main.go` を探索して動的に決まるため、Lambda を追加しても justfile を変更する必要はない。

ビルドは `provided.al2023` / `arm64`、出力は `bin/{関数名}/bootstrap`。
`provided` ランタイムは実行ファイル名が `bootstrap` で固定される。

### Lambda Layer

poppler のネイティブバイナリを Layer として配布する。

```sh
cd backend/layers/pdf-processor
./build.sh
```

詳細は [backend/layers/pdf-processor/README.ja.md](../backend/layers/pdf-processor/README.ja.md) を参照。

## CI

`.github/workflows/ci-backend.yml` が `pull_request` で動く。
`golangci-lint` と `build-test` の 2 ジョブ構成。

actions は full commit SHA でピン留めしている。タグは付け替えられるため。

CI から AWS の実 API に到達できないようにしてある。
`permissions` は `contents: read` のみで OIDC を与えず、加えて `AWS_EC2_METADATA_DISABLED=true` で IMDS も塞ぐ。

## 設計ドキュメント

命名規則、アーキテクチャの詳細、DynamoDB 設計、リージョン選定、Lambda の配布方式、Textract の FeatureTypes 選定、対象論文の選定条件は Notion の Develop データベース (親ページ: AWS AIP-C01) にある。

新しい設計判断は ADR 形式 (ステータス → 背景 → 選択肢 → 比較 → 決定 → 理由 → 結果 → 再検討の条件) で追記する。

## 制約

| 制約                | 内容                               | 対処                                 |
| ------------------- | ---------------------------------- | ------------------------------------ |
| ペイロード上限      | Step Functions の State 間は 256KB | 実体は S3、State 間はキーのみ        |
| 同期 API のページ数 | Textract の同期処理は PDF 1 ページ | 非同期 API と SNS 完了通知           |
| 非同期処理の上限    | PDF は 500MB、3,000 ページ         | バリデーション層で弾く               |
| 対応言語            | Textract は日本語非対応            | 日本語は経路 B のみを通す            |
| スロットリング      | Bedrock の同時実行数に上限         | `MaxConcurrency` と指数バックオフ    |
| Lambda の実行時間   | 最大 15 分                         | 長時間処理は Step Functions 側で待つ |
| 保護された PDF      | Textract で処理不可                | バリデーション層で弾く               |

## 評価データ

arXiv の `cs.CL` / `cs.LG`、8〜20 ページ、LaTeX ソースが入手できるものを対象とする。

arXiv のデフォルトライセンスは再配布不可のため、PDF や抽出結果を再配布する構成にはできない。
`backend/testdata/pdf/` は git 管理外とし、`tools/fetch-corpus` で取得する。
