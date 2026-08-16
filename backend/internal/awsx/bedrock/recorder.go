package bedrock

import (
	"context"
	"time"
)

// Recorder は Converser をラップし、応答をファイルへ記録するモード
//
// 実 API を呼ぶため課金が発生する (記録の取得はユーザーの承認を得てから行う)
type Recorder struct {
	next  Converser
	store *Store
	route Route
	now   func() time.Time
}

var _ Converser = (*Recorder)(nil)

// NewRecorder は記録モードのクライアントを組み立てる
func NewRecorder(next Converser, dir string, route Route) *Recorder {
	return &Recorder{
		next:  next,
		store: NewStore(dir),
		route: route,
		now:   time.Now,
	}
}

// Converse は実クライアントを呼び出し、その応答を記録してから返す
func (r *Recorder) Converse(ctx context.Context, req Request) (*Response, error) {
	if req.RecordKey == "" {
		return nil, ErrRecordKeyRequired
	}
	resp, err := r.next.Converse(ctx, req)
	if err != nil {
		return nil, err
	}
	rec := &Recording{
		ModelID:    req.ModelID,
		Route:      r.route,
		RecordedAt: r.now().UTC(),
		Response:   *resp,
	}
	if err := r.store.Save(req.RecordKey, rec); err != nil {
		return nil, err
	}
	return resp, nil
}
