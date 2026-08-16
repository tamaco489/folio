# Go のコーディング規約

## パッケージの分け方

- `main.go` には `lambda.Start()` とハンドラの組み立てのみを置き、ロジックは `internal/` に配置する
- `pkg/` は設けない。外部から import される想定がないため
- `internal/` の `config` `domain` `awsx` は共有層。サブシステム固有のものは `internal/pipeline/` にまとめる

## パッケージ内のファイル分割

`internal/awsx/*` (SDK のラッパ) は次の形にする。

| ファイル                      | 置くもの                                                                                                                                             |
| ----------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `doc.go`                      | パッケージコメントと `package` 行だけ                                                                                                                |
| `interface.go`                | 依存側のインタフェース (`API` など) と `var _ API = (*sdk.Client)(nil)`、公開インタフェース (`Converser` など) と `var _ Converser = (*Client)(nil)` |
| `client.go`                   | `Client` 構造体、`Option` と `With*`、`New`、`Bucket()` / `TableName()` のような取得メソッド                                                         |
| `<処理>.go`                   | メソッド群を処理ごとに分ける。1 ファイルに 1 つの関心 (s3: `get.go` `put.go` `head.go`、dynamo: `register.go` `get.go` `update.go` `list.go`)        |
| `errors.go`                   | センチネル `var (Err...)`、専用エラー型 (`JobExistsError` など)、`wrapErr` / `isNotFound` のような判定ヘルパ                                         |
| `const.go`                    | パッケージ定数                                                                                                                                       |
| `recorder.go` / `replayer.go` | 記録・再生のデコレータ。記録の型と読み書きは `recording.go` (textract) / `record.go` (bedrock) に置く                                                |

`internal/pipeline/*` は `Client` を持たないので軽い形にとどめる。

| ファイル     | 置くもの                                                                                                                       |
| ------------ | ------------------------------------------------------------------------------------------------------------------------------ |
| `doc.go`     | パッケージコメント (Lambda のロジックを担うパッケージ。`normalize` `pdf` のような `<pkg>.go` を持つパッケージはその先頭に残す) |
| `handler.go` | `Input` / `Output`、`Handler`、`Option`、`New`、`Handle`                                                                       |
| `errors.go`  | センチネル、専用エラー型 (`InvalidInputError` `RetryableError` など)、`classify` のような判定ヘルパ                            |
| `const.go`   | パッケージ定数 (上限値、接頭辞、失敗コードの文字列など)                                                                        |

`normalize` `verify` のように既に責務でファイルが分かれているパッケージはその形を保ち、センチネルとパッケージ定数の塊だけを `errors.go` / `const.go` へ出す。

- **`fmt.Errorf` のラップとメッセージ文字列は現場に残す。** 箇所ごとに一意なメッセージであれば集約しない
- **型付き enum の定数 (`Role` `Status` `Decision` など) はその型の隣に残す。** `const.go` には移さない
- 定数と組み立て関数が一体のもの (`s3/keys.go`、`dynamo/job.go` の属性名) はそのファイルに残す。使う場所が 1 箇所に閉じる単独の定数もその隣に置いてよい
- `Recorder` / `Replayer` のようにパッケージ内の型がインタフェースを満たすことの表明 (`var _ API = (*Replayer)(nil)`) はその型の隣に置く
- フェイクは `fake_test.go` / `s3test` / `dynamotest` のまま
- **テストファイルは分割しない。** `<pkg>_test.go` のまま置く (テストの並びを変えるとレビューで差分が追えなくなる)

## Lambda の形

- `main.go` は `config.Load` → `cfg.LoadAWS` → ハンドラの組み立て → `lambda.Start(handler.Handle)` の順に書き、起動の失敗は `log.Fatalf` で報告する (手本: `cmd/pipeline/validator/main.go`)
- 入出力の JSON タグは lowerCamelCase にする (`jobId` `pageCount`)
- ハンドラは `(*Output, error)` で返す (AWS SDK v2 の `(*XxxOutput, error)` に揃える)。エラー時は `nil, err`、出力の無い正常終了 (textract-parser の SNS 経路) だけ `nil, nil` を返す。値返し (`Output{}, err`) にしない
- Step Functions の Retry / Catch で判別するエラーは専用のエラー型 (`RetryableError` `InvalidInputError` など) で最外に返す。Lambda はエラーの型名を `errorType` として報告するため、`ErrorEquals` はその型名で照合する
- State 間の入出力は 256KB が上限。ハンドラの出力は S3 キーと小さな判定結果のみにし、実体は S3 に置く

