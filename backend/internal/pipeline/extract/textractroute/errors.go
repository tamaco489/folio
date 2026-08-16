package textractroute

import "errors"

// ErrEmptyAnalysis は解釈できる Block が 1 件も無いことを示す
var ErrEmptyAnalysis = errors.New("textractroute: analysis has no blocks")
