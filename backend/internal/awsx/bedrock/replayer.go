package bedrock

import "context"

// Replayer は記録済みレスポンスを返す再生モード
//
// 実 API を一切呼ばないため、テストと開発時の反復に用いる
type Replayer struct {
	store *Store
}

var _ Converser = (*Replayer)(nil)

// NewReplayer は再生モードのクライアントを組み立てる
func NewReplayer(dir string) *Replayer {
	return &Replayer{store: NewStore(dir)}
}

// Converse は RecordKey に対応する記録を返す
func (r *Replayer) Converse(_ context.Context, req Request) (*Response, error) {
	if req.RecordKey == "" {
		return nil, ErrRecordKeyRequired
	}
	rec, err := r.store.Load(req.RecordKey)
	if err != nil {
		return nil, err
	}
	resp := rec.Response
	resp.Attempts = 1
	return &resp, nil
}
