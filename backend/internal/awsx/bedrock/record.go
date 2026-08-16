package bedrock

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Route は抽出経路 (記録ファイル名の一部となる)
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
// SDK の ConverseOutput は union 型を含み JSON へ往復できないため、呼び出し側が必要とする形へ落とした Response を記録する
type Recording struct {
	ModelID    string    `json:"modelId"`
	Route      Route     `json:"route,omitempty"`
	RecordedAt time.Time `json:"recordedAt"`
	Response   Response  `json:"response"`
	Note       string    `json:"note,omitempty"` // Note は記録の由来を書き残すための補助欄
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
