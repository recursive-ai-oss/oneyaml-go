package oneyaml

import (
	"math/big"
	"strconv"
	"strings"
)

// sameDecimal reports whether the float texts a and b stand for the same number
// when read exactly, so that two keys that are written differently but are one
// number are told from two numbers that only round together.
func sameDecimal(a, b string) bool { return canonNumber(a) == canonNumber(b) }

// maxExpDigits bounds the exponent that is added up exactly. Longer exponents are
// compared as written; two spellings of one such number are then taken for two
// numbers, which only changes which error is reported.
const maxExpDigits = 4096

// canonNumber returns a string that is equal for two float texts exactly when
// they stand for the same value: sign, significant digits and the decimal
// exponent for a decimal number, and the signed, lower-cased text for .inf and
// .nan. It works on the digits, so an exponent like e999999999 costs nothing.
func canonNumber(s string) string {
	t := strings.TrimPrefix(s, "+")
	neg := strings.HasPrefix(t, "-")
	if neg {
		t = t[1:]
	}
	sign := ""
	if neg {
		sign = "-"
	}
	var digits []byte
	point := -1
	i := 0
	for ; i < len(t); i++ {
		c := t[i]
		if c == '.' && point < 0 {
			point = len(digits)
		} else if c >= '0' && c <= '9' {
			digits = append(digits, c)
		} else {
			break
		}
	}
	if len(digits) == 0 || (i < len(t) && t[i] != 'e' && t[i] != 'E') {
		// .inf, .nan or something else that is not a decimal.
		return "t:" + sign + strings.ToLower(t)
	}
	if point < 0 {
		point = len(digits)
	}
	lead := 0
	for lead < len(digits) && digits[lead] == '0' {
		lead++
	}
	digits = digits[lead:]
	point -= lead
	for len(digits) > 0 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
	}
	if len(digits) == 0 {
		return "d:0" // zero, signed or not
	}
	exp := new(big.Int)
	if i < len(t) {
		et := strings.TrimPrefix(t[i+1:], "+")
		if len(strings.TrimLeft(et, "-0")) > maxExpDigits {
			return "x:" + sign + string(digits) + "/" + strconv.Itoa(point) + "/" + et
		}
		if _, ok := exp.SetString(et, 10); !ok {
			return "t:" + sign + strings.ToLower(t)
		}
	}
	exp.Add(exp, big.NewInt(int64(point)))
	return "d:" + sign + string(digits) + "e" + exp.String()
}
