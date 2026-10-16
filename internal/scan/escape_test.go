package scan_test

import (
	"errors"
	"testing"

	"oneyaml.dev/go/internal/scan"
)

// A double-quoted escape that names no character is an error of the scanner, at
// the backslash, so that no reader of the tokens has to decode a scalar to find
// out. The code points on either side of the surrogate range are fine.
func TestEscapeValuesAreChecked(t *testing.T) {
	bad := map[string]int{
		`"\ud800"`:     1,
		`"\udbff"`:     1,
		`"\udc00"`:     1,
		`"\udfff"`:     1,
		`"\U0000d800"`: 1,
		`"\U00110000"`: 1,
		`"\Uffffffff"`: 1,
		`"ab\ud800"`:   3,
	}
	for in, off := range bad {
		err := scanAll(in)
		var se *scan.Error
		if !errors.As(err, &se) || se.Category != scan.Syntax || se.Offset != off || se.Msg != "escape is not a Unicode scalar value" {
			t.Errorf("%s: got %v, want an error at offset %d", in, err, off)
		}
	}
	for _, in := range []string{`"\ud7ff"`, `"\ue000"`, `"\U0010ffff"`, `"\U00010000"`, `"\u0000\x00\xff"`, `"\uD7FF\uE000"`} {
		if err := scanAll(in); err != nil {
			t.Errorf("%s: %v", in, err)
		}
	}
}
