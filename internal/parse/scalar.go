package parse

import (
	"unicode/utf8"

	"oneyaml.dev/go/internal/scan"
)

// AppendScalar appends the value of the scalar event ev to dst and returns the
// extended slice: escapes are expanded, quotes removed, line folding applied
// and block scalars de-indented and chomped. Reuse dst[:0] between calls to
// avoid allocating. The caller resolves the schema (null, bool, number) from
// the result, ev.Style and the tag.
func (p *Parser) AppendScalar(dst []byte, ev *Event) ([]byte, error) {
	return AppendScalar(dst, p.src, ev)
}

// AppendScalar is Parser.AppendScalar for a bare source slice. An error has
// Syntax category and an offset into src; it can only occur for a double
// quoted escape that is not a Unicode scalar value, since the scanner has
// already checked everything else.
func AppendScalar(dst, src []byte, ev *Event) ([]byte, error) {
	if ev.Kind != Scalar {
		return dst, nil
	}
	b := src[ev.Start:ev.End]
	switch ev.Style {
	case scan.Plain:
		return appendFlow(dst, b, src, ev.Start, scan.Plain)
	case scan.SingleQuoted, scan.DoubleQuoted:
		if len(b) < 2 {
			return dst, nil
		}
		return appendFlow(dst, b[1:len(b)-1], src, ev.Start+1, ev.Style)
	default:
		return appendBlock(dst, b, ev.Style, ev.Indent), nil
	}
}

func isBrk(c byte) bool { return c == '\n' || c == '\r' }

// appendFlow decodes plain and quoted scalar bodies. Line folding [YAML 1.2
// section 6.5]: white space before a line break is dropped, a single break becomes a
// space and n>1 breaks become n-1 newlines; white space after a break is
// dropped. "real" marks how much of dst is content that must not be trimmed.
func appendFlow(dst, b, src []byte, base int, style scan.Style) ([]byte, error) {
	real := len(dst)
	n := len(b)
	for i := 0; i < n; {
		c := b[i]
		switch {
		case isBrk(c):
			dst = dst[:real]
			breaks := 0
			for i < n {
				i = skipBrk(b, i)
				breaks++
				for i < n && isWS(b[i]) {
					i++
				}
				if i < n && isBrk(b[i]) {
					continue
				}
				break
			}
			if breaks == 1 {
				dst = append(dst, ' ')
			} else {
				for k := 1; k < breaks; k++ {
					dst = append(dst, '\n')
				}
			}
			real = len(dst)
		case c == ' ' || c == '\t':
			dst = append(dst, c)
			i++
		case c == '\'' && style == scan.SingleQuoted:
			dst = append(dst, '\'')
			i += 2
			real = len(dst)
		case c == '\\' && style == scan.DoubleQuoted:
			i++
			if i >= n {
				return dst, scan.NewError(src, base+i, Syntax, "dangling escape")
			}
			e := b[i]
			var r rune
			digits := 0
			switch e {
			case '0':
				r = 0
			case 'a':
				r = 7
			case 'b':
				r = 8
			case 't', '\t':
				r = 9
			case 'n':
				r = 10
			case 'v':
				r = 11
			case 'f':
				r = 12
			case 'r':
				r = 13
			case 'e':
				r = 27
			case ' ', '"', '/', '\\':
				r = rune(e)
			case 'N':
				r = 0x85
			case '_':
				r = 0xA0
			case 'L':
				r = 0x2028
			case 'P':
				r = 0x2029
			case 'x':
				digits = 2
			case 'u':
				digits = 4
			case 'U':
				digits = 8
			case '\n', '\r':
				// Escaped line break: joins the lines without a space; blank lines
				// that follow still count as newlines.
				i = skipBrk(b, i)
				for i < n && isWS(b[i]) {
					i++
				}
				for i < n && isBrk(b[i]) {
					dst = append(dst, '\n')
					i = skipBrk(b, i)
					for i < n && isWS(b[i]) {
						i++
					}
				}
				real = len(dst)
				continue
			}
			i++
			if digits > 0 {
				if i+digits > n {
					return dst, scan.NewError(src, base+i, Syntax, "truncated escape")
				}
				var v uint32
				for k := 0; k < digits; k++ {
					h := unhex(b[i+k])
					if h < 0 {
						return dst, scan.NewError(src, base+i+k, Syntax, "invalid hexadecimal escape")
					}
					v = v<<4 | uint32(h)
				}
				i += digits
				if v >= 0xD800 && v <= 0xDBFF {
					// The scanner has checked that a low surrogate escape follows.
					m, ok := scan.LowSurrogateEscape(b[i:])
					if !ok {
						return dst, scan.NewError(src, base+i-digits-2, Syntax, "escape is not a Unicode scalar value")
					}
					var lo uint32
					for _, c := range b[i+2 : i+m] {
						lo = lo<<4 | uint32(unhex(c))
					}
					i += m
					v = 0x10000 + (v-0xD800)<<10 + (lo - 0xDC00)
				} else if v > utf8.MaxRune || v >= 0xDC00 && v <= 0xDFFF {
					return dst, scan.NewError(src, base+i-digits-2, Syntax, "escape is not a Unicode scalar value")
				}
				r = rune(v)
			}
			dst = utf8.AppendRune(dst, r)
			real = len(dst)
		default:
			dst = append(dst, c)
			i++
			real = len(dst)
		}
	}
	return dst, nil
}

