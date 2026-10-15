package yaml

import (
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"time"
)

// skind is what a scalar resolves to.
type skind uint8

const (
	skStr skind = iota
	skNull
	skBool
	skInt
	skUint
	skFloat
)

// sval is a resolved scalar. For skStr the text is the caller's own bytes, or
// str when the text had to be rebuilt (a !!binary value).
type sval struct {
	kind skind
	b    bool
	i    int64
	u    uint64
	f    float64
	str  []byte // set only when the string differs from the source text
	own  bool   // str is meaningful
}

// tagKind is an explicit tag the reader understands.
type tagKind uint8

const (
	tagNone tagKind = iota
	tagStr
	tagBinary
	tagInt
	tagFloat
	tagBool
	tagNull
	tagTimestamp
	tagOther // any tag that selects no resolution: the text stays a string
)

const coreTagPrefix = "tag:yaml.org,2002:"

// classifyTag maps a resolved tag to a tagKind.
func classifyTag(t string) tagKind {
	if len(t) > len(coreTagPrefix) && t[:len(coreTagPrefix)] == coreTagPrefix {
		switch t[len(coreTagPrefix):] {
		case "str":
			return tagStr
		case "binary":
			return tagBinary
		case "int":
			return tagInt
		case "float":
			return tagFloat
		case "bool":
			return tagBool
		case "null":
			return tagNull
		case "timestamp":
			return tagTimestamp
		}
	}
	return tagOther
}

func (t tagKind) name() string {
	switch t {
	case tagStr:
		return "!!str"
	case tagBinary:
		return "!!binary"
	case tagInt:
		return "!!int"
	case tagFloat:
		return "!!float"
	case tagBool:
		return "!!bool"
	case tagNull:
		return "!!null"
	case tagTimestamp:
		return "!!timestamp"
	}
	return "tag"
}

func (k skind) tagName() string {
	switch k {
	case skNull:
		return "!!null"
	case skBool:
		return "!!bool"
	case skInt, skUint:
		return "!!int"
	case skFloat:
		return "!!float"
	}
	return "!!str"
}

// resolveTable marks the first bytes that can start something other than a
// string: signs, digits, the letters of the boolean and null words, '~' and '.'.
var resolveTable = func() (t [256]byte) {
	t['+'], t['-'] = 'S', 'S'
	for c := '0'; c <= '9'; c++ {
		t[c] = 'D'
	}
	for _, c := range "yYnNtTfFoO~" {
		t[c] = 'M'
	}
	t['.'] = '.'
	return
}()

// resolve classifies the text of a scalar the way a YAML 1.1 reader of the
// yaml.v2 family does: y/yes/on and n/no/off are booleans, the usual null words
// are null, integers take 0x, 0b and legacy 0-prefixed octal forms with '_'
// separators, and floats include .inf and .nan. Base 60 numbers are not numbers.
// An explicit tag either narrows the choice or, when the text does not fit, is
// an error.
func resolve(in []byte, tag tagKind) (sval, error) {
	if tag == tagOther {
		return sval{}, nil
	}
	v, rk := resolveText(in, tag)
	switch {
	case tag == tagNone, tag == tagStr, tag == tagBinary:
	case tag == tagTimestamp && rk == rkTimestamp:
	case tag == tagFloat && (v.kind == skInt || v.kind == skUint) && rk == rkNumber:
		if v.kind == skInt {
			v = sval{kind: skFloat, f: float64(v.i)}
		} else {
			v = sval{kind: skFloat, f: float64(v.u)}
		}
	case tagMatches(tag, v.kind) && rk != rkTimestamp:
	default:
		got := v.kind.tagName()
		if rk == rkTimestamp {
			got = "!!timestamp"
		}
		return sval{}, fmt.Errorf("cannot decode %s `%s` as a %s", got, in, tag.name())
	}
	if tag == tagBinary {
		raw, err := base64.StdEncoding.DecodeString(string(in))
		if err != nil {
			return sval{}, fmt.Errorf("!!binary value contains invalid base64 data")
		}
		return sval{kind: skStr, str: raw, own: true}, nil
	}
	return v, nil
}

func tagMatches(t tagKind, k skind) bool {
	switch t {
	case tagInt:
		return k == skInt || k == skUint
	case tagFloat:
		return k == skFloat
	case tagBool:
		return k == skBool
	case tagNull:
		return k == skNull
	}
	return false
}

type resKind uint8

const (
	rkOther resKind = iota
	rkNumber
	rkTimestamp
)

func resolveText(in []byte, tag tagKind) (sval, resKind) {
	hint := byte('M') // the empty string reads as null
	if len(in) > 0 {
		hint = resolveTable[in[0]]
	}
	if hint == 0 || tag == tagStr || tag == tagBinary {
		return sval{}, rkOther
	}
	switch hint {
	case 'M', '.', 'S':
		if v, ok := resolveWord(in); ok {
			return v, rkOther
		}
	case 'D':
	}
	switch hint {
	case '.':
		// ".5" and the like; other text that starts with a dot is a string.
		if len(in) > 1 && in[1] >= '0' && in[1] <= '9' {
			if f, err := strconv.ParseFloat(string(in), 64); err == nil {
				return sval{kind: skFloat, f: f}, rkNumber
			}
		}
	case 'D', 'S':
		if v, ok := fastDecimal(in); ok {
			return v, rkNumber
		}
		if tag == tagNone || tag == tagTimestamp {
			if isTimestamp(in) {
				return sval{}, rkTimestamp
			}
		}
		plain := in
		for _, c := range in {
			if c == '_' {
				plain = removeUnderscores(in)
				break
			}
		}
		if intCandidate(plain) {
			s := string(plain)
			if n, err := strconv.ParseInt(s, 0, 64); err == nil {
				return sval{kind: skInt, i: n}, rkNumber
			}
			if n, err := strconv.ParseUint(s, 0, 64); err == nil {
				return sval{kind: skUint, u: n}, rkNumber
			}
		}
		if looksFloat(plain) {
			if f, err := strconv.ParseFloat(string(plain), 64); err == nil {
				return sval{kind: skFloat, f: f}, rkNumber
			}
		}
	}
	return sval{}, rkOther
}

