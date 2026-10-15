// Package resolve decides what a plain scalar means.
//
// Core classifies text under the YAML 1.2 Core schema. Resolve11 classifies it
// the way a YAML 1.1 reader would, and ReadsAsString combines the two, so that a
// writer can tell whether a string may be written unquoted and still come back
// as the same string from any common reader.
//
// Every classifier works on bytes and does not allocate.
package resolve

import (
	"math"
	"math/big"
	"strconv"
)

// Kind is the resolved type of a plain scalar.
type Kind uint8

// The resolved kinds. String is the zero value: text that matches no other form.
const (
	String Kind = iota
	Null
	Bool
	Int
	Float
)

func (k Kind) String() string {
	switch k {
	case Null:
		return "null"
	case Bool:
		return "bool"
	case Int:
		return "int"
	case Float:
		return "float"
	}
	return "string"
}

// Width says how much of an integer a Result holds.
type Width uint8

// Integer widths.
const (
	// Fits64 means the value is in Result.I.
	Fits64 Width = iota
	// FitsU64 means the value is positive, above MaxInt64, and in Result.U.
	FitsU64
	// Wide means the value needs a big.Int; call Result.Big.
	Wide
)

// Result is a classified scalar.
//
// Only the field that belongs to Kind is meaningful: B for Bool, I/U/Wide for
// Int, F for Float.
type Result struct {
	Kind Kind
	B    bool
	W    Width
	I    int64
	U    uint64
	F    float64

	// Base, Neg and Digits describe an Int so that a wide value can be rebuilt.
	// Digits is a sub-slice of the classified bytes without sign or prefix and,
	// for forms that allow it, still containing '_' separators.
	Base   uint8
	Neg    bool
	Digits []byte
}

// Big returns the value of an Int result as a big.Int. It allocates.
func (r Result) Big() *big.Int {
	n := new(big.Int)
	switch r.W {
	case Fits64:
		n.SetInt64(r.I)
		return n
	case FitsU64:
		n.SetUint64(r.U)
		return n
	}
	d := r.Digits
	clean := make([]byte, 0, len(d))
	for _, c := range d {
		if c != '_' {
			clean = append(clean, c)
		}
	}
	if r.Base == 60 {
		// Sexagesimal digits are groups separated by ':'; fold them in base 60.
		acc := new(big.Int)
		sixty := big.NewInt(60)
		var group big.Int
		start := 0
		for i := 0; i <= len(clean); i++ {
			if i == len(clean) || clean[i] == ':' {
				group.SetString(string(clean[start:i]), 10)
				acc.Mul(acc, sixty)
				acc.Add(acc, &group)
				start = i + 1
			}
		}
		n = acc
	} else {
		n.SetString(string(clean), int(r.Base))
	}
	if r.Neg {
		n.Neg(n)
	}
	return n
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'f')
}

// Core classifies b under the YAML 1.2 Core schema (section 10.3.2).
//
// Only plain scalars are resolved this way; the caller must not call it for
// quoted scalars or scalars with an explicit tag. The empty string is null.
func Core(b []byte) Result {
	n := len(b)
	if n == 0 {
		return Result{Kind: Null}
	}
	switch c := b[0]; c {
	case '~':
		if n == 1 {
			return Result{Kind: Null}
		}
	case 'n', 'N':
		if isNullWord(b) {
			return Result{Kind: Null}
		}
	case 't', 'T', 'f', 'F':
		if v, ok := coreBool(b); ok {
			return Result{Kind: Bool, B: v}
		}
	case '.':
		if f, ok := coreSpecialFloat(b); ok {
			return Result{Kind: Float, F: f}
		}
		return coreNumber(b)
	case '-', '+', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return coreNumber(b)
	}
	return Result{}
}

func isNullWord(b []byte) bool {
	switch string(b) {
	case "null", "Null", "NULL":
		return true
	}
	return false
}

func coreBool(b []byte) (v, ok bool) {
	switch string(b) {
	case "true", "True", "TRUE":
		return true, true
	case "false", "False", "FALSE":
		return false, true
	}
	return false, false
}

// coreSpecialFloat recognises .inf, .Inf, .INF, +/-, and .nan, .NaN, .NAN.
// A sign is allowed on infinity only.
func coreSpecialFloat(b []byte) (float64, bool) {
	s := b
	neg, signed := false, false
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		neg, signed = s[0] == '-', true
		s = s[1:]
	}
	switch string(s) {
	case ".inf", ".Inf", ".INF":
		if neg {
			return math.Inf(-1), true
		}
		return math.Inf(1), true
	case ".nan", ".NaN", ".NAN":
		if !signed {
			return math.NaN(), true
		}
	}
	return 0, false
}

var pow10tab = [...]float64{1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11,
	1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22}

