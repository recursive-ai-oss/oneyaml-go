package resolve

import (
	"bytes"
	"errors"
	"math"
	"strconv"
)

// Resolve11 classifies b the way a YAML 1.1 reader does, following the type
// repository at yaml.org/type: y/n/yes/no/on/off booleans, integers with '_'
// separators, 0b binary, legacy 0-prefixed octal and base-60 forms, and floats
// with '_' separators and base-60 forms.
//
// Integers in base 60 report Base 60 and the digits as written. Merge keys and
// timestamps are separate predicates (IsMerge11, IsTimestamp11) because they are
// types of their own in YAML 1.1.
func Resolve11(b []byte) Result {
	n := len(b)
	if n == 0 {
		return Result{Kind: Null}
	}
	if n <= 5 {
		if v, ok := bool11(b); ok {
			return Result{Kind: Bool, B: v}
		}
		if b[0] == '~' && n == 1 || isNullWord(b) {
			return Result{Kind: Null}
		}
	}
	switch b[0] {
	case '+', '-', '.', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return number11(b)
	}
	return Result{}
}

func bool11(b []byte) (v, ok bool) {
	switch string(b) {
	case "y", "Y", "yes", "Yes", "YES", "on", "On", "ON":
		return true, true
	case "n", "N", "no", "No", "NO", "off", "Off", "OFF":
		return false, true
	}
	return coreBool(b)
}

func number11(b []byte) Result {
	n := len(b)
	i := 0
	neg := false
	if b[0] == '+' || b[0] == '-' {
		neg = b[0] == '-'
		i = 1
		if n == 1 {
			return Result{}
		}
	}
	rest := b[i:]
	if f, ok := special11(b); ok {
		return Result{Kind: Float, F: f}
	}
	if rest[0] == '.' {
		return float11(b, rest, neg)
	}
	if !isDigit(rest[0]) {
		return Result{}
	}
	if rest[0] == '0' && len(rest) > 1 {
		switch rest[1] {
		case 'b':
			return radix11(rest[2:], 2, neg)
		case 'x':
			return radix11(rest[2:], 16, neg)
		}
		if isDigit(rest[1]) || rest[1] == '_' {
			return radix11(rest[1:], 8, neg)
		}
	}
	// Decimal or base 60 integer, or a float with a '.'.
	j := 0
	for j < len(rest) && (isDigit(rest[j]) || rest[j] == '_') {
		j++
	}
	switch {
	case j == len(rest):
		if rest[0] == '_' {
			return Result{}
		}
		return radix11(rest, 10, neg)
	case rest[j] == ':':
		return sexagesimal11(b, rest, neg)
	case rest[j] == '.':
		return float11(b, rest, neg)
	}
	return Result{}
}

func special11(b []byte) (float64, bool) {
	s := b
	neg := false
	if s[0] == '+' || s[0] == '-' {
		neg = s[0] == '-'
		s = s[1:]
	}
	switch string(s) {
	case ".inf", ".Inf", ".INF":
		if neg {
			return math.Inf(-1), true
		}
		return math.Inf(1), true
	case ".nan", ".NaN", ".NAN":
		if len(s) == len(b) {
			return math.NaN(), true
		}
	}
	return 0, false
}

// radix11 reads digits (with '_' allowed anywhere, as the 1.1 patterns do)
// in the given base. At least one real digit is required.
func radix11(digits []byte, base uint64, neg bool) Result {
	if len(digits) == 0 {
		return Result{}
	}
	var v uint64
	wide, seen := false, false
	for _, c := range digits {
		var d uint64
		switch {
		case c == '_':
			continue
		case isDigit(c):
			d = uint64(c - '0')
		case base == 16 && isHex(c):
			d = uint64(c|0x20-'a') + 10
		default:
			return Result{}
		}
		if d >= base {
			return Result{}
		}
		seen = true
		if !wide {
			if v > (math.MaxUint64-d)/base {
				wide = true
			} else {
				v = v*base + d
			}
		}
	}
	if !seen {
		return Result{}
	}
	r := Result{Kind: Int, Base: uint8(base), Neg: neg, Digits: digits}
	if wide {
		r.W = Wide
		return r
	}
	return finishInt(r, v)
}

