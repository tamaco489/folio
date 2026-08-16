# Go のコーディング規約

## パッケージの分け方

- `main.go` には `lambda.Start()` とハンドラの組み立てのみを置き、ロジックは `internal/` に配置する
- `pkg/` は設けない。外部から import される想定がないため
- `internal/` の `config` `domain` `awsx` は共有層。サブシステム固有のものは `internal/pipeline/` にまとめる

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

`backend/tools/tools.go` は `//go:build tools` を付けた依存保持専用のファイル。
`aws-lambda-go` はどこからも参照されない間 `go mod tidy` で削除されるため、これで保持している。
最初の `main.go` が入って参照先ができたら、このファイルは削除する。
