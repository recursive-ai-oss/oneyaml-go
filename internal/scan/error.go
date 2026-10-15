package scan

import (
	"strconv"
	"unicode/utf8"
)

// Category is the stable class of an Error. Callers switch on it; the message
// text may change.
type Category uint8

const (
	// Syntax is any violation of the YAML grammar, including characters
	// that YAML does not allow.
	Syntax Category = iota + 1
	// Encoding is input that is not UTF-8 (invalid bytes, or UTF-16/32).
	Encoding
	// Limit is input that exceeds a configured limit such as nesting depth.
	Limit
)

// String returns the category name used in diagnostics and by the test kit.
func (c Category) String() string {
	switch c {
	case Syntax:
		return "syntax"
	case Encoding:
		return "encoding"
	case Limit:
		return "limit"
	}
	return "unknown"
}

// Error is a scanner or parser error with a position.
//
// Offset is a byte offset into the source. Line and Column are 1-based and
// count decoded characters: a byte order mark is not counted and LF, CRLF and
// a lone CR are one line break each. An error at the end of the input has the
// position just after the last character.
type Error struct {
	Category Category
	Msg      string
	Offset   int
	Line     int
	Column   int
}

// Error implements the error interface.
func (e *Error) Error() string {
	return "yaml: " + e.Category.String() + " error at line " + strconv.Itoa(e.Line) +
		", column " + strconv.Itoa(e.Column) + ": " + e.Msg
}

// NewError builds an Error at byte offset off of src. The line and column are
// computed here, once, because errors are rare and the scanner does not track
// line numbers on its hot path.
func NewError(src []byte, off int, cat Category, msg string) *Error {
	if off > len(src) {
		off = len(src)
	}
	if off < 0 {
		off = 0
	}
	line, col := 1, 1
	i := 0
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		i = 3
	}
	for i < off {
		c := src[i]
		switch {
		case c == '\n':
			line++
			col = 1
			i++
		case c == '\r':
			line++
			col = 1
			i++
			if i < len(src) && src[i] == '\n' {
				i++
			}
		case c == 0xEF && col == 1 && i+2 < len(src) && src[i+1] == 0xBB && src[i+2] == 0xBF:
			// A byte order mark at the start of a line is not a character.
			i += 3
		case c < utf8.RuneSelf:
			col++
			i++
		default:
			_, n := utf8.DecodeRune(src[i:])
			col++
			i += n
		}
	}
	return &Error{Category: cat, Msg: msg, Offset: off, Line: line, Column: col}
}