// sexagesimal11 handles [1-9][0-9_]*(:[0-5]?[0-9])+ and the same with a
// fraction (a float).
func sexagesimal11(whole, rest []byte, neg bool) Result {
	if rest[0] == '0' || rest[0] == '_' {
		return Result{}
	}
	j := 0
	var total uint64
	over := false
	floatPart := -1
	for j < len(rest) {
		k := j
		var g uint64
		digs := 0
		for k < len(rest) && (isDigit(rest[k]) || (rest[k] == '_' && j == 0)) {
			if rest[k] != '_' {
				g = g*10 + uint64(rest[k]-'0')
				digs++
				if g > 1<<40 {
					over = true
					g = 1 << 40
				}
			}
			k++
		}
		if digs == 0 {
			return Result{}
		}
		if j > 0 && (digs > 2 || g > 59) {
			return Result{}
		}
		total = total*60 + g
		if total > math.MaxInt64/60 {
			over = true
		}
		j = k
		if j == len(rest) {
			break
		}
		if rest[j] == ':' {
			j++
			if j == len(rest) {
				return Result{}
			}
			continue
		}
		if rest[j] == '.' {
			floatPart = j
			break
		}
		return Result{}
	}
	if floatPart < 0 {
		r := Result{Kind: Int, Base: 60, Neg: neg, Digits: rest}
		if over {
			r.W = Wide
			return r
		}
		return finishInt(r, total)
	}
	// Fraction digits and '_' only.
	frac := rest[floatPart+1:]
	for _, c := range frac {
		if !isDigit(c) && c != '_' {
			return Result{}
		}
	}
	f := float64(total)
	scale := 0.1
	for _, c := range frac {
		if c == '_' {
			continue
		}
		f += float64(c-'0') * scale
		scale /= 10
	}
	if neg {
		f = -f
	}
	return Result{Kind: Float, F: f}
}

// float11 handles [0-9][0-9_]*\.[0-9_]*(e[-+][0-9]+)? and \.[0-9_]+(e[-+][0-9]+)?.
// rest starts at the first digit or the dot.
func float11(whole, rest []byte, neg bool) Result {
	i := 0
	for i < len(rest) && (isDigit(rest[i]) || rest[i] == '_') {
		i++
	}
	if i == 0 && rest[0] != '.' {
		return Result{}
	}
	if i > 0 && rest[0] == '_' {
		return Result{}
	}
	if i == len(rest) || rest[i] != '.' {
		return Result{}
	}
	i++
	fs := i
	for i < len(rest) && (isDigit(rest[i]) || rest[i] == '_') {
		i++
	}
	if rest[0] == '.' && i == fs {
		return Result{}
	}
	if i < len(rest) {
		if rest[i] != 'e' && rest[i] != 'E' {
			return Result{}
		}
		i++
		if i >= len(rest) || (rest[i] != '+' && rest[i] != '-') {
			return Result{}
		}
		i++
		es := i
		for i < len(rest) && isDigit(rest[i]) {
			i++
		}
		if i == es || i != len(rest) {
			return Result{}
		}
	}
	// Parse a copy without '_' (rare, so the allocation is acceptable).
	clean := make([]byte, 0, len(whole))
	for _, c := range whole {
		if c != '_' {
			clean = append(clean, c)
		}
	}
	f, _ := strconv.ParseFloat(string(clean), 64)
	return Result{Kind: Float, F: f}
}

// IsMerge11 reports whether b is the YAML 1.1 merge key "<<".
func IsMerge11(b []byte) bool { return len(b) == 2 && b[0] == '<' && b[1] == '<' }

// IsTimestamp11 reports whether b has the shape of a YAML 1.1 timestamp: a date
// YYYY-MM-DD or a date and time with optional fraction and zone.
func IsTimestamp11(b []byte) bool {
	n := len(b)
	if n < 8 || !(isDigit(b[0]) && isDigit(b[1]) && isDigit(b[2]) && isDigit(b[3]) && b[4] == '-') {
		return false
	}
	i := 5
	i, ok := digitsRange(b, i, 1, 2)
	if !ok || i >= n || b[i] != '-' {
		return false
	}
	i, ok = digitsRange(b, i+1, 1, 2)
	if !ok {
		return false
	}
	if i == n {
		return true
	}
	if b[i] == 'T' || b[i] == 't' {
		i++
	} else if b[i] == ' ' || b[i] == '\t' {
		for i < n && (b[i] == ' ' || b[i] == '\t') {
			i++
		}
	} else {
		return false
	}
	i, ok = digitsRange(b, i, 1, 2)
	if !ok || i >= n || b[i] != ':' {
		return false
	}
	// Readers built on time layouts such as "2006-1-2 15:4:5" take one digit
	// for minutes and seconds too, so the writer treats that as a timestamp.
	i, ok = digitsRange(b, i+1, 1, 2)
	if !ok || i >= n || b[i] != ':' {
		return false
	}
	i, ok = digitsRange(b, i+1, 1, 2)
	if !ok {
		return false
	}
	if i < n && b[i] == '.' {
		i++
		for i < n && isDigit(b[i]) {
			i++
		}
	}
	for i < n && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	if i == n {
		return true
	}
	if b[i] == 'Z' {
		return i+1 == n
	}
	if b[i] != '+' && b[i] != '-' {
		return false
	}
	i, ok = digitsRange(b, i+1, 1, 2)
	if !ok {
		return false
	}
	if i == n {
		return true
	}
	if b[i] != ':' {
		return false
	}
	i, ok = digitsRange(b, i+1, 2, 2)
	return ok && i == n
}

func digitsRange(b []byte, i, lo, hi int) (int, bool) {
	s := i
	for i < len(b) && i-s < hi && isDigit(b[i]) {
		i++
	}
	return i, i-s >= lo
}

