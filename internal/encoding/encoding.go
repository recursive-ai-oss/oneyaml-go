// Package encoding detects and converts the character encodings a YAML stream
// may use: UTF-8, UTF-16 and UTF-32, each in both byte orders.
//
// The scanner reads UTF-8 only. Readers call Detect and, when the input is not
// UTF-8, ToUTF8 once before parsing; UTF-8 input is passed through without a
// copy. Writers that must keep a document's original encoding call FromUTF8.
package encoding

import (
	"unicode/utf8"

	"oneyaml.dev/go/internal/scan"
)

// Encoding is a character encoding of a YAML stream.
type Encoding uint8

// The encodings of YAML 1.2 section 5.2.
const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
	UTF32LE
	UTF32BE
)

// String returns the usual name of e.
func (e Encoding) String() string {
	switch e {
	case UTF16LE:
		return "UTF-16LE"
	case UTF16BE:
		return "UTF-16BE"
	case UTF32LE:
		return "UTF-32LE"
	case UTF32BE:
		return "UTF-32BE"
	}
	return "UTF-8"
}

// Detect reports the encoding of src and the length of its byte order mark,
// 0 when there is none. It follows the table of section 5.2: a mark decides;
// without one, the null-byte pattern of a first ASCII character decides.
// Anything else is UTF-8. A UTF-8 mark is reported with length 3 but is left in
// the data, which the scanner skips itself.
func Detect(src []byte) (Encoding, int) {
	if len(src) == 0 {
		return UTF8, 0
	}
	b0 := src[0]
	// Fast exit for the overwhelmingly common case: ordinary UTF-8 text starts
	// with a byte that is neither NUL nor the first byte of a UTF-16 mark.
	if b0 != 0x00 && b0 != 0xFF && b0 != 0xFE && b0 != 0xEF {
		if len(src) < 2 || src[1] != 0 {
			return UTF8, 0
		}
	}
	switch {
	case len(src) >= 4 && b0 == 0x00 && src[1] == 0x00 && src[2] == 0xFE && src[3] == 0xFF:
		return UTF32BE, 4
	case len(src) >= 4 && b0 == 0xFF && src[1] == 0xFE && src[2] == 0x00 && src[3] == 0x00:
		return UTF32LE, 4
	case len(src) >= 2 && b0 == 0xFE && src[1] == 0xFF:
		return UTF16BE, 2
	case len(src) >= 2 && b0 == 0xFF && src[1] == 0xFE:
		return UTF16LE, 2
	case len(src) >= 3 && b0 == 0xEF && src[1] == 0xBB && src[2] == 0xBF:
		return UTF8, 3
	}
	// No mark: the first character is ASCII, so its NUL padding shows the form.
	switch {
	case len(src) >= 4 && b0 == 0 && src[1] == 0 && src[2] == 0 && src[3] != 0:
		return UTF32BE, 0
	case len(src) >= 4 && b0 != 0 && src[1] == 0 && src[2] == 0 && src[3] == 0:
		return UTF32LE, 0
	case len(src) >= 2 && b0 == 0 && src[1] != 0:
		return UTF16BE, 0
	case len(src) >= 2 && b0 != 0 && src[1] == 0:
		return UTF16LE, 0
	}
	return UTF8, 0
}

// Convert is Detect followed by ToUTF8: the input as UTF-8, and its encoding.
// UTF-8 input is returned as is, without copying.
func Convert(src []byte) ([]byte, Encoding, error) {
	enc, bom := Detect(src)
	if enc == UTF8 {
		return src, UTF8, nil
	}
	out, err := ToUTF8(src[bom:], enc)
	return out, enc, err
}

// ToUTF8 converts src, which has no byte order mark, from enc to UTF-8. UTF-8
// input is returned as is. A malformed unit (an unpaired surrogate, a code
// point above U+10FFFF, or a truncated final unit) gives a *scan.Error of
// category Encoding. Its position is that of the offending character in the
// decoded text, and its offset indexes the UTF-8 output produced so far.
func ToUTF8(src []byte, enc Encoding) ([]byte, error) {
	switch enc {
	case UTF8:
		return src, nil
	case UTF16LE, UTF16BE:
		return from16(src, enc == UTF16BE)
	default:
		return from32(src, enc == UTF32BE)
	}
}

