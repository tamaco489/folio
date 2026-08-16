package crossref

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 記録モードが応答をファイルに残し、再生モードで同じ URL から読み戻せることを確かめる
func TestRecorderRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ft := &fakeTransport{responses: []*http.Response{
		status(http.StatusOK, `{"message":{"DOI":"10.1000/rec","title":["Recorded"]}}`, "X-Rate-Limit-Limit", "10"),
		status(http.StatusNotFound, "Resource not found."),
	}}
	recorder := NewRecorder(ft, dir)
	recorder.now = func() time.Time { return time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC) }
	live, _ := newTestClient(recorder)
	ctx := context.Background()

	got, err := live.LookupDOI(ctx, "10.1000/rec")
	if err != nil {
		t.Fatalf("LookupDOI: %v", err)
	}
	if _, err := live.LookupDOI(ctx, "10.1000/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 の err = %v, want ErrNotFound", err)
	}

	// JSON の本文は body に、そうでない本文は text に入り、URL と取得日時が残ること
	b, err := os.ReadFile(filepath.Join(dir, "works_10.1000_2Frec.json"))
	if err != nil {
		t.Fatalf("記録ファイルが無い: %v", err)
	}
	for _, want := range []string{`"url": "https://api.crossref.org/works/10.1000%2Frec"`, `"recordedAt": "2026-08-16T00:00:00Z"`, `"status": 200`, `"body": {`, `"X-Rate-Limit-Limit"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("記録に %s が無い:\n%s", want, b)
		}
	}
	b, err = os.ReadFile(filepath.Join(dir, "works_10.1000_2Fmissing.json"))
	if err != nil {
		t.Fatalf("記録ファイルが無い: %v", err)
	}
	if !strings.Contains(string(b), `"text": "Resource not found."`) {
		t.Errorf("JSON でない本文が text に入っていない:\n%s", b)
	}

	replayer, err := NewReplayer(dir)
	if err != nil {
		t.Fatalf("NewReplayer: %v", err)
	}
	replay, _ := newTestClient(replayer)
	replayed, err := replay.LookupDOI(ctx, "10.1000/rec")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replayed != got {
		t.Errorf("replayed = %+v, want %+v", replayed, got)
	}
	if _, err := replay.LookupDOI(ctx, "10.1000/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("再生した 404 の err = %v, want ErrNotFound", err)
	}
}

// 記録の無い URL は転送層の失敗になり、Client はリトライの末に諦めること (テストの取り違えを黙って通さない)
func TestReplayerUnknownURL(t *testing.T) {
	c, _ := newTestClient(newReplayer(t))
	_, err := c.LookupDOI(context.Background(), "10.1000/not-recorded")
	if !errors.Is(err, ErrRecordingNotFound) {
		t.Fatalf("err = %v, want ErrRecordingNotFound", err)
	}
}

func TestNewReplayerRejectsBrokenRecording(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"url":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewReplayer(dir); err == nil {
		t.Error("url の無い記録が受け入れられた")
	}
}

func TestRecordName(t *testing.T) {
	tests := map[string]struct {
		in   string
		want string
	}{
		"正常系_DOI のパスの場合_英数字と ._- 以外が _ になること": {in: "https://api.crossref.org/works/10.1038%2Fnature14539", want: "works_10.1038_2Fnature14539.json"},
		"正常系_検索クエリの場合_連続する記号が 1 つの _ に畳まれること": {in: "https://api.crossref.org/works?query.bibliographic=Deep+learning&rows=3", want: "works_query.bibliographic_Deep_learning_rows_3.json"},
		"境界値_長い URL の場合_上限で切られること":            {in: "https://api.crossref.org/works?query.bibliographic=" + strings.Repeat("a", 200), want: "works_query.bibliographic_" + strings.Repeat("a", maxRecordNameLen-len("works_query.bibliographic_")) + ".json"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := recordName(tt.in); got != tt.want {
				t.Errorf("recordName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
