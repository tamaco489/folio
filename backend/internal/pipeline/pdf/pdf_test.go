package pdf

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// requirePoppler は poppler が使えない環境でテストをスキップする
//
// CI に poppler が入っていない可能性があるため、存在確認で分岐する
func requirePoppler(t *testing.T) *Runner {
	t.Helper()

	for _, name := range []string{binPDFInfo, binPDFToPPM, binPDFToText} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("poppler が見つからないためスキップする: %s", name)
		}
	}
	return NewRunner(WithBinDir(""), WithTimeout(time.Minute))
}

func TestResolveBinDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if got := resolveBinDir(dir); got != dir {
		t.Errorf("resolveBinDir(%q) = %q, want %q", dir, got, dir)
	}

	// Layer の既定パスが存在するかは実行環境に依存するため、
	// 既定パスか PATH 委譲 (空文字) のどちらかであることだけを確かめる
	got := resolveBinDir("")
	if got != "" && got != DefaultBinDir {
		t.Errorf("resolveBinDir(\"\") = %q, want %q or empty", got, DefaultBinDir)
	}
}

func TestResolveDPI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  string
		want int
	}{
		{name: "未設定なら既定値", env: "", want: DefaultDPI},
		{name: "数値を採用する", env: "200", want: 200},
		{name: "数値でなければ既定値", env: "high", want: DefaultDPI},
		{name: "ゼロ以下なら既定値", env: "0", want: DefaultDPI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveDPI(tt.env); got != tt.want {
				t.Errorf("resolveDPI(%q) = %d, want %d", tt.env, got, tt.want)
			}
		})
	}
}

func TestNewRunnerOptions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	r := NewRunner(WithBinDir(dir), WithDPI(300), WithTimeout(3*time.Second), WithMinCharsPerPage(10))

	if r.BinDir() != dir {
		t.Errorf("BinDir() = %q, want %q", r.BinDir(), dir)
	}
	if r.DPI() != 300 {
		t.Errorf("DPI() = %d, want 300", r.DPI())
	}
	if r.timeout != 3*time.Second {
		t.Errorf("timeout = %v, want 3s", r.timeout)
	}
	if r.minCharsPerPage != 10 {
		t.Errorf("minCharsPerPage = %d, want 10", r.minCharsPerPage)
	}
}

func TestBinaryNotFound(t *testing.T) {
	t.Parallel()

	r := NewRunner(WithBinDir(t.TempDir()))
	if _, err := r.binary(binPDFInfo); !errors.Is(err, ErrBinaryNotFound) {
		t.Errorf("binary() error = %v, want ErrBinaryNotFound", err)
	}
}

func TestBinaryFromBinDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, binPDFInfo)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	r := NewRunner(WithBinDir(dir))
	got, err := r.binary(binPDFInfo)
	if err != nil {
		t.Fatalf("binary() error = %v", err)
	}
	if got != path {
		t.Errorf("binary() = %q, want %q", got, path)
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stderr string
		want   error
	}{
		{name: "パスワード保護", stderr: "Command Line Error: Incorrect password", want: ErrEncrypted},
		{name: "壊れたファイル", stderr: "Syntax Error: Couldn't read xref table", want: ErrDamaged},
		{name: "PDF ではない", stderr: "Syntax Warning: May not be a PDF file", want: ErrDamaged},
		{name: "未知のエラー", stderr: "something else", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := classify(tt.stderr)
			if tt.want == nil {
				if got != nil {
					t.Errorf("classify(%q) = %v, want nil", tt.stderr, got)
				}
				return
			}
			if !errors.Is(got, tt.want) {
				t.Errorf("classify(%q) = %v, want %v", tt.stderr, got, tt.want)
			}
		})
	}
}

func TestInfo(t *testing.T) {
	r := requirePoppler(t)

	path := writeTestPDF(t, filepath.Join(t.TempDir(), "sample.pdf"), []string{
		textPageContent([]string{"page one"}),
		textPageContent([]string{"page two"}),
		blankPageContent(),
	})

	info, err := r.Info(context.Background(), path)
	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Pages != 3 {
		t.Errorf("Pages = %d, want 3", info.Pages)
	}
	if info.Encrypted {
		t.Errorf("Encrypted = true, want false")
	}
}

func TestInfoDamaged(t *testing.T) {
	r := requirePoppler(t)

	path := filepath.Join(t.TempDir(), "broken.pdf")
	if err := os.WriteFile(path, []byte("not a pdf at all"), 0o644); err != nil {
		t.Fatalf("write broken pdf: %v", err)
	}

	if _, err := r.Info(context.Background(), path); err == nil {
		t.Fatal("Info() error = nil, want error")
	}
}

func TestParseInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		out           string
		wantPages     int
		wantEncrypted bool
		wantErr       bool
	}{
		{
			name:      "暗号化なし",
			out:       "Title:          sample\nPages:          12\nEncrypted:      no\n",
			wantPages: 12,
		},
		{
			name:          "暗号化あり",
			out:           "Pages:          4\nEncrypted:      yes (print:yes copy:no)\n",
			wantPages:     4,
			wantEncrypted: true,
		},
		{
			name:    "ページ数がない",
			out:     "Title:          sample\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseInfo([]byte(tt.out))
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseInfo() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseInfo() error = %v", err)
			}
			if got.Pages != tt.wantPages {
				t.Errorf("Pages = %d, want %d", got.Pages, tt.wantPages)
			}
			if got.Encrypted != tt.wantEncrypted {
				t.Errorf("Encrypted = %v, want %v", got.Encrypted, tt.wantEncrypted)
			}
		})
	}
}
