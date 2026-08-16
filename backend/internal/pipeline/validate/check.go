package validate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

// readHead はマジックバイトの判定に使う先頭バイトを読む
func readHead(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("validate: open %q: %w", path, err)
	}
	defer f.Close()

	buf := make([]byte, headerWindow)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("validate: read %q: %w", path, err)
	}
	return buf[:n], nil
}

// looksLikePDF は先頭バイトに PDF の署名が含まれるかを返す
//
// 位置を offset 0 に限定しないのは、署名の前に余分なバイトを持つ PDF を poppler が受け付けるためで、限定すると後段の pdfinfo と判定が食い違う
func looksLikePDF(head []byte) bool {
	return bytes.Contains(head, pdfMagic)
}

// checkSize はサイズ上限を超える場合に理由を返す
func checkSize(size int64) *Reason {
	if size <= MaxBytes {
		return nil
	}
	return &Reason{
		Code:    CodeTooLarge,
		Message: fmt.Sprintf("size %d bytes exceeds the limit of %d bytes", size, MaxBytes),
	}
}

// checkPages はページ数が扱える範囲にない場合に理由を返す
func checkPages(pages int) *Reason {
	if pages < 1 {
		return &Reason{Code: CodeDamaged, Message: "document has no pages"}
	}
	if pages <= MaxPages {
		return nil
	}
	return &Reason{
		Code:    CodeTooManyPages,
		Message: fmt.Sprintf("page count %d exceeds the limit of %d", pages, MaxPages),
	}
}
