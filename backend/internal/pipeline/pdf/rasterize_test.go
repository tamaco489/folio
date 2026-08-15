package pdf

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPageFileName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		page int
		want string
	}{
		{page: 1, want: "page-0001.png"},
		{page: 42, want: "page-0042.png"},
		{page: 999, want: "page-0999.png"},
		{page: 1000, want: "page-1000.png"},
		{page: 3000, want: "page-3000.png"},
	}

	for _, tt := range tests {
		if got := PageFileName(tt.page); got != tt.want {
			t.Errorf("PageFileName(%d) = %q, want %q", tt.page, got, tt.want)
		}
	}
}

func TestNormalizePageFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// pdftoppm はページ数に応じた桁数で出力するため 2 桁の名前を模す
	for _, name := range []string{"page-01.png", "page-02.png", "page-10.png", "note.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	pages, err := normalizePageFiles(dir)
	if err != nil {
		t.Fatalf("normalizePageFiles() error = %v", err)
	}

	want := []string{
		filepath.Join(dir, "page-0001.png"),
		filepath.Join(dir, "page-0002.png"),
		filepath.Join(dir, "page-0010.png"),
	}
	if len(pages) != len(want) {
		t.Fatalf("len(pages) = %d, want %d", len(pages), len(want))
	}
	for i, path := range want {
		if pages[i] != path {
			t.Errorf("pages[%d] = %q, want %q", i, pages[i], path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("stat %q: %v", path, err)
		}
	}
}

func TestRasterize(t *testing.T) {
	r := requirePoppler(t)

	tmp := t.TempDir()
	path := writeTestPDF(t, filepath.Join(tmp, "sample.pdf"), []string{
		textPageContent([]string{"first page"}),
		textPageContent([]string{"second page"}),
	})
	outDir := filepath.Join(tmp, "pages")

	got, err := r.Rasterize(context.Background(), path, outDir)
	if err != nil {
		t.Fatalf("Rasterize() error = %v", err)
	}

	if got.DPI != DefaultDPI {
		t.Errorf("DPI = %d, want %d", got.DPI, DefaultDPI)
	}
	if len(got.Pages) != 2 {
		t.Fatalf("len(Pages) = %d, want 2", len(got.Pages))
	}
	for i, page := range got.Pages {
		want := filepath.Join(outDir, PageFileName(i+1))
		if page != want {
			t.Errorf("Pages[%d] = %q, want %q", i, page, want)
		}
		fi, err := os.Stat(page)
		if err != nil {
			t.Fatalf("stat %q: %v", page, err)
		}
		if fi.Size() == 0 {
			t.Errorf("%q is empty", page)
		}
	}
}

func TestRasterizePagesRange(t *testing.T) {
	r := requirePoppler(t)

	tmp := t.TempDir()
	path := writeTestPDF(t, filepath.Join(tmp, "sample.pdf"), []string{
		textPageContent([]string{"one"}),
		textPageContent([]string{"two"}),
		textPageContent([]string{"three"}),
	})
	outDir := filepath.Join(tmp, "pages")

	got, err := r.RasterizePages(context.Background(), path, outDir, 2, 3)
	if err != nil {
		t.Fatalf("RasterizePages() error = %v", err)
	}

	want := []string{
		filepath.Join(outDir, PageFileName(2)),
		filepath.Join(outDir, PageFileName(3)),
	}
	if len(got.Pages) != len(want) {
		t.Fatalf("len(Pages) = %d, want %d", len(got.Pages), len(want))
	}
	for i, page := range want {
		if got.Pages[i] != page {
			t.Errorf("Pages[%d] = %q, want %q", i, got.Pages[i], page)
		}
	}
}

func TestRasterizePagesInvalidRange(t *testing.T) {
	t.Parallel()

	r := NewRunner()
	if _, err := r.RasterizePages(context.Background(), "sample.pdf", t.TempDir(), 3, 2); err == nil {
		t.Fatal("RasterizePages() error = nil, want error")
	}
}