func skipBrk(b []byte, i int) int {
	if b[i] == '\r' && i+1 < len(b) && b[i+1] == '\n' {
		return i + 2
	}
	return i + 1
}

// skipBlank consumes lines at b[p:] that are empty after at most indent spaces
// and returns the new offset and the number of lines consumed.
func skipBlank(b []byte, p, indent int) (int, int) {
	count := 0
	for p < len(b) {
		n := 0
		for n < indent && p+n < len(b) && b[p+n] == ' ' {
			n++
		}
		r := p + n
		if r >= len(b) {
			// A last line of spaces without a line break still counts as a blank line.
			if n > 0 {
				count++
			}
			return len(b), count
		}
		if !isBrk(b[r]) {
			return p, count
		}
		p = skipBrk(b, r)
		count++
	}
	return p, count
}

// appendBlock decodes a literal or folded scalar [YAML 1.2 section 8.1]. b is the
// whole token text, header included; indent is the content indentation.
func appendBlock(dst, b []byte, style scan.Style, indent int) []byte {
	i := 1
	chomp := 0 // -1 strip, 0 clip, +1 keep
	for k := 0; k < 2 && i < len(b); k++ {
		switch c := b[i]; {
		case c == '+':
			chomp = 1
			i++
		case c == '-':
			chomp = -1
			i++
		case c >= '1' && c <= '9':
			i++
		}
	}
	for i < len(b) && !isBrk(b[i]) {
		i++
	}
	if i < len(b) {
		i = skipBrk(b, i)
	}
	n := len(b)
	p, tb := skipBlank(b, i, indent)
	lb := false
	leadingBlank := false
	for p < n {
		p += indent
		trailingBlank := p < n && isWS(b[p])
		if style == scan.Folded && lb && !leadingBlank && !trailingBlank {
			if tb == 0 {
				dst = append(dst, ' ')
			}
			lb = false
		} else if lb {
			dst = append(dst, '\n')
			lb = false
		}
		for ; tb > 0; tb-- {
			dst = append(dst, '\n')
		}
		leadingBlank = trailingBlank
		s := p
		for p < n && !isBrk(b[p]) {
			p++
		}
		dst = append(dst, b[s:p]...)
		if p >= n {
			// An unterminated last line that is only spaces is taken as ended.
			lb = p > s && allSpaces(b[s:p])
			break
		}
		p = skipBrk(b, p)
		lb = true
		p, tb = skipBlank(b, p, indent)
	}
	if chomp != -1 {
		if lb {
			dst = append(dst, '\n')
		}
		if chomp == 1 {
			for ; tb > 0; tb-- {
				dst = append(dst, '\n')
			}
		}
	}
	return dst
}

func allSpaces(b []byte) bool {
	for _, c := range b {
		if c != ' ' {
			return false
		}
	}
	return true
}