// ReadsAsString reports whether b, written as a plain scalar, is read as the
// text b itself by every common reader: it is a string under the Core schema and
// under YAML 1.1, and it is neither a 1.1 timestamp, nor the merge key "<<", nor
// the 1.1 value indicator "=".
func ReadsAsString(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	switch b[0] {
	case '<':
		if IsMerge11(b) {
			return false
		}
	case '=':
		if len(b) == 1 {
			return false
		}
	case 'n', 'N', 'y', 'Y', 'o', 'O', 't', 'T', 'f', 'F', '~':
		return Core(b).Kind == String && Resolve11(b).Kind == String && !strconvFloatWord(b)
	case 'i', 'I', '_', '+', '-', '.', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return numberFormsAreStrings(b)
	}
	return true
}

// numberFormsAreStrings is the number-like half of ReadsAsString.
func numberFormsAreStrings(b []byte) bool {
	if !(Core(b).Kind == String && Resolve11(b).Kind == String && !IsTimestamp11(b) && !looseRadix(b) && !strconvFloatWord(b)) {
		return false
	}
	// Some readers skip any run of signs, so "+-1" is the integer 1 to them.
	if n := signRun(b); n >= 2 && n < len(b) {
		rest := b[n:]
		if k := Core(rest).Kind; k == Int || k == Float {
			return false
		}
		if k := Resolve11(rest).Kind; k == Int || k == Float {
			return false
		}
	}
	// Readers strip '_' wherever it occurs before parsing a number, so "0_" and
	// "-_1" are integers to them. Judge the text with the underscores removed.
	if bytes.IndexByte(b, '_') < 0 {
		return true
	}
	var a [64]byte
	c := a[:0]
	for _, x := range b {
		if x != '_' {
			c = append(c, x)
		}
	}
	if len(c) == 0 {
		return true
	}
	// Only numbers matter here: a reader that strips underscores does it to parse
	// a number, so "_y" is not a boolean to anyone.
	return !isNumberKind(Core(c).Kind) && !isNumberKind(Resolve11(c).Kind) && !looseRadix(c) && !strconvFloatWord(c)
}

func isNumberKind(k Kind) bool { return k == Int || k == Float }

// strconvFloatWord reports whether strconv.ParseFloat reads b as a number even
// though it is not a decimal literal: inf, infinity and unsigned nan in any case, and
// hexadecimal floats such as 0x1p-2. Readers built on ParseFloat accept these.
func strconvFloatWord(b []byte) bool {
	j := 0
	if len(b) > 0 && (b[0] == '+' || b[0] == '-') {
		j = 1
	}
	r := b[j:]
	if len(r) < 3 {
		return false
	}
	if r[0] != '0' {
		if len(r) != 3 && len(r) != 8 {
			return false
		}
		// ParseFloat takes a sign before inf and infinity but not before nan.
		return (j == 0 && bytes.EqualFold(r, []byte("nan"))) ||
			bytes.EqualFold(r, []byte("inf")) || bytes.EqualFold(r, []byte("infinity"))
	}
	if r[1] != 'x' && r[1] != 'X' {
		return false
	}
	_, err := strconv.ParseFloat(string(b), 64)
	return err == nil || errors.Is(err, strconv.ErrRange)
}

// looseRadix reports whether b looks like an integer with a 0x, 0o or 0b prefix
// in any letter case, with an optional sign before or after the prefix, which readers built on
// strconv.ParseInt with base 0 (yaml.v3, goccy) accept even though neither the
// Core schema nor YAML 1.1 does. Writers must not leave such text plain.
func looseRadix(b []byte) bool {
	i := 0
	if b[0] == '+' || b[0] == '-' {
		i = 1
	}
	if len(b) < i+3 || b[i] != '0' {
		return false
	}
	switch b[i+1] {
	case 'x', 'X', 'o', 'O', 'b', 'B':
	default:
		return false
	}
	rest := b[i+2:]
	// strconv.ParseInt accepts a second sign after the prefix: "0b-1" is -1.
	if len(rest) > 1 && (rest[0] == '+' || rest[0] == '-') {
		rest = rest[1:]
	}
	for _, c := range rest {
		if !isHex(c) && c != '_' {
			return false
		}
	}
	return true
}

// ReadsAsStringS is ReadsAsString for a string. It copies only the strings
// whose first byte can start a non-string form, and then into a stack buffer, so
// it does not allocate for text of up to 64 bytes.
func ReadsAsStringS(s string) bool {
	if len(s) == 0 {
		return false
	}
	switch s[0] {
	case '<', '=', 'i', 'I', '_', 'n', 'N', 'y', 'Y', 'o', 'O', 't', 'T', 'f', 'F', '~',
		'+', '-', '.', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		if len(s) <= 64 {
			var a [64]byte
			return ReadsAsString(a[:copy(a[:], s)])
		}
		return ReadsAsString([]byte(s))
	}
	return true
}

// signRun is the length of the run of '+' and '-' at the start of b.
func signRun(b []byte) int {
	n := 0
	for n < len(b) && (b[n] == '+' || b[n] == '-') {
		n++
	}
	return n
}
