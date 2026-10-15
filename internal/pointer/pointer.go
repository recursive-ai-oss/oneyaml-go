// Package pointer parses RFC 6901 JSON Pointers.
package pointer

import (
	"errors"
	"strings"
)

// ErrSyntax means the pointer is not a valid RFC 6901 pointer.
var ErrSyntax = errors.New("invalid JSON pointer")

// Parse splits ptr into its reference tokens, with "~1" and "~0" decoded. The
// empty pointer addresses the whole document and has no tokens. Any other
// pointer must start with "/", and "~" must be followed by 0 or 1.
func Parse(ptr string) ([]string, error) {
	if ptr == "" {
		return nil, nil
	}
	if ptr[0] != '/' {
		return nil, ErrSyntax
	}
	n := strings.Count(ptr, "/")
	toks := make([]string, 0, n)
	rest := ptr[1:]
	for {
		tok, tail, more := strings.Cut(rest, "/")
		if strings.IndexByte(tok, '~') >= 0 {
			var b strings.Builder
			for i := 0; i < len(tok); i++ {
				if tok[i] != '~' {
					b.WriteByte(tok[i])
					continue
				}
				if i+1 >= len(tok) {
					return nil, ErrSyntax
				}
				i++
				switch tok[i] {
				case '0':
					b.WriteByte('~')
				case '1':
					b.WriteByte('/')
				default:
					return nil, ErrSyntax
				}
			}
			tok = b.String()
		}
		toks = append(toks, tok)
		if !more {
			return toks, nil
		}
		rest = tail
	}
}

// Escape encodes one reference token for use in a pointer.
func Escape(tok string) string {
	if !strings.ContainsAny(tok, "~/") {
		return tok
	}
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(tok)
}

// Join builds a pointer from tokens.
func Join(toks []string) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteByte('/')
		b.WriteString(Escape(t))
	}
	return b.String()
}

// Index parses tok as an array index per RFC 6901 section 4: digits only, no
// leading zero unless the token is "0". ok is false for anything else,
// including "-", which the caller handles itself.
func Index(tok string) (n int, ok bool) {
	if tok == "" || len(tok) > 18 || (len(tok) > 1 && tok[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] < '0' || tok[i] > '9' {
			return 0, false
		}
		n = n*10 + int(tok[i]-'0')
	}
	return n, true
}
