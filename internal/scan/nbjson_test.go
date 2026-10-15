package scan_test

import (
	"errors"
	"testing"

	"oneyaml.dev/go/internal/scan"
)

// scanAll drains the scanner and returns the first error, or nil.
func scanAll(src string) error {
	s := scan.New([]byte(src), 0)
	for {
		tok, err := s.Peek()
		if err != nil {
			return err
		}
		s.Skip()
		if tok.Kind == scan.StreamEnd {
			return nil
		}
	}
}

// Quoted scalars are built from nb-json, so DEL, the C1 controls and U+FFFE
// are allowed inside them; everywhere else the printable set applies.
func TestNonPrintableOnlyInQuotedScalars(t *testing.T) {
	soft := []string{"\x7f", "\u0080", "\u009f", "\ufffe", "\uffff"}
	for _, c := range soft {
		accept := []string{
			"a: \"x" + c + "y\"\n",
			"a: 'x" + c + "y'\n",
			"[\"" + c + "\", '" + c + "']\n",
			"\"x\n " + c + "y\"\n",
		}
		for _, in := range accept {
			if err := scanAll(in); err != nil {
				t.Errorf("%q: %v", in, err)
			}
		}
		reject := []string{
			"a: x" + c + "y\n",
			"a: b # c" + c + "\n",
			"a: |\n x" + c + "\n",
			"a: \"ok\" # " + c + "\n",
			"a: \"ok\"\nb: " + c + "\n",
			"'" + c + "'\n" + c + "\n",
			"&a" + c + " b\n",
		}
		for _, in := range reject {
			if err := scanAll(in); err == nil {
				t.Errorf("%q: accepted", in)
			}
		}
	}
}

// A hard error after an allowed character inside a quoted scalar is still found.
func TestBadByteAfterQuotedNonPrintable(t *testing.T) {
	for _, in := range []string{"\"\x7f\" \x00\n", "[\"\ufffe\", \x01]\n", "\"\x7f\" \xff\n"} {
		if err := scanAll(in); err == nil {
			t.Errorf("%q: accepted", in)
		}
	}
	// A control character inside a quoted scalar stays an error.
	if err := scanAll("\"a\x00b\"\n"); err == nil {
		t.Error("NUL inside quotes accepted")
	}
}

// Invalid UTF-8 behind an allowed character inside a quoted scalar is still
// an encoding error, found at the bad byte.
func TestInvalidUTF8BehindQuotedNonPrintable(t *testing.T) {
	soft := []string{"\x7f", "\u0080", "\u009f", "\ufffe", "\uffff"}
	bad := []string{"\x8e", "\xc2", "\xef", "\xef\xbf", "\xff"}
	for _, c := range soft {
		for _, b := range bad {
			for _, in := range []string{
				"a: '" + c + b + "'\n",
				"a: \"" + c + "x" + b + "\"\n",
				"a: ['" + c + "', \"" + c + c + b + "\"]\n",
				"'" + c + "\n " + c + b + "'\n",
			} {
				err := scanAll(in)
				var se *scan.Error
				if !errors.As(err, &se) || se.Category != scan.Encoding {
					t.Errorf("%q: got %v, want an encoding error", in, err)
				}
			}
		}
	}
	// The error points at the bad byte, not at the allowed character before it.
	err := scanAll("a: '\x7f\x8e'\n")
	var se *scan.Error
	if errors.As(err, &se) && se.Offset != 5 {
		t.Errorf("offset %d, want 5", se.Offset)
	}
}
