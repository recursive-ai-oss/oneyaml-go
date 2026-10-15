package resolve

import (
	"math"
	"strconv"
	"time"
)

// Timestamp is the kind V3 gives to a plain scalar that looks like a date. Core
// never returns it. The value is read with ParseTimestampV3.
const Timestamp Kind = Float + 1

// V3 classifies a plain scalar the way yaml.v3 does. That is neither Core nor
// YAML 1.1:
//   - true, false and null have the three Core spellings and "~", and nothing
//     else; "yes", "on" and "y" stay strings (a bool-typed target accepts them,
//     which the caller handles);
//   - integers take Go's base-0 syntax, so 0x1F, 0b101, 0o17 and legacy 0777 are
//     octal, and "_" separators are dropped first;
//   - text that is not an integer but has the shape of a decimal float is a float;
//   - a date or date-time is a Timestamp.
//
// Results that need more than 64 bits never occur: such text is read as a float.
// The empty string is null.
func V3(b []byte) Result {
	n := len(b)
	if n == 0 {
		return Result{Kind: Null}
	}
	switch c := b[0]; {
	case c >= '0' && c <= '9':
		if n >= 5 && b[4] == '-' && looksLikeDate(b) {
			if _, ok := ParseTimestampV3(string(b)); ok {
				return Result{Kind: Timestamp}
			}
		}
		return v3Number(b)
	case c == '+' || c == '-':
		if n > 1 && b[1] == '.' {
			if f, ok := coreSpecialFloat(b); ok && !(b[0] == '+' && f != f) {
				return Result{Kind: Float, F: f}
			}
		}
		return v3Number(b)
	case c == '.':
		if f, ok := coreSpecialFloat(b); ok {
			return Result{Kind: Float, F: f}
		}
		return v3Number(b)
	case c == '~':
		if n == 1 {
			return Result{Kind: Null}
		}
	case c == 'n' || c == 'N':
		if isNullWord(b) {
			return Result{Kind: Null}
		}
	case c == 't' || c == 'T' || c == 'f' || c == 'F':
		if v, ok := coreBool(b); ok {
			return Result{Kind: Bool, B: v}
		}
	}
	return Result{}
}

func looksLikeDate(b []byte) bool {
	for i := 0; i < 4; i++ {
		if !isDigit(b[i]) {
			return false
		}
	}
	return true
}

// v3Number reads b, which starts with a digit, a sign or a dot, as an integer
// or a float, or leaves it a string.
func v3Number(b []byte) Result {
	if b[0] == '.' && !underscoresBetweenDigits(b) {
		// A text that starts with a dot is read as a float as written (yaml.v3 hands
		// it to strconv.ParseFloat), which takes an underscore only between digits.
		// Other numbers have their underscores dropped before they are read.
		return Result{}
	}
	p := b
	var stack [64]byte
	for _, c := range b {
		if c == '_' {
			q := stack[:0]
			for _, c := range b {
				if c != '_' {
					q = append(q, c)
				}
			}
			p = q
			break
		}
	}
	if len(p) == 0 {
		return Result{}
	}
	i := 0
	neg, signed := false, false
	if p[0] == '+' || p[0] == '-' {
		neg, signed = p[0] == '-', true
		i = 1
	}
	if mag, st := baseZero(p[i:]); st == magOK {
		switch {
		case !signed && mag > math.MaxInt64:
			return Result{Kind: Int, W: FitsU64, U: mag}
		case !neg && mag <= math.MaxInt64:
			return Result{Kind: Int, I: int64(mag)}
		case neg && mag <= 1<<63:
			return Result{Kind: Int, I: -int64(mag)}
		}
	}
	if isDecimalFloat(p) {
		if f, err := strconv.ParseFloat(string(p), 64); err == nil {
			return Result{Kind: Float, F: f}
		}
	}
	return Result{}
}

// underscoresBetweenDigits reports whether every underscore in b has a digit on
// each side, as strconv.ParseFloat requires.
func underscoresBetweenDigits(b []byte) bool {
	for i, c := range b {
		if c == '_' && !(i > 0 && isDigit(b[i-1]) && i+1 < len(b) && isDigit(b[i+1])) {
			return false
		}
	}
	return true
}

const (
	magOK = iota
	magOverflow
	magInvalid
)

// baseZero reads an unsigned integer the way strconv.ParseUint does with base 0,
// for text without underscores.
func baseZero(s []byte) (uint64, int) {
	if len(s) == 0 {
		return 0, magInvalid
	}
	base := uint64(10)
	if s[0] == '0' {
		switch {
		case len(s) >= 3 && s[1]|0x20 == 'b':
			base, s = 2, s[2:]
		case len(s) >= 3 && s[1]|0x20 == 'o':
			base, s = 8, s[2:]
		case len(s) >= 3 && s[1]|0x20 == 'x':
			base, s = 16, s[2:]
		default:
			base, s = 8, s[1:]
			if len(s) == 0 {
				return 0, magOK
			}
		}
	}
	var v uint64
	for _, c := range s {
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case c|0x20 >= 'a' && c|0x20 <= 'f':
			d = uint64(c|0x20-'a') + 10
		default:
			return 0, magInvalid
		}
		if d >= base {
			return 0, magInvalid
		}
		if v > (math.MaxUint64-d)/base {
			// Keep scanning: an invalid digit later is still a syntax error, and the
			// caller treats both the same way.
			return 0, magOverflow
		}
		v = v*base + d
	}
	return v, magOK
}

// isDecimalFloat reports whether p matches
// [-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?
func isDecimalFloat(p []byte) bool {
	i, n := 0, len(p)
	if i < n && (p[i] == '+' || p[i] == '-') {
		i++
	}
	digits := func() int {
		s := i
		for i < n && isDigit(p[i]) {
			i++
		}
		return i - s
	}
	if i < n && p[i] == '.' {
		i++
		if digits() == 0 {
			return false
		}
	} else {
		if digits() == 0 {
			return false
		}
		if i < n && p[i] == '.' {
			i++
			digits()
		}
	}
	if i < n && (p[i] == 'e' || p[i] == 'E') {
		i++
		if i < n && (p[i] == '+' || p[i] == '-') {
			i++
		}
		if digits() == 0 {
			return false
		}
	}
	return i == n
}

var v3TimeFormats = [...]string{
	"2006-1-2T15:4:5.999999999Z07:00",
	"2006-1-2t15:4:5.999999999Z07:00",
	"2006-1-2 15:4:5.999999999",
	"2006-1-2",
}

// ParseTimestampV3 reads the date and date-time forms yaml.v3 accepts: the
// RFC 3339 form with short date fields, a lower-case "t", a space-separated time
// with no zone, and a plain date.
func ParseTimestampV3(s string) (time.Time, bool) {
	i := 0
	for ; i < len(s); i++ {
		if c := s[i]; c < '0' || c > '9' {
			break
		}
	}
	if i != 4 || i == len(s) || s[i] != '-' {
		return time.Time{}, false
	}
	for _, f := range v3TimeFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// V3Bool11 reads the YAML 1.1 boolean words yaml.v3 accepts when the target is a
// bool: y, yes, on and n, no, off in the three usual cases.
func V3Bool11(b []byte) (v, ok bool) {
	switch string(b) {
	case "y", "Y", "yes", "Yes", "YES", "on", "On", "ON":
		return true, true
	case "n", "N", "no", "No", "NO", "off", "Off", "OFF":
		return false, true
	}
	return false, false
}