// coreNumber handles the integer and decimal-float forms. b is non-empty and
// starts with a sign, a digit or '.'.
func coreNumber(b []byte) Result {
	n := len(b)
	i := 0
	neg := false
	if b[0] == '-' || b[0] == '+' {
		neg = b[0] == '-'
		i = 1
		if n == 1 {
			return Result{}
		}
		if b[1] == '.' {
			if f, ok := coreSpecialFloat(b); ok {
				return Result{Kind: Float, F: f}
			}
		}
	} else if b[0] == '0' && n > 2 {
		switch b[1] {
		case 'x':
			return radixInt(b[2:], 16, false)
		case 'o':
			return radixInt(b[2:], 8, false)
		}
	}

	// Decimal: digits [. digits] [e[+-]digits]
	start := i
	var mant uint64
	mdig := 0    // significant digits folded into mant
	fracDig := 0 // fraction digits folded into mant
	lead := true // still skipping leading zeros
	overflow := false
	for ; i < n && isDigit(b[i]); i++ {
		d := uint64(b[i] - '0')
		if lead && d == 0 {
			continue
		}
		lead = false
		if mdig < 19 {
			mant = mant*10 + d
			mdig++
		} else {
			overflow = true
		}
	}
	intDigits := i - start
	if i == n {
		if intDigits == 0 {
			return Result{}
		}
		return decimalInt(b[start:], neg, mant, overflow)
	}

	isFloat := false
	if b[i] == '.' {
		isFloat = true
		i++
		fs := i
		for ; i < n && isDigit(b[i]); i++ {
			d := uint64(b[i] - '0')
			if lead && d == 0 {
				fracDig++
				continue
			}
			lead = false
			if mdig < 19 {
				mant = mant*10 + d
				mdig++
				fracDig++
			} else {
				overflow = true
			}
		}
		if intDigits == 0 && i == fs {
			return Result{}
		}
	}
	exp := 0
	if i < n && (b[i] == 'e' || b[i] == 'E') {
		if intDigits == 0 && !isFloat {
			return Result{} // an exponent needs a mantissa: "+e5" is text
		}
		isFloat = true
		i++
		eneg := false
		if i < n && (b[i] == '+' || b[i] == '-') {
			eneg = b[i] == '-'
			i++
		}
		es := i
		for ; i < n && isDigit(b[i]); i++ {
			if exp < 100000 {
				exp = exp*10 + int(b[i]-'0')
			}
		}
		if i == es {
			return Result{}
		}
		if eneg {
			exp = -exp
		}
	}
	if i != n || !isFloat {
		return Result{}
	}
	// Clinger's fast path: the mantissa and the power of ten are both exact.
	if !overflow && mant < 1<<53 {
		e10 := exp - fracDig
		var f float64
		ok := true
		switch {
		case e10 == 0:
			f = float64(mant)
		case e10 > 0 && e10 <= 22:
			f = float64(mant) * pow10tab[e10]
		case e10 < 0 && e10 >= -22:
			f = float64(mant) / pow10tab[-e10]
		default:
			ok = false
		}
		if ok {
			if neg {
				f = -f
			}
			return Result{Kind: Float, F: f}
		}
	}
	f, _ := strconv.ParseFloat(string(b), 64) // out-of-range gives +-Inf, which Core allows
	return Result{Kind: Float, F: f}
}

// decimalInt builds an Int result from the digits (no sign) of a decimal.
func decimalInt(digits []byte, neg bool, mant uint64, overflow bool) Result {
	r := Result{Kind: Int, Base: 10, Neg: neg, Digits: digits}
	if !overflow {
		return finishInt(r, mant)
	}
	// More than 19 significant digits: try an exact uint64 parse.
	var v uint64
	for _, c := range digits {
		d := uint64(c - '0')
		if v > (math.MaxUint64-d)/10 {
			r.W = Wide
			return r
		}
		v = v*10 + d
	}
	return finishInt(r, v)
}

func finishInt(r Result, v uint64) Result {
	if r.Neg {
		if v <= 1<<63 {
			r.I = -int64(v) // v == 1<<63 wraps to MinInt64, which is correct
			return r
		}
		r.W = Wide
		return r
	}
	if v <= math.MaxInt64 {
		r.I = int64(v)
		return r
	}
	r.W = FitsU64
	r.U = v
	return r
}

// radixInt parses 0x and 0o integers. digits follows the two-byte prefix.
func radixInt(digits []byte, base uint64, allowUnderscore bool) Result {
	if len(digits) == 0 {
		return Result{}
	}
	var v uint64
	wide := false
	seen := false
	for _, c := range digits {
		var d uint64
		switch {
		case isDigit(c):
			d = uint64(c - '0')
		case base == 16 && isHex(c):
			d = uint64(c|0x20-'a') + 10
		case c == '_' && allowUnderscore:
			continue
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
	r := Result{Kind: Int, Base: uint8(base), Digits: digits}
	if wide {
		r.W = Wide
		return r
	}
	return finishInt(r, v)
}