// intCandidate reports whether b can be an integer literal in any base: an
// optional sign, then only letters and digits. It keeps text such as 10.0.0.1
// away from the integer parsers, whose errors allocate.
func intCandidate(b []byte) bool {
	if len(b) > 0 && (b[0] == '+' || b[0] == '-') {
		b = b[1:]
	}
	if len(b) == 0 || b[0] < '0' || b[0] > '9' {
		return false
	}
	for _, c := range b {
		if !(c >= '0' && c <= '9' || c|0x20 >= 'a' && c|0x20 <= 'z') {
			return false
		}
	}
	return true
}

func removeUnderscores(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c != '_' {
			out = append(out, c)
		}
	}
	return out
}

// fastDecimal reads a plain decimal integer without a leading zero.
func fastDecimal(b []byte) (sval, bool) {
	i := 0
	neg := false
	if b[0] == '-' || b[0] == '+' {
		neg = b[0] == '-'
		i = 1
	}
	n := len(b) - i
	if n == 0 || n > 18 || (b[i] == '0' && n > 1) {
		return sval{}, false
	}
	var v int64
	for ; i < len(b); i++ {
		c := b[i]
		if c < '0' || c > '9' {
			return sval{}, false
		}
		v = v*10 + int64(c-'0')
	}
	if neg {
		v = -v
	}
	return sval{kind: skInt, i: v}, true
}

// looksFloat matches [-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?
func looksFloat(b []byte) bool {
	i := 0
	if i < len(b) && (b[i] == '-' || b[i] == '+') {
		i++
	}
	digits := func() int {
		s := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
		return i - s
	}
	if i < len(b) && b[i] == '.' {
		i++
		if digits() == 0 {
			return false
		}
	} else {
		if digits() == 0 {
			return false
		}
		if i < len(b) && b[i] == '.' {
			i++
			digits()
		}
	}
	if i < len(b) && (b[i] == 'e' || b[i] == 'E') {
		i++
		if i < len(b) && (b[i] == '-' || b[i] == '+') {
			i++
		}
		if digits() == 0 {
			return false
		}
	}
	return i == len(b)
}

// resolveWord resolves the words a 1.1 reader knows by name.
func resolveWord(in []byte) (sval, bool) {
	switch string(in) {
	case "y", "Y", "yes", "Yes", "YES", "true", "True", "TRUE", "on", "On", "ON":
		return sval{kind: skBool, b: true}, true
	case "n", "N", "no", "No", "NO", "false", "False", "FALSE", "off", "Off", "OFF":
		return sval{kind: skBool}, true
	case "", "~", "null", "Null", "NULL":
		return sval{kind: skNull}, true
	case ".nan", ".NaN", ".NAN":
		return sval{kind: skFloat, f: math.NaN()}, true
	case ".inf", ".Inf", ".INF", "+.inf", "+.Inf", "+.INF":
		return sval{kind: skFloat, f: math.Inf(1)}, true
	case "-.inf", "-.Inf", "-.INF":
		return sval{kind: skFloat, f: math.Inf(-1)}, true
	}
	return sval{}, false
}

var timestampFormats = [...]string{
	"2006-1-2T15:4:5.999999999Z07:00",
	"2006-1-2t15:4:5.999999999Z07:00",
	"2006-1-2 15:4:5.999999999",
	"2006-1-2",
}

// isTimestamp reports whether in is one of the date forms a 1.1 reader takes as
// a timestamp. The value stays text in the JSON; only the classification
// matters, so that such text is quoted when written.
func isTimestamp(in []byte) bool {
	i := 0
	for i < len(in) && in[i] >= '0' && in[i] <= '9' {
		i++
	}
	if i != 4 || i == len(in) || in[i] != '-' {
		return false
	}
	s := string(in)
	for _, f := range timestampFormats {
		if _, err := time.Parse(f, s); err == nil {
			return true
		}
	}
	return false
}

// isBase60Float reports whether s is a base 60 number such as 1:30. A 1.1 reader
// may read it as a number, so a writer quotes it.
func isBase60Float(s []byte) bool {
	if len(s) == 0 {
		return false
	}
	c := s[0]
	if !(c == '+' || c == '-' || c >= '0' && c <= '9') {
		return false
	}
	colon := false
	for _, c := range s {
		if c == ':' {
			colon = true
			break
		}
	}
	if !colon {
		return false
	}
	// ^[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+(?:\.[0-9_]*)?$
	i := 0
	if s[i] == '+' || s[i] == '-' {
		i++
	}
	if i >= len(s) || s[i] < '0' || s[i] > '9' {
		return false
	}
	i++
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '_') {
		i++
	}
	groups := 0
	for i < len(s) && s[i] == ':' {
		i++
		// [0-5]?[0-9]
		if i < len(s) && s[i] >= '0' && s[i] <= '5' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
			i += 2
		} else if i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		} else {
			return false
		}
		groups++
	}
	if groups == 0 {
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '_') {
			i++
		}
	}
	return i == len(s)
}
