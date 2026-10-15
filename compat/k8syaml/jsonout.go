package yaml

import (
	"math"
	"strconv"
	"unicode/utf8"
)

const hexDigits = "0123456789abcdef"

// safeJSON[b] is true for bytes that encoding/json writes as they are inside a
// string: printable ASCII except '"', '\\', '<', '>' and '&'.
var safeJSON = func() (t [utf8.RuneSelf]bool) {
	for c := 0x20; c < utf8.RuneSelf; c++ {
		t[c] = true
	}
	for _, c := range `"\<>&` {
		t[c] = false
	}
	return
}()

// appendJSONString appends s as a JSON string literal, escaping exactly what
// encoding/json does with its default settings: the HTML characters, control
// characters, U+2028 and U+2029, and invalid UTF-8 (written as U+FFFD).
func appendJSONString(dst, s []byte) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if safeJSON[b] {
				i++
				continue
			}
			dst = append(dst, s[start:i]...)
			switch b {
			case '\\', '"':
				dst = append(dst, '\\', b)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[b>>4], hexDigits[b&0xF])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRune(s[i:])
		if r == utf8.RuneError && size == 1 {
			dst = append(dst, s[start:i]...)
			dst = append(dst, `\ufffd`...)
			i += size
			start = i
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			dst = append(dst, s[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', hexDigits[r&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// appendJSONFloat appends f the way encoding/json writes a float64. NaN and the
// infinities have no JSON form and are reported by the caller.
func appendJSONFloat(dst []byte, f float64) []byte {
	abs := math.Abs(f)
	format := byte('f')
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}
	dst = strconv.AppendFloat(dst, f, format, -1, 64)
	if format == 'e' {
		// Clean up e-09 to e-9, as encoding/json does.
		n := len(dst)
		if n >= 4 && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
			dst[n-2] = dst[n-1]
			dst = dst[:n-1]
		}
	}
	return dst
}
