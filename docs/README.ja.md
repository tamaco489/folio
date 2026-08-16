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

Lambda が読む環境変数 (`internal/config`)。`FOLIO_ENV` と `AWS_REGION` は全 Lambda が読む。

| 環境変数                       | 読む Lambda                     | 内容                                                |
| ------------------------------ | ------------------------------- | --------------------------------------------------- |
| `FOLIO_ENV`                    | 全部                            | 環境識別子 (`dev` / `stg` / `prd`)                  |
| `FOLIO_DOCUMENTS_BUCKET`       | 全部                            | S3 バケット名                                       |
| `FOLIO_JOBS_TABLE`             | validator, finalizer            | DynamoDB テーブル名                                 |
| `FOLIO_BEDROCK_MODEL_ID`       | textract-parser, bedrock-parser | 構造化に用いるモデル ID                             |
| `FOLIO_TEXTRACT_SNS_TOPIC_ARN` | textract-parser                 | Textract の完了通知トピック                         |
| `FOLIO_TEXTRACT_ROLE_ARN`      | textract-parser                 | Textract が SNS へ発行するために引き受けるロール    |
| `FOLIO_TEXTRACT_FEATURE_TYPES` | textract-parser (任意)          | FeatureTypes (カンマ区切り。既定は `LAYOUT,TABLES`) |
| `FOLIO_CROSSREF_MAILTO`        | finalizer (任意)                | Crossref の polite pool に入るための連絡先          |

### S3 のキー設計

同一バケット内でプレフィックスにより役割を分ける。

```text
uploads/{jobId}/original.pdf          受領した PDF。イベント発火点
work/{jobId}/pages/page-NNNN.png      ラスタライズ結果
work/{jobId}/textract/raw.json        Textract の生出力
work/{jobId}/textract/callback.json   Textract 完了通知への応答に要する情報 (タスクトークンなど)
work/{jobId}/textract/document.json   経路 A の正規化前の構造化結果 (finalizer が読む)
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
├── backend/                Go 単一モジュール (github.com/tamaco489/folio/backend。詳細は backend/README.ja.md)
│   ├── cmd/pipeline/       validator, preprocessor, textract-parser, bedrock-parser, finalizer (main.go は組み立てのみ)
│   ├── internal/
│   │   ├── config/         共有層 — 環境変数の読み込みと検証
│   │   ├── domain/         共有層 — 構造化 JSON のスキーマ
│   │   ├── awsx/           共有層 — s3, dynamo, sfn, textract, bedrock (SDK のラッパ。フェイクは s3test, dynamotest)
│   │   └── pipeline/       Lambda のロジック: validate, preprocess, textractparser, bedrockparser, finalize
│   │                       純ロジック: pdf, extract (textractroute, bedrockroute), normalize, verify (crossref)
│   ├── tools/              fetch-corpus, build-truth, evaluate (デプロイ対象外。未実装)
│   ├── testdata/           textract/ と bedrock/ に記録済みレスポンス (Crossref の記録は internal/pipeline/verify/testdata/)
│   ├── layers/             Lambda Layer のビルド定義 (Dockerfile + build.sh)
│   ├── scripts/            justfile のレシピから呼ぶシェルスクリプト (cmds, build, package, clean)
│   ├── justfile            レシピの宣言のみ。各レシピはスクリプトか単一コマンドを呼ぶ
│   ├── .golangci.yml
│   └── go.mod
└── infra/                  Terraform (詳細は infra/README.ja.md)
    ├── modules/            storage (実装済み)、messaging, compute, pipeline, iam (未実装)
    ├── envs/dev/           dev 環境。provider・backend・変数を持ち、modules/ を呼び出す (stg, prd は Phase 1 の対象外)
    └── justfile
```

`internal/pipeline/` のうち `validate` `preprocess` `textractparser` `bedrockparser` `finalize` は Lambda 1 つずつに対応し、S3 と DynamoDB の読み書きを担う。
`pdf` `extract` `normalize` `verify` は S3 を触らない純ロジックで、前処理・抽出・スキーマ正規化・検証を担う。
API サーバ (`cmd/api/`) は Phase 1 の対象外で、まだ存在しない。

## ツールチェーン

[asdf](https://asdf-vm.com/) で管理し、ルートの `.tool-versions` で固定している。

```text
golang        1.26.5
terraform     1.15.8
golangci-lint 2.12.2
```

`golangci-lint` のバージョンはローカル用の `.tool-versions` と CI の golangci-lint Action の `version:` の 2 か所で固定している。更新時は両方を揃える。

## コマンド

タスクランナーは Make ではなく [just](https://github.com/casey/just) を使う。
justfile はルートに置かず `backend/` と `infra/` の直下に置くため、実行は各ディレクトリに移動してから行う。

```sh
cd backend && just test      # Go のテスト。lint / build / package などは backend/README.ja.md
cd infra && just plan        # Terraform の計画。init / validate / apply などは infra/README.ja.md
```

各レシピの一覧は `just --list`、詳細と前提は [backend/README.ja.md](../backend/README.ja.md) と [infra/README.ja.md](../infra/README.ja.md) を参照。
justfile にはシェルの処理を書かず、単一コマンドで済まないレシピは `scripts/` のスクリプトを呼ぶ。

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
