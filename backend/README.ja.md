# backend

English: [README.md](README.md)

論文 PDF を構造化 JSON に変換するパイプラインの Go コード。全体像は [docs/README.ja.md](../docs/README.ja.md)。

## 構成

| ディレクトリ    | 中身                                                         |
| --------------- | ------------------------------------------------------------ |
| `cmd/pipeline/` | Lambda 5 本 (デプロイされるもの)                             |
| `internal/`     | Lambda とツールが共有するロジック                            |
| `tools/`        | 評価のためのローカルツール (デプロイしない)                  |
| `layers/`       | Lambda Layer (poppler)                                       |
| `testdata/`     | 記録済みの AWS レスポンスと評価用 PDF (`pdf/` は git 管理外) |

## Lambda

| Lambda            | 役割                                                                                     |
| ----------------- | ---------------------------------------------------------------------------------------- |
| `validator`       | 入口。PDF として読めるか、処理済みの jobId (= SHA-256) でないかを判定する                |
| `preprocessor`    | ページ画像とテキストレイヤーを `work/` に出す                                            |
| `textract-parser` | 経路 A。Textract で読み取り、Bedrock で書誌情報・章立て・参考文献に整理する (英語のみ)   |
| `bedrock-parser`  | 経路 B。ページ画像 1 枚を Bedrock に見せて構造化する (ページごとに並列)                  |
| `finalizer`       | 出口。両経路の結果を正規化し、Crossref で参考文献を照合し、`outputs/` と DynamoDB に残す |

## tools

抽出精度を数値で測るための 3 つ。論文を題材にしたのは LaTeX ソースから正解を機械的に作れるため。

```text
fetch-corpus ──> PDF ──> (パイプライン) ──> 抽出結果 ──┐
                  │                                      ├──> evaluate ──> 一致率
                  └──> LaTeX ソース ──> build-truth ──> 正解 ──┘
```

| ツール         | 状態            | 何をするか                                                                                                                                                 |
| -------------- | --------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `fetch-corpus` | 実装済み        | arXiv から条件 (cs.CL / cs.LG、8〜20 ページ) に合う論文を取得し、ページ数・参考文献数・LaTeX ソースの有無・ライセンス・SHA-256 を `corpus.json` に記録する |
| `build-truth`  | Phase 2、未実装 | LaTeX ソースから題名・著者・節見出し・参考文献を取り出し、正解 JSON にする                                                                                 |
| `evaluate`     | Phase 2、未実装 | 抽出結果と正解 JSON を突き合わせ、項目ごとの一致率を出す。経路 A / B の比較にも使う                                                                        |

## コマンド

```sh
cd backend
just build           # Lambda 5 本をビルドする
just upload          # zip を S3 に置き、Lambda のコードを差し替える
just submit <pdf>    # PDF を投入してパイプラインを起動する (Textract / Bedrock の課金あり)
just fetch-corpus    # 評価用論文を arXiv から取得する
```

`fetch-corpus` の既定はカテゴリ (`cs.CL` または `cs.LG`) だけで絞る。テーマで絞るときは arXiv の検索構文を `-query` に、論文を指名するときは `-ids` に渡す。

```sh
just fetch-corpus -query 'cat:cs.CL AND ti:"large language model"'   # 題名に LLM を含む cs.CL
just fetch-corpus -query 'cat:cs.LG AND abs:benchmark'                 # 要旨に benchmark を含む cs.LG
just fetch-corpus -ids 2608.20318,2301.07041                           # ID を指名 (検索しない)
```

ほかのレシピは `just --list`。
