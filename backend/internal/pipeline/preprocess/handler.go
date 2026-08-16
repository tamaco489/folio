package preprocess

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"

	"github.com/tamaco489/folio/backend/internal/awsx/s3"
	"github.com/tamaco489/folio/backend/internal/domain"
	"github.com/tamaco489/folio/backend/internal/pipeline/pdf"
)

// Input は前処理 State の入力
//
// バリデーション State が jobId を確定させており、S3 キーは jobId から規則で導出できるためキーは受け取らない
type Input struct {
	JobID string `json:"jobId"`
}

// Output は前処理 State の出力
//
// ページ画像はキーを列挙せず、プレフィックスとページ数から後段が導出する (200 ページ分のキーを並べると出力が肥大するため)
type Output struct {
	JobID            string          `json:"jobId"`
	PageCount        int             `json:"pageCount"`
	HasTextLayer     bool            `json:"hasTextLayer"`
	Language         domain.Language `json:"language"`
	LanguageDetected bool            `json:"languageDetected"` // LanguageDetected は Language に判定根拠があるか (false は既定値であることを表す)
	SkipTextract     bool            `json:"skipTextract"`     // SkipTextract は経路 A を通さないか (Textract が日本語に非対応であるため)
	PageImagePrefix  string          `json:"pageImagePrefix"`
	TextLayerKey     string          `json:"textLayerKey"`
}

// Handler は前処理 State の依存をまとめる
type Handler struct {
	storage    *s3.Client
	runner     *pdf.Runner
	baseDir    string
	chunkPages int
}

// Option は Handler の設定を変更する
type Option func(*Handler)

// WithBaseDir は作業ディレクトリを作る親ディレクトリを指定する
//
// 空文字なら os.TempDir (Lambda では /tmp) の下に作る
func WithBaseDir(dir string) Option {
	return func(h *Handler) { h.baseDir = dir }
}

// WithChunkPages は 1 回のラスタライズで扱うページ数を指定する
func WithChunkPages(n int) Option {
	return func(h *Handler) {
		if n > 0 {
			h.chunkPages = n
		}
	}
}

// New は前処理のハンドラを組み立てる
func New(storage *s3.Client, runner *pdf.Runner, opts ...Option) *Handler {
	h := &Handler{
		storage:    storage,
		runner:     runner,
		chunkPages: DefaultChunkPages,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Handle は PDF をラスタライズしてテキストレイヤーを抽出し、結果の所在と判定を返す
//
// ページ数の上限はバリデーション State が弾いているため、ここでは再判定しない
func (h *Handler) Handle(ctx context.Context, in Input) (Output, error) {
	if in.JobID == "" {
		return Output{}, ErrEmptyJobID
	}

	workDir, err := os.MkdirTemp(h.baseDir, workDirPattern)
	if err != nil {
		return Output{}, fmt.Errorf("preprocess: create work dir: %w", err)
	}
	// 同じ実行環境が次のジョブに再利用されるため、/tmp に成果物を残さない
	// 後片付けの失敗は処理結果を左右しないが、/tmp が埋まる予兆になるため記録だけする
	defer func() {
		if rerr := os.RemoveAll(workDir); rerr != nil {
			slog.WarnContext(ctx, "failed to remove work dir", "dir", workDir, "error", rerr)
		}
	}()

	pdfPath := filepath.Join(workDir, fileOriginalPDF)
	if err := h.download(ctx, s3.OriginalPDFKey(in.JobID), pdfPath); err != nil {
		return Output{}, err
	}

	// ページ数はチャンク分割の範囲を決めるために先に確定させる
	info, err := h.runner.Info(ctx, pdfPath)
	if err != nil {
		return Output{}, err
	}

	text, err := h.runner.ExtractText(ctx, pdfPath, filepath.Join(workDir, fileTextLayer))
	if err != nil {
		return Output{}, err
	}

	// 言語判定は全文の文字種で行うため、先頭だけを見ずに読み切る (英文要旨で始まる日本語論文を取り違えないため)
	body, err := os.ReadFile(text.Path)
	if err != nil {
		return Output{}, fmt.Errorf("preprocess: read extracted text: %w", err)
	}
	if err := h.storage.PutBytes(ctx, s3.TextLayerKey(in.JobID), body, s3.WithContentType(contentTypeText)); err != nil {
		return Output{}, err
	}

	if err := h.rasterize(ctx, in.JobID, pdfPath, filepath.Join(workDir, dirPages), info.Pages); err != nil {
		return Output{}, err
	}

	language, detected := DetectLanguage(string(body), text.HasTextLayer)
	return Output{
		JobID:            in.JobID,
		PageCount:        info.Pages,
		HasTextLayer:     text.HasTextLayer,
		Language:         language,
		LanguageDetected: detected,
		SkipTextract:     language == domain.LanguageJapanese,
		PageImagePrefix:  PageImagePrefix(in.JobID),
		TextLayerKey:     s3.TextLayerKey(in.JobID),
	}, nil
}

// PageImagePrefix はページ画像のキーが共有するプレフィックスを返す
//
// キーの組み立て規則を awsx/s3 に閉じ込めたままにするため、自前で連結せず 1 ページ目のキーから導く
func PageImagePrefix(jobID string) string {
	return path.Dir(s3.PageImageKey(jobID, 1)) + "/"
}

// download は S3 のオブジェクトをローカルファイルへ流す
func (h *Handler) download(ctx context.Context, key, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("preprocess: create %s: %w", dst, err)
	}

	if _, err := h.storage.Download(ctx, key, f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("preprocess: close %s: %w", dst, err)
	}
	return nil
}

// rasterize はページを chunkPages 単位で描画し、アップロードのたびにローカルの画像を消す
//
// 全ページを描き切ってからまとめて送ると /tmp の使用量がページ数に比例するため、チャンクごとに送って消す
func (h *Handler) rasterize(ctx context.Context, jobID, pdfPath, outDir string, pages int) error {
	for first := 1; first <= pages; first += h.chunkPages {
		last := min(first+h.chunkPages-1, pages)

		result, err := h.runner.Rasterize(ctx, pdfPath, outDir, first, last)
		if err != nil {
			return err
		}
		if want := last - first + 1; len(result.Pages) != want {
			return fmt.Errorf("%w: pages %d-%d: rendered %d, want %d", ErrPageCountMismatch, first, last, len(result.Pages), want)
		}

		for page := first; page <= last; page++ {
			if err := h.uploadPage(ctx, jobID, page, filepath.Join(result.Dir, pdf.PageFileName(page))); err != nil {
				return err
			}
		}
	}
	return nil
}

// uploadPage は 1 ページを S3 へ送り、ローカルの画像を消す
//
// 消し残すと次のチャンクの Rasterize が前のチャンクのファイルまで拾い、ページ数の検査が通らなくなる
func (h *Handler) uploadPage(ctx context.Context, jobID string, page int, imagePath string) error {
	f, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("preprocess: open page image: %w", err)
	}
	defer f.Close()

	// ファイルは seek できるが、SDK に長さを判定させず明示する
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("preprocess: stat page image: %w", err)
	}

	if err := h.storage.Put(ctx, s3.PageImageKey(jobID, page), f,
		s3.WithContentType(contentTypePNG), s3.WithContentLength(fi.Size())); err != nil {
		return err
	}

	if err := os.Remove(imagePath); err != nil {
		return fmt.Errorf("preprocess: remove page image: %w", err)
	}
	return nil
}
