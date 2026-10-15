package emit

import (
	"encoding/base64"
	"math"
	"strconv"
	"unicode/utf8"

	"oneyaml.dev/go/internal/resolve"
)

// style is how a string is written.
type style uint8

const (
	stPlain style = iota
	stSingle
	stDouble
	stLiteral
	stBinary // not valid UTF-8: written as !!binary
)

// context says where a string is going, which decides the styles open to it.
type context uint8

const (
	ctxValue context = iota // block context: all styles
	ctxKey                  // implicit key: one line only
	ctxFlow                 // inside [] or {}: no block styles, no flow indicators in plain
)

// chooseStyle picks the style for s. It returns stLiteral only for ctxValue, and
// needIndicator reports that a literal needs an indentation indicator because
// its first line starts with a space.
func chooseStyle(s string, c context) (st style, needIndicator bool) {
	n := len(s)
	if n == 0 {
		return stSingle, false
	}
	plain := true
	switch s[0] {
	case ' ', '\t', '#', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`', '{', '}', '[', ']', ',':
		plain = false
	case '-', '?', ':':
		if n == 1 || s[1] == ' ' || s[1] == '\t' || s[1] == '\n' {
			plain = false
		}
	}
	switch s[n-1] {
	case ' ', '\t', ':':
		plain = false
	}
	if n >= 3 && (s[:3] == "---" || s[:3] == "...") {
		plain = false
	}
	// goccy fails on a key that ends in the merge-key text.
	if n >= 2 && s[n-2:] == "<<" {
		plain = false
	}
	var multi, tab, bad bool
	flow := c == ctxFlow
	for i := 0; i < n; {
		b := s[i]
		if b < utf8.RuneSelf {
			switch {
			case b == '\n':
				multi = true
			case b == '\t':
				tab = true
			case b < 0x20 || b == 0x7f:
				bad = true
			case b == ' ':
				if i+1 < n && s[i+1] == '#' {
					plain = false
				}
			case b == ':':
				if flow || (i+1 < n && s[i+1] == ' ') {
					plain = false
				}
			case flow && (b == ',' || b == '[' || b == ']' || b == '{' || b == '}' || b == '?'):
				plain = false
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return stBinary, false
		}
		// Next line, line and paragraph separators and the byte order mark are
		// breaks or invisible for some readers; C1 controls are not printable.
		if r < 0xA0 || r == 0x2028 || r == 0x2029 || r == 0xFEFF || r == 0xFFFE || r == 0xFFFF {
			bad = true
		}
		i += size
	}
	switch {
	case bad:
		return stDouble, false
	case multi:
		if c != ctxValue {
			return stDouble, false
		}
		return literalStyle(s)
	case plain && !tab && resolve.ReadsAsStringS(s):
		return stPlain, false
	}
	return stSingle, false
}

// literalStyle decides whether the multi-line s can be a literal block scalar.
func literalStyle(s string) (style, bool) {
	if s[0] == '\n' {
		return stDouble, false
	}
	start := 0
	first := true
	indicator := false
	for start <= len(s) {
		end := start
		for end < len(s) && s[end] != '\n' {
			end++
		}
		line := s[start:end]
		if len(line) > 0 {
			allWS := true
			for i := 0; i < len(line); i++ {
				if line[i] != ' ' && line[i] != '\t' {
					allWS = false
					break
				}
			}
			last := line[len(line)-1]
			if allWS || line[0] == '\t' || last == ' ' || last == '\t' {
				// Whitespace-only lines, lines that start with a tab and lines with
				// trailing blanks are valid content, but readers disagree about them.
				return stDouble, false
			}
			if first && line[0] == ' ' {
				indicator = true
			}
			first = false
		}
		start = end + 1
	}
	if indicator && len(s) >= 2 && s[len(s)-2:] == "\n\n" {
		// An indentation indicator together with keep chomping trips goccy.
		return stDouble, false
	}
	return stLiteral, indicator
}

func appendSingle(dst []byte, s string) []byte {
	dst = append(dst, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			dst = append(dst, '\'')
		}
		dst = append(dst, s[i])
	}
	return append(dst, '\'')
}

const hexDigits = "0123456789ABCDEF"

func appendDouble(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		b := s[i]
		var esc string
		size := 1
		switch {
		case b == '"':
			esc = `\"`
		case b == '\\':
			esc = `\\`
		case b == '\n':
			esc = `\n`
		case b == '\t':
			esc = `\t`
		case b == '\r':
			esc = `\r`
		case b == 0:
			esc = `\0`
		case b == 7:
			esc = `\a`
		case b == 8:
			esc = `\b`
		case b == 0x0b:
			esc = `\v`
		case b == 0x0c:
			esc = `\f`
		case b == 0x1b:
			esc = `\e`
		case b < 0x20 || b == 0x7f:
			dst = append(dst, s[start:i]...)
			dst = append(dst, '\\', 'x', hexDigits[b>>4], hexDigits[b&15])
			i++
			start = i
			continue
		case b >= utf8.RuneSelf:
			r, sz := utf8.DecodeRuneInString(s[i:])
			size = sz
			switch {
			case r == 0x85:
				esc = `\N`
			case r == 0x2028:
				esc = `\L`
			case r == 0x2029:
				esc = `\P`
			case r < 0xA0 || r == 0xFEFF || r == 0xFFFE || r == 0xFFFF:
				dst = append(dst, s[start:i]...)
				if r <= 0xFF {
					dst = append(dst, '\\', 'x', hexDigits[r>>4], hexDigits[r&15])
				} else {
					dst = append(dst, '\\', 'u', hexDigits[r>>12&15], hexDigits[r>>8&15], hexDigits[r>>4&15], hexDigits[r&15])
				}
				i += size
				start = i
				continue
			default:
				i += size
				continue
			}
		default:
			i++
			continue
		}
		dst = append(dst, s[start:i]...)
		dst = append(dst, esc...)
		i += size
		start = i
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// appendFloat writes f in the shortest form that reads back as the same value.
// The text always has a '.' or an exponent so that it cannot be taken for an
// integer, and the exponent form always has a '.' in the mantissa because YAML
// 1.1 readers require one.
func appendFloat(dst []byte, f float64, bits int) []byte {
	switch {
	case math.IsNaN(f):
		return append(dst, ".nan"...)
	case math.IsInf(f, 1):
		return append(dst, ".inf"...)
	case math.IsInf(f, -1):
		return append(dst, "-.inf"...)
	}
	abs := math.Abs(f)
	format := byte('f')
	if abs != 0 {
		if bits == 32 {
			if a := float32(abs); a < 1e-6 || a >= 1e21 {
				format = 'e'
			}
		} else if abs < 1e-6 || abs >= 1e21 {
			format = 'e'
		}
	}
	start := len(dst)
	dst = strconv.AppendFloat(dst, f, format, -1, bits)
	body := dst[start:]
	e := -1
	dot := false
	for i, c := range body {
		if c == '.' {
			dot = true
		} else if c == 'e' {
			e = i
			break
		}
	}
	if dot {
		return dst
	}
	if e < 0 {
		return append(dst, '.', '0')
	}
	dst = append(dst, 0, 0)
	copy(dst[start+e+2:], dst[start+e:len(dst)-2])
	dst[start+e] = '.'
	dst[start+e+1] = '0'
	return dst
}

func appendBinary(dst []byte, b []byte) []byte {
	if len(b) == 0 {
		return append(dst, `!!binary ""`...)
	}
	dst = append(dst, "!!binary "...)
	n := base64.StdEncoding.EncodedLen(len(b))
	l := len(dst)
	for cap(dst)-l < n {
		dst = append(dst[:cap(dst)], 0)[:l]
	}
	dst = dst[:l+n]
	base64.StdEncoding.Encode(dst[l:], b)
	return dst
}

// maxSimpleKey is the longest implicit key YAML allows, in characters of the
// written key (quotes and escapes included); longer keys need the "? " form.
const maxSimpleKey = 1024

// appendKey appends s as an implicit key. ok is false, and dst is returned as it
// was, when s cannot be one: it is not valid UTF-8 or its written form is longer
// than maxSimpleKey characters.
func appendKey(dst []byte, s string, c context) (out []byte, ok bool) {
	st, _ := chooseStyle(s, c)
	if st == stBinary || len(s) > 6*maxSimpleKey {
		return dst, false
	}
	start := len(dst)
	switch st {
	case stPlain:
		dst = append(dst, s...)
	case stSingle:
		dst = appendSingle(dst, s)
	default:
		dst = appendDouble(dst, s)
	}
	w := dst[start:]
	if len(w) > maxSimpleKey && utf8.RuneCount(w) > maxSimpleKey {
		return dst[:start], false
	}
	return dst, true
}