func fail(out []byte, msg string) ([]byte, error) {
	return nil, scan.NewError(out, len(out), scan.Encoding, msg)
}

func from16(src []byte, be bool) ([]byte, error) {
	out := make([]byte, 0, len(src)/2+len(src)/8+8)
	unit := func(i int) rune {
		if be {
			return rune(src[i])<<8 | rune(src[i+1])
		}
		return rune(src[i+1])<<8 | rune(src[i])
	}
	i := 0
	for i+1 < len(src) {
		u := unit(i)
		i += 2
		switch {
		case u < 0xD800 || u > 0xDFFF:
		case u >= 0xDC00:
			return fail(out, "UTF-16 low surrogate without a high surrogate")
		default:
			if i+1 >= len(src) {
				return fail(out, "UTF-16 high surrogate at the end of the input")
			}
			lo := unit(i)
			if lo < 0xDC00 || lo > 0xDFFF {
				return fail(out, "UTF-16 high surrogate not followed by a low surrogate")
			}
			i += 2
			u = 0x10000 + (u-0xD800)<<10 + (lo - 0xDC00)
		}
		out = utf8.AppendRune(out, u)
	}
	if i < len(src) {
		return fail(out, "UTF-16 input ends in the middle of a unit")
	}
	return out, nil
}

func from32(src []byte, be bool) ([]byte, error) {
	out := make([]byte, 0, len(src)/4+len(src)/8+8)
	i := 0
	for i+3 < len(src) {
		var u uint32
		if be {
			u = uint32(src[i])<<24 | uint32(src[i+1])<<16 | uint32(src[i+2])<<8 | uint32(src[i+3])
		} else {
			u = uint32(src[i+3])<<24 | uint32(src[i+2])<<16 | uint32(src[i+1])<<8 | uint32(src[i])
		}
		i += 4
		if u > utf8.MaxRune {
			return fail(out, "UTF-32 code point above U+10FFFF")
		}
		if u >= 0xD800 && u <= 0xDFFF {
			return fail(out, "UTF-32 surrogate code point")
		}
		out = utf8.AppendRune(out, rune(u))
	}
	if i < len(src) {
		return fail(out, "UTF-32 input ends in the middle of a unit")
	}
	return out, nil
}

// FromUTF8 appends utf to dst, converted to enc and preceded by a byte order
// mark when withBOM is set. utf must be valid UTF-8; an invalid byte is written
// as U+FFFD.
func FromUTF8(dst, utf []byte, enc Encoding, withBOM bool) []byte {
	if enc == UTF8 {
		if withBOM {
			dst = append(dst, 0xEF, 0xBB, 0xBF)
		}
		return append(dst, utf...)
	}
	put := func(dst []byte, r rune) []byte {
		switch enc {
		case UTF16LE, UTF16BE:
			var units [2]uint16
			n := 1
			if r >= 0x10000 {
				r -= 0x10000
				units[0], units[1] = uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF))
				n = 2
			} else {
				units[0] = uint16(r)
			}
			for _, u := range units[:n] {
				if enc == UTF16BE {
					dst = append(dst, byte(u>>8), byte(u))
				} else {
					dst = append(dst, byte(u), byte(u>>8))
				}
			}
		case UTF32BE:
			dst = append(dst, byte(r>>24), byte(r>>16), byte(r>>8), byte(r))
		default:
			dst = append(dst, byte(r), byte(r>>8), byte(r>>16), byte(r>>24))
		}
		return dst
	}
	if withBOM {
		dst = put(dst, 0xFEFF)
	}
	for i := 0; i < len(utf); {
		r, n := utf8.DecodeRune(utf[i:])
		dst = put(dst, r)
		i += n
	}
	return dst
}
