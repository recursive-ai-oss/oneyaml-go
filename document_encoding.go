package oneyaml

import (
	"unicode/utf8"

	"oneyaml.dev/go/internal/encoding"
)

// UTF-16 and UTF-32 documents. The scanner reads UTF-8 only, so a Document built
// from UTF-16 or UTF-32 text keeps the UTF-8 form for parsing and editing and
// converts back when it is written: an unedited document returns the original
// bytes, an edited one is written again in the original encoding, with a byte
// order mark if the input had one.
var (
	// decodeDocInput converts data to UTF-8. When data is already UTF-8 it returns
	// a nil enc. Otherwise enc describes the original encoding (including whether it
	// had a byte order mark) and is passed back to encodeDocOutput.
	decodeDocInput = func(data []byte) (utf8 []byte, enc any, err error) {
		e, bom := encoding.Detect(data)
		if e == encoding.UTF8 {
			return nil, nil, nil
		}
		out, err := encoding.ToUTF8(data[bom:], e)
		if err != nil {
			return nil, nil, convertError(err, out, &config{}, 0, 0)
		}
		return out, docEncoding{e, bom > 0}, nil
	}
	// encodeDocOutput converts edited UTF-8 text back to the original encoding.
	encodeDocOutput = func(utf8 []byte, enc any) []byte {
		de := enc.(docEncoding)
		out := encoding.FromUTF8(nil, utf8, de.enc, de.bom)
		if e, _ := encoding.Detect(out); !de.bom && e != de.enc {
			// Without a byte order mark the encoding is recognised from a first
			// character in ASCII. An edit can put something else first (a root
			// replaced by Japanese text); write the mark then, or no reader could
			// tell what the text is.
			out = encoding.FromUTF8(nil, utf8, de.enc, true)
		}
		return out
	}
)

type docEncoding struct {
	enc encoding.Encoding
	bom bool
}

// docEncoding returns how the Document writes its text when that is not UTF-8.
func (s *docStream) docEncoding() (docEncoding, bool) {
	if s == nil || s.enc == nil {
		return docEncoding{}, false
	}
	de, ok := s.enc.(docEncoding)
	return de, ok
}

// factor is the most bytes that one byte of UTF-8 text takes when written.
func (e docEncoding) factor() int {
	if e.enc == encoding.UTF16LE || e.enc == encoding.UTF16BE {
		return 2
	}
	return 4
}

// markLen is the size of the byte order mark that is written.
func (e docEncoding) markLen() int {
	if !e.bom {
		return 0
	}
	if e.enc == encoding.UTF16LE || e.enc == encoding.UTF16BE {
		return 2
	}
	return 4
}

// minLen is the fewest bytes that n bytes of UTF-8 text take when written: three
// bytes of Japanese text take two in UTF-16, and the rest take at least as many.
func (e docEncoding) minLen(n int) int {
	if e.factor() == 2 {
		return n * 2 / 3
	}
	return n
}

// writtenLen is the size of the UTF-8 text p when written, without a mark. An
// invalid byte counts as the replacement character, as it is written.
func (e docEncoding) writtenLen(p []byte) int {
	n := 0
	for i := 0; i < len(p); {
		r, w := rune(p[i]), 1
		if r >= utf8.RuneSelf {
			r, w = utf8.DecodeRune(p[i:])
		}
		i += w
		if e.factor() == 4 || r >= 0x10000 {
			n += 4
		} else {
			n += 2
		}
	}
	return n
}
