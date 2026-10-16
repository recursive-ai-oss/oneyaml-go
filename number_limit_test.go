package oneyaml_test

import (
	"errors"
	"math/big"
	"strconv"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
	"oneyaml.dev/go/internal/resolve"
)

func digits(n int) string { return "1" + strings.Repeat("7", n-1) }

func wantLimit(t *testing.T, err error) {
	t.Helper()
	var le *oneyaml.LimitError
	if !errors.As(err, &le) || le.Limit != oneyaml.LimitMaxNumberDigits {
		t.Fatalf("want a number digits LimitError, got %v", err)
	}
}

// A number past MaxNumberDigits is refused when it would be converted, in every
// place a number is built, and never when the text is only read as a string.
func TestMaxNumberDigits(t *testing.T) {
	big := digits(200_000)
	limit := oneyaml.Limits(oneyaml.LimitSet{MaxNumberDigits: 1000})
	for name, run := range map[string]func() error{
		"any":       func() error { var v any; return oneyaml.Unmarshal([]byte(big), &v, limit) },
		"in list":   func() error { var v any; return oneyaml.Unmarshal([]byte("[a, "+big+"]"), &v, limit) },
		"map key":   func() error { var v any; return oneyaml.Unmarshal([]byte("{ "+big+": 1 }"), &v, limit) },
		"big.Int":   func() error { var v *bigInt; return oneyaml.Unmarshal([]byte(big), &v, limit) },
		"float":     func() error { var v float64; return oneyaml.Unmarshal([]byte(big), &v, limit) },
		"float tag": func() error { var v any; return oneyaml.Unmarshal([]byte("!!float "+big), &v, limit) },
		"float text": func() error {
			var v any
			return oneyaml.Unmarshal([]byte("0."+strings.Repeat("0", 5000)+"1"), &v, limit)
		},
		"document": func() error {
			d, err := oneyaml.Parse([]byte("a: " + big))
			if err != nil {
				return err
			}
			var v any
			return d.Decode(&v)
		},
		"untrusted": func() error {
			var v any
			return oneyaml.Unmarshal([]byte("1"+strings.Repeat("0", 5000)), &v, oneyaml.Untrusted())
		},
	} {
		t.Run(name, func(t *testing.T) { wantLimit(t, run()) })
	}
	t.Run("string target", func(t *testing.T) {
		var s string
		if err := oneyaml.Unmarshal([]byte(big), &s, limit); err != nil || len(s) != len(big) {
			t.Fatalf("%d bytes, %v", len(s), err)
		}
	})
	t.Run("quoted", func(t *testing.T) {
		var v any
		if err := oneyaml.Unmarshal([]byte(`"`+big+`"`), &v, limit); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("node text", func(t *testing.T) {
		d, err := oneyaml.Parse([]byte("a: " + big))
		if err != nil {
			t.Fatal(err)
		}
		var s string
		if err := d.Get("/a", &s); err != nil || len(s) != len(big) {
			t.Fatalf("%d bytes, %v", len(s), err)
		}
	})
	t.Run("no limits", func(t *testing.T) {
		var v any
		if err := oneyaml.Unmarshal([]byte(digits(20_000)), &v, oneyaml.NoLimits()); err != nil {
			t.Fatal(err)
		}
	})
}

type bigInt = big.Int

// The default allows a 100,000 digit integer, exactly.
func TestHundredThousandDigitsDecode(t *testing.T) {
	in := digits(100_000)
	want, _ := new(big.Int).SetString(in, 10)
	var v any
	if err := oneyaml.Unmarshal([]byte(in), &v); err != nil {
		t.Fatal(err)
	}
	if got, ok := v.(*big.Int); !ok || got.Cmp(want) != 0 {
		t.Fatalf("got %T", v)
	}
	var b big.Int
	if err := oneyaml.Unmarshal([]byte(in), &b); err != nil || b.Cmp(want) != 0 {
		t.Fatal(err)
	}
	// One digit more is past the default.
	wantLimit(t, oneyaml.Unmarshal([]byte(digits(100_001)), &v))
}

// A huge float text is read without any big-integer conversion (the float is
// strconv's, which is linear in the text), so the digit limit does not apply.
func TestHugeFloatIsLinear(t *testing.T) {
	in := "0." + strings.Repeat("3", 2_000_000) + "e5"
	want, _ := strconv.ParseFloat(in, 64)
	var v any
	before := resolve.BigConversions()
	err := oneyaml.Unmarshal([]byte(in), &v, oneyaml.NoLimits())
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := v.(float64); !ok || f != want {
		t.Fatalf("got %T %v", v, v)
	}
	if n := resolve.BigConversions() - before; n != 0 {
		t.Errorf("%d big-integer conversions for a float", n)
	}
}
