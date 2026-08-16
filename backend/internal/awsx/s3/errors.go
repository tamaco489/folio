package s3

import (
	"errors"
	"fmt"
	"net/http"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awss3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// ErrNotFound はオブジェクトが存在しないことを表す
//
// SDK は GetObject と HeadObject で異なるエラー型を返すため、ここで一本化する
var ErrNotFound = errors.New("s3: object not found")

func wrapErr(op, key string, err error) error {
	if isNotFound(err) {
		return fmt.Errorf("s3: %s %q: %w", op, key, ErrNotFound)
	}
	return fmt.Errorf("s3: %s %q: %w", op, key, err)
}

// isNotFound は SDK が返す複数の「存在しない」表現を吸収する
//
// 同じ意味が 3 系統で来る
//   - GetObject は NoSuchKey を返す
//   - HeadObject はボディを持たないため NotFound を返す
//   - SDK が型に落とせなかった場合は 404 のまま伝わってくる
func isNotFound(err error) bool {
	if _, ok := errors.AsType[*awss3types.NoSuchKey](err); ok {
		return true
	}

	if _, ok := errors.AsType[*awss3types.NotFound](err); ok {
		return true
	}

	if respErr, ok := errors.AsType[*awshttp.ResponseError](err); ok && respErr.HTTPStatusCode() == http.StatusNotFound {
		return true
	}

	return false
}
