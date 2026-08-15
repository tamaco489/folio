package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ErrRecordingNotFound は再生対象の記録が見つからない場合に返る
var ErrRecordingNotFound = errors.New("bedrock: recording not found")

// ErrRecordKeyRequired は記録・再生時にキーが指定されていない場合に返る
var ErrRecordKeyRequired = errors.New("bedrock: record key is required")

// Route は抽出経路。記録ファイル名の一部となる
type Route string

const (
	// RouteTextract は経路 A (Textract の出力を構造化する)
	RouteTextract Route = "textract"
	// RouteBedrock は経路 B (ページ画像を直接読ませる)
	RouteBedrock Route = "bedrock"
)

// RecordKey は testdata/bedrock/{論文 ID}-{経路}.json のファイル名部分を組み立てる
func RecordKey(paperID string, route Route) string {
	return paperID + "-" + string(route)
}

// Recording は 1 回の Converse 呼び出しの記録
//
// SDK の ConverseOutput は union 型を含み JSON へ往復できないため、
// 呼び出し側が必要とする形へ落とした Response を記録する
type Recording struct {
	ModelID    string    `json:"modelId"`
	Route      Route     `json:"route,omitempty"`
	RecordedAt time.Time `json:"recordedAt"`
	Response   Response  `json:"response"`
	// Note は記録の由来を書き残すための補助欄
	Note string `json:"note,omitempty"`
}

// Store は記録ファイルの読み書きを担う
type Store struct {
	dir string
}

// NewStore は記録の保存先ディレクトリを指定する (規約上は testdata/bedrock)
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) path(key string) string {
	return filepath.Join(s.dir, key+".json")
}

// Save は記録をファイルへ書き出す
func (s *Store) Save(key string, rec *Recording) error {
	if key == "" {
		return ErrRecordKeyRequired
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("bedrock: create recording dir: %w", err)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("bedrock: marshal recording: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(s.path(key), b, 0o644); err != nil {
		return fmt.Errorf("bedrock: write recording: %w", err)
	}
	return nil
}

// Load は記録をファイルから読み込む
func (s *Store) Load(key string) (*Recording, error) {
	if key == "" {
		return nil, ErrRecordKeyRequired
	}
	b, err := os.ReadFile(s.path(key))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrRecordingNotFound, s.path(key))
		}
		return nil, fmt.Errorf("bedrock: read recording: %w", err)
	}
	var rec Recording
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("bedrock: unmarshal recording: %w", err)
	}
	return &rec, nil
}

// Recorder は Converser をラップし、応答をファイルへ記録するモード
//
// 実 API を呼ぶため課金が発生する。記録の取得はユーザーの承認を得てから行う
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
