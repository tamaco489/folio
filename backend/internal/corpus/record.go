package corpus

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Paper は取得した論文 1 本の記録 (corpus.json の要素)
//
// PDF 本体は再配布できないため、共有するのはこの記録だけにする
type Paper struct {
	ArxivID           string    `json:"arxivId"`           // ArxivID は版を除いた ID
	Version           string    `json:"version"`           // Version は取得した版
	Title             string    `json:"title"`             //
	PrimaryCategory   string    `json:"primaryCategory"`   //
	Categories        []string  `json:"categories"`        //
	Published         time.Time `json:"published"`         //
	Pages             int       `json:"pages"`             // Pages は pdfinfo のページ数
	References        int       `json:"references"`        // References は本文末尾の [n] 形式の件数 (推定)
	HasSource         bool      `json:"hasSource"`         // HasSource は LaTeX ソースを e-print から入手できるか
	SourceContentType string    `json:"sourceContentType"` // SourceContentType は判定に用いた e-print の Content-Type
	License           string    `json:"license"`           // License はライセンスの URL (既定ライセンスは空のことがある)
	LicenseKind       string    `json:"licenseKind"`       // LicenseKind は cc / arxiv / unknown
	File              string    `json:"file"`              // File は取得先ディレクトリからの相対パス
	SHA256            string    `json:"sha256"`            // SHA256 は PDF のハッシュ (パイプラインの jobId と同じ)
	FetchedAt         time.Time `json:"fetchedAt"`         //
}

// Corpus は corpus.json 全体
type Corpus struct {
	Papers []Paper `json:"papers"`
}

// LoadCorpus は取得先ディレクトリの記録を読む (無ければ空)
func LoadCorpus(dir string) (*Corpus, error) {
	b, err := os.ReadFile(filepath.Join(dir, RecordFile))
	if errors.Is(err, os.ErrNotExist) {
		return &Corpus{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("corpus: read %s: %w", RecordFile, err)
	}
	var c Corpus
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("corpus: parse %s: %w", RecordFile, err)
	}
	return &c, nil
}

// Save は記録を書き戻す
func (c *Corpus) Save(dir string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("corpus: encode %s: %w", RecordFile, err)
	}
	return writeFile(filepath.Join(dir, RecordFile), append(b, '\n'))
}

// Has は同じ ID (版は問わない) が記録済みかを返す
func (c *Corpus) Has(arxivID string) bool {
	for _, p := range c.Papers {
		if p.ArxivID == arxivID {
			return true
		}
	}
	return false
}

// Add は記録を末尾に足す (同じ ID があれば置き換える)
func (c *Corpus) Add(p Paper) {
	for i := range c.Papers {
		if c.Papers[i].ArxivID == p.ArxivID {
			c.Papers[i] = p
			return
		}
	}
	c.Papers = append(c.Papers, p)
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("corpus: create dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("corpus: write %s: %w", path, err)
	}
	return nil
}
