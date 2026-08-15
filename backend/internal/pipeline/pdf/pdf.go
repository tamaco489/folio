// Package pdf は poppler (pdfinfo, pdftoppm, pdftotext) の呼び出しをこの層に閉じ込める
//
// S3 の読み書きは行わず、ローカルファイルパスのみを受け渡す
// Lambda では Layer が /opt/bin にバイナリを配置し、ローカルでは PATH 上の poppler を使う
package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBinDir は Lambda Layer がバイナリを展開する位置
	DefaultBinDir = "/opt/bin"

	// EnvBinDir はバイナリの配置先を上書きする環境変数
	EnvBinDir = "FOLIO_POPPLER_BIN_DIR"

	// EnvDPI はラスタライズ解像度を上書きする環境変数
	EnvDPI = "FOLIO_RASTERIZE_DPI"

	// DefaultDPI は経路 B に渡すページ画像の解像度
	//
	// 精度とコストの初期値として 150 DPI を採る (#34 の検証で見直す)
	//   - A4 (210x297mm) を 150 DPI で描画すると 1240x1754 px となり、Claude の高解像度入力の上限 (長辺 2576 px) に収まるため縮小されない
	//   - 200 DPI にすると画素数が約 1.8 倍になり画像トークンも比例して増える
	DefaultDPI = 150

	// DefaultTimeout は外部プロセス 1 回あたりの上限
	//
	// Lambda の実行時間上限 15 分を超えないよう余裕を持たせる
	DefaultTimeout = 10 * time.Minute

	// DefaultMinCharsPerPage はテキストレイヤーを持つと判定する 1 ページあたりの文字数
	//
	// スキャン PDF にもページ番号やスタンプ由来の数文字が乗ることがあるため、ゼロ判定ではなく閾値を置く
	// 通常の本文ページは 1000 文字を超えるので 50 文字は十分に低い
	DefaultMinCharsPerPage = 50

	binPDFInfo   = "pdfinfo"
	binPDFToPPM  = "pdftoppm"
	binPDFToText = "pdftotext"
)

var (
	// ErrBinaryNotFound は poppler の実行ファイルを解決できないことを示す
	ErrBinaryNotFound = errors.New("pdf: poppler binary not found")

	// ErrEncrypted は PDF が暗号化・保護されていることを示す
	ErrEncrypted = errors.New("pdf: document is encrypted")

	// ErrDamaged は PDF として読めないことを示す
	ErrDamaged = errors.New("pdf: document is damaged or not a pdf")

	// ErrNoPagesRendered はラスタライズ結果が 1 枚も得られないことを示す
	ErrNoPagesRendered = errors.New("pdf: no pages rendered")
)

// Runner は poppler の実行設定を保持する
type Runner struct {
	binDir          string
	dpi             int
	timeout         time.Duration
	minCharsPerPage int
}

// Option は Runner の設定を変更する
type Option func(*Runner)

// WithBinDir は poppler の配置ディレクトリを指定する
//
// 空文字を渡すと PATH から解決する
func WithBinDir(dir string) Option {
	return func(r *Runner) { r.binDir = dir }
}

// WithDPI はラスタライズ解像度を指定する
func WithDPI(dpi int) Option {
	return func(r *Runner) {
		if dpi > 0 {
			r.dpi = dpi
		}
	}
}

// WithTimeout は外部プロセス 1 回あたりの上限を指定する
func WithTimeout(d time.Duration) Option {
	return func(r *Runner) {
		if d > 0 {
			r.timeout = d
		}
	}
}

// WithMinCharsPerPage はテキストレイヤー判定の閾値を指定する
func WithMinCharsPerPage(n int) Option {
	return func(r *Runner) {
		if n >= 0 {
			r.minCharsPerPage = n
		}
	}
}

// NewRunner は環境変数と Option から Runner を組み立てる
func NewRunner(opts ...Option) *Runner {
	r := &Runner{
		binDir:          resolveBinDir(os.Getenv(EnvBinDir)),
		dpi:             resolveDPI(os.Getenv(EnvDPI)),
		timeout:         DefaultTimeout,
		minCharsPerPage: DefaultMinCharsPerPage,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// DPI は実際に使用する解像度を返す
func (r *Runner) DPI() int { return r.dpi }

// BinDir は解決済みのバイナリ配置ディレクトリを返す
//
// 空文字は PATH から解決することを意味する
func (r *Runner) BinDir() string { return r.binDir }

// resolveBinDir は環境変数、Layer の既定パス、PATH の順に配置先を決める
func resolveBinDir(env string) string {
	if env != "" {
		return env
	}
	if isExecutable(filepath.Join(DefaultBinDir, binPDFToPPM)) {
		return DefaultBinDir
	}
	return ""
}

// resolveDPI は環境変数の解像度を検証する
func resolveDPI(env string) int {
	if env == "" {
		return DefaultDPI
	}
	dpi, err := strconv.Atoi(env)
	if err != nil || dpi <= 0 {
		return DefaultDPI
	}
	return dpi
}

func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}

// binary は実行ファイルの絶対パスを解決する
func (r *Runner) binary(name string) (string, error) {
	if r.binDir == "" {
		path, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("%w: %s: %w", ErrBinaryNotFound, name, err)
		}
		return path, nil
	}

	path := filepath.Join(r.binDir, name)
	if !isExecutable(path) {
		return "", fmt.Errorf("%w: %s", ErrBinaryNotFound, path)
	}
	return path, nil
}

// run は poppler のコマンドを実行し標準出力を返す
func (r *Runner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	path, err := r.binary(name)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("pdf: %s: %w", name, ctxErr)
		}
		if classified := classify(stderr.String()); classified != nil {
			return nil, classified
		}
		return nil, fmt.Errorf("pdf: %s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}

	return stdout.Bytes(), nil
}

// classify は poppler の標準エラー出力を既知のエラーに対応付ける
func classify(stderr string) error {
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "incorrect password"),
		strings.Contains(lower, "encrypted"):
		return fmt.Errorf("%w: %s", ErrEncrypted, strings.TrimSpace(stderr))
	case strings.Contains(lower, "may not be a pdf"),
		strings.Contains(lower, "couldn't read"),
		strings.Contains(lower, "damaged"):
		return fmt.Errorf("%w: %s", ErrDamaged, strings.TrimSpace(stderr))
	}
	return nil
}