## AWS SDK のラップ

`internal/awsx/` の各パッケージは同じ形にする。

- **SDK のクライアントではなく `API` インタフェースを受け取る。** 使うメソッドだけを列挙し、`New(api API, ...)` で組み立てる。`*dynamodb.Client` などは `API` を満たすのでそのまま渡せる
- **エラーはセンチネルで公開する** (`ErrNotFound` `ErrJobNotFound` `ErrJobInProgress` など)。呼び出し側は `errors.Is` / `errors.AsType[T]` で判定でき、SDK の型に依存しない
- **バケット名・テーブル名は `New` に注入する。** パッケージ内で環境変数を読まない

SDK が同じ意味を複数の型で返す場合は、ラッパ側で 1 つのセンチネルに畳む。
S3 の NotFound は `awss3types.NoSuchKey` / `awss3types.NotFound` / HTTP 404 の 3 系統で来るが、呼び出し側は `errors.Is(err, s3.ErrNotFound)` だけで判定できるようにする。

## import エイリアス

- **AWS SDK (`github.com/aws/aws-sdk-go-v2/...`) は衝突の有無によらず `aws` 接頭辞のエイリアスを付ける** — `awss3` `awsdynamodb` `awstextract` `awsbedrockruntime` `awssfn` `awsconfig` `awshttp`。`service/*/types` も同じ接頭辞で `awss3types` `awstextracttypes` のようにする。`aws-sdk-go-v2/aws` はパッケージ名が `aws` なのでそのまま
- `aws-lambda-go` の `lambda` `events` はエイリアスを付けない (1 ファイルに 1 つで衝突しない)
- **自プロジェクトのパッケージ (`internal/...`) はエイリアスを付けずパッケージ名のまま使う**

```go
import (
    awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

    "github.com/tamaco489/folio/backend/internal/awsx/s3"
)
```

## 書き方

- 必要最低限の実装にする。使わない公開 API、将来用の抽象、使わない `Option` 関数を入れない
- コメントは「なぜ」が自明でないときだけ書く。句点 (。) を含めず、文の途中で改行せず 1 行で書く。長くなるなら GoDoc の箇条書き (`//   - x`) にする
- 構造体フィールドのコメントは行末に置く (`Field T // Field は ...`)
- センチネルエラーの `var (...)` は 1 件ごとに空行で区切る

## modernize の扱い

- `errors.As` ではなく `errors.AsType[T]` を使う (Go 1.26)
- ポインタが要る値は `new(x)` で作る (Go 1.26)。`Ptr` のようなヘルパは置かない
- struct には `omitempty` が効かない。`time.Time` などの struct フィールドには付けない (`omitzero` への置き換えは挙動が変わるため採らない)
- PR を出す前に `just fix-diff` と `just modernize` を通し、提案が 0 件であることを確認する

## 依存の注入

待機・時刻・乱数は外から差し替えられる形にする。
テストが実時間や実行順に依存しなくなる。

- 指数バックオフの待機は `WithSleeper`、ジッタの乱数源は `WithRandN`
- 時刻は `WithClock`

## 外部バイナリの呼び出し

- 実行ファイルのパスをハードコードしない。`WithBinDir()` → 環境変数 → `/opt/bin` → `PATH` の順で解決する
- **S3 の読み書きをこの層に持ち込まない。** ローカルのファイルパスを受け渡し、S3 との橋渡しは `cmd/` 側が担う

## 依存の追加

必要な AWS SDK は `go.mod` に載せてある。**新しい依存を追加しない。** 追加が必要なら理由を PR に書く。
