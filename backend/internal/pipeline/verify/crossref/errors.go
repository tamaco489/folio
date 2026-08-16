package crossref

import "errors"

var (
	// ErrNotFound は DOI が Crossref に登録されていない場合に返る
	//
	// arXiv の DOI (10.48550/arXiv.*) は DataCite 登録のため Crossref では常にこれになる
	ErrNotFound = errors.New("crossref: work not found")

	// ErrRecordingNotFound は再生対象の記録が見つからない場合に返る
	ErrRecordingNotFound = errors.New("crossref: recording not found")
)
