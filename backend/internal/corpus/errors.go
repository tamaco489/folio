package corpus

import "errors"

var (
	// ErrNoCandidates は検索や ID 指定で候補が 1 件も得られなかったことを示す
	ErrNoCandidates = errors.New("corpus: no candidate papers")

	// ErrUnexpectedStatus は arXiv が 2xx 以外を返したことを示す
	ErrUnexpectedStatus = errors.New("corpus: unexpected http status")

	// ErrInvalidID は arXiv ID の形式 (YYMM.NNNNN か YYMM.NNNNNvN) でないことを示す
	ErrInvalidID = errors.New("corpus: invalid arxiv id")
)
