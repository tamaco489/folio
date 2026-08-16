package crossref

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxRecordNameLen は記録ファイル名の上限 (検索の URL は長くなりうる)
const maxRecordNameLen = 100

// Recording は 1 回の HTTP 応答の記録
//
// Client を通した経路全体 (URL の組み立て、404 の判別、リトライ) を実 API なしで検証するため、Work に落とす前の応答をそのまま残す
// 本文は JSON なら Body に、そうでなければ (404 の "Resource not found." など) Text に入れる
type Recording struct {
	URL        string          `json:"url"`
	RecordedAt time.Time       `json:"recordedAt"`
	Status     int             `json:"status"`
	Header     http.Header     `json:"header,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
	Text       string          `json:"text,omitempty"`
	Note       string          `json:"note,omitempty"` // Note は記録の由来を書き残すための補助欄 (合成した記録はその旨を書く)
}

// recordName は URL からファイル名を組み立てる (パスとクエリを英数字と ._- 以外を _ に置き換えて繋ぐ)
//
// 再生時の照合は Recording.URL で行うため、名前は読みやすさのためだけにある
func recordName(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	var sb strings.Builder
	lastUnderscore := false
	for _, r := range s {
		ok := r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-')
		if ok {
			sb.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			sb.WriteByte('_')
			lastUnderscore = true
		}
	}
	name := strings.Trim(sb.String(), "_")
	if len(name) > maxRecordNameLen {
		name = name[:maxRecordNameLen]
	}
	return name + ".json"
}

// Recorder は実 API の応答を通しつつファイルへ記録する http.RoundTripper
//
// 記録の取得は polite pool 準拠 (WithMailto) で少数回に留める
type Recorder struct {
	next http.RoundTripper
	dir  string
	now  func() time.Time
}

var _ http.RoundTripper = (*Recorder)(nil)

// NewRecorder は記録モードの転送層を組み立てる
func NewRecorder(next http.RoundTripper, dir string) *Recorder {
	return &Recorder{next: next, dir: dir, now: time.Now}
}

// RoundTrip は実際に送信し、応答をファイルへ書いてから返す
func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	rec := Recording{
		URL:        req.URL.String(),
		RecordedAt: r.now().UTC(),
		Status:     resp.StatusCode,
		Header:     resp.Header,
	}
	if json.Valid(body) {
		rec.Body = body
	} else {
		rec.Text = string(body)
	}
	if err := writeRecording(filepath.Join(r.dir, recordName(rec.URL)), rec); err != nil {
		return nil, err
	}
	return resp, nil
}

func writeRecording(path string, rec Recording) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("crossref: create recording dir: %w", err)
	}
	// URL の & を & に逃がさず、記録をそのまま読める形にする
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rec); err != nil {
		return fmt.Errorf("crossref: marshal recording: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("crossref: write recording: %w", err)
	}
	return nil
}

// Replayer は記録済みの応答を URL で引いて返す http.RoundTripper
//
// 記録が無い URL は転送層の失敗として返し、Client はそれを unavailable 相当のエラーに畳む
type Replayer struct {
	byURL map[string]Recording
}

var _ http.RoundTripper = (*Replayer)(nil)

// NewReplayer はディレクトリ直下の *.json をすべて読み込む
func NewReplayer(dir string) (*Replayer, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("crossref: list recordings: %w", err)
	}
	r := &Replayer{byURL: make(map[string]Recording, len(paths))}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("crossref: read recording: %w", err)
		}
		var rec Recording
		if err := json.Unmarshal(b, &rec); err != nil {
			return nil, fmt.Errorf("crossref: parse recording %s: %w", p, err)
		}
		if rec.URL == "" || rec.Status == 0 {
			return nil, fmt.Errorf("crossref: recording %s lacks url or status", p)
		}
		r.byURL[rec.URL] = rec
	}
	return r, nil
}

// RoundTrip は URL に一致する記録を応答として返す
func (r *Replayer) RoundTrip(req *http.Request) (*http.Response, error) {
	rec, ok := r.byURL[req.URL.String()]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRecordingNotFound, req.URL.String())
	}
	body := []byte(rec.Text)
	if len(rec.Body) > 0 {
		body = rec.Body
	}
	header := rec.Header.Clone()
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode:    rec.Status,
		Status:        fmt.Sprintf("%d %s", rec.Status, http.StatusText(rec.Status)),
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}
