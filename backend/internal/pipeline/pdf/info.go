package pdf

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Info は pdfinfo から取得した文書の属性
type Info struct {
	Pages      int    // Pages はページ数
	Encrypted  bool   // Encrypted は暗号化・保護されているか
	Encryption string // Encryption は pdfinfo が報告した暗号化の詳細 (未暗号化なら空)
}

// Info は PDF のページ数と保護状態を取得する
//
// ページ数と保護状態を 1 回の呼び出しで得られるため pdfinfo を使う
// PDF ヘッダの自前解析は圧縮オブジェクトや線形化に弱く、判定を誤るとバリデーション層 (#16) が誤った理由で処理を打ち切るため採らない
func (r *Runner) Info(ctx context.Context, pdfPath string) (Info, error) {
	// 空パスワードで開けない PDF は pdfinfo が非ゼロ終了するため、classify が ErrEncrypted に変換する
	out, err := r.run(ctx, binPDFInfo, "-enc", "UTF-8", pdfPath)
	if err != nil {
		return Info{}, err
	}

	info, err := parseInfo(out)
	if err != nil {
		return Info{}, err
	}
	return info, nil
}

// parseInfo は pdfinfo の "キー: 値" 形式の出力を解釈する
func parseInfo(out []byte) (Info, error) {
	var info Info
	seenPages := false

	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)

		switch strings.TrimSpace(key) {
		case "Pages":
			pages, err := strconv.Atoi(value)
			if err != nil {
				return Info{}, fmt.Errorf("pdf: parse pages %q: %w", value, err)
			}
			info.Pages = pages
			seenPages = true

		case "Encrypted":
			if !strings.HasPrefix(strings.ToLower(value), "no") {
				info.Encrypted = true
				info.Encryption = value
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Info{}, fmt.Errorf("pdf: scan pdfinfo output: %w", err)
	}

	if !seenPages {
		return Info{}, fmt.Errorf("%w: pdfinfo reported no page count", ErrDamaged)
	}
	return info, nil
}
