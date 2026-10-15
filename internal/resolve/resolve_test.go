package resolve

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// want describes the expected resolution of one plain scalar.
type want struct {
	in string
	k  Kind
	b  bool    // Bool
	i  string  // Int, decimal text of the value
	f  float64 // Float (NaN compared as NaN)
}

func str(in string) want              { return want{in: in} }
func null(in string) want             { return want{in: in, k: Null} }
func boolean(in string, v bool) want  { return want{in: in, k: Bool, b: v} }
func integer(in, v string) want       { return want{in: in, k: Int, i: v} }
func float(in string, v float64) want { return want{in: in, k: Float, f: v} }

// coreTable mirrors the kit's schema/core-* cases, one group per case.
var coreTable = map[string][]want{
	"core-integers": {
		integer("12", "12"), integer("-7", "-7"), integer("+3", "3"), integer("0o14", "12"),
		integer("0xFF", "255"), integer("0777", "777"), integer("0", "0"),
	},
	"core-float-forms": {
		float("1.", 1), float(".5", .5), float("-.5", -.5), float("+.5", .5), float("+1.5", 1.5),
		float("1e+3", 1000), float("1E-3", .001), float(".1e2", 10), float("-1.5e-2", -.015),
		float("0.0", 0), integer("10", "10"),
	},
	"core-floats": {
		float("1.5", 1.5), float(".5", .5), float("1e3", 1000), float(".inf", math.Inf(1)),
		float("-.INF", math.Inf(-1)), float(".NaN", math.NaN()), float("12.", 12), float("6.02E+23", 6.02e23),
	},
	"core-infinity-and-nan-spellings": {
		float("+.inf", math.Inf(1)), float("+.INF", math.Inf(1)), float(".Inf", math.Inf(1)),
		float("-.inf", math.Inf(-1)), float(".nan", math.NaN()), float(".NAN", math.NaN()),
		str(".iNf"), str("nan"), str("inf"), str(".nAn"), str("-.nan"), str("+.nan"),
	},
	"core-integers-beyond-64-bits": {
		integer("18446744073709551616", "18446744073709551616"),
		integer("-9223372036854775809", "-9223372036854775809"),
		integer("0xFFFFFFFFFFFFFFFFFF", "4722366482869645213695"),
		integer("0o7777777777777777777777", "73786976294838206463"),
		integer("123456789012345678901234567890", "123456789012345678901234567890"),
		integer("18446744073709551615", "18446744073709551615"),
		integer("9223372036854775807", "9223372036854775807"),
		integer("-9223372036854775808", "-9223372036854775808"),
		integer("0000000000000000000000000001", "1"),
	},
	"core-integers-leading-zeros-and-signs": {
		integer("007", "7"), integer("-0", "0"), integer("+0", "0"), integer("00", "0"), integer("-012", "-12"),
	},
	"core-legacy-numbers-are-strings": {
		str("190:20:30"), str("0b1010"), str("1_000"), str("0x_1F"), str("1_000.5"), str("685230:15"),
	},
	"core-more-yaml-1-1-boolean-spellings-are-strings": {
		str("Yes"), str("YES"), str("no"), str("NO"), str("On"), str("ON"), str("off"), str("Off"),
		str("OFF"), str("n"), str("N"), str("Y"), str("yEs"),
	},
	"core-non-decimal-integers-take-no-sign": {
		str("-0x1F"), str("+0x1F"), str("-0o17"), str("0x"), str("0o"), str("0o8"), str("0xG"),
		str("0X1F"), str("0O17"),
	},
	"core-non-numbers-are-strings": {
		str("e3"), str("1e"), str("1.5.2"), str("."), str("-."), str("+"), str("1_0.5"), str("1,000"),
		str("0x1.8"), str("++1"), str("1 2"), str(".e3"),
	},
	"core-null-and-boolean": {
		null("null"), null("~"), null(""), boolean("true", true), boolean("False", false),
		boolean("TRUE", true), null("Null"), null("NULL"),
	},
	"core-null-and-boolean-near-misses-are-strings": {
		str("nUll"), str("nil"), str("None"), str("tRUE"), str("fALSE"), str("t"), str("f"), str("T"),
		str("F"), str("~~"), str("NULl"),
	},
	"core-timestamps-are-strings": {
		str("2001-12-14"), str("2001-12-14t21:59:43.10-05:00"), str("2001-12-14 21:59:43.10 -5"),
	},
	"core-yaml-1-1-spellings-are-strings": {
		str("yes"), str("No"), str("on"), str("0b11"), str("1_000"), str("1:30"), str("y"),
	},
}

func check(t *testing.T, name string, r Result, w want) {
	t.Helper()
	if r.Kind != w.k {
		t.Errorf("%s %q: kind %v, want %v", name, w.in, r.Kind, w.k)
		return
	}
	switch w.k {
	case Bool:
		if r.B != w.b {
			t.Errorf("%s %q: bool %v", name, w.in, r.B)
		}
	case Int:
		if got := r.Big().String(); got != w.i {
			t.Errorf("%s %q: int %s, want %s", name, w.in, got, w.i)
		}
	case Float:
		if math.IsNaN(w.f) {
			if !math.IsNaN(r.F) {
				t.Errorf("%s %q: float %v, want NaN", name, w.in, r.F)
			}
		} else if r.F != w.f {
			t.Errorf("%s %q: float %v, want %v", name, w.in, r.F, w.f)
		}
	}
}

func TestCoreKitCases(t *testing.T) {
	for name, ws := range coreTable {
		for _, w := range ws {
			check(t, "schema/"+name, Core([]byte(w.in)), w)
		}
	}
}

// TestCoreAgainstKitFiles reads the kit's own case files when ONEYAML_KIT points
// at a kit checkout, so the table above cannot drift from them.
func TestCoreAgainstKitFiles(t *testing.T) {
	kit := os.Getenv("ONEYAML_KIT")
	if kit == "" {
		t.Skip("ONEYAML_KIT not set")
	}
	dirs, _ := filepath.Glob(filepath.Join(kit, "suites/own/cases/schema/core-*"))
	if len(dirs) == 0 {
		t.Fatal("no core-* cases found under ONEYAML_KIT")
	}
	for _, dir := range dirs {
		in, err := os.ReadFile(filepath.Join(dir, "input.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		cj, err := os.ReadFile(filepath.Join(dir, "case.json"))
		if err != nil {
			t.Fatal(err)
		}
		var c struct {
			Expect struct {
				Value []json.RawMessage `json:"value"`
				// A bounded case accepts either a refusal or this value.
				Accept struct {
					Value []json.RawMessage `json:"value"`
				} `json:"accept"`
			} `json:"expect"`
		}
		dec := json.NewDecoder(bytes.NewReader(cj))
		err = dec.Decode(&c)
		if c.Expect.Value == nil {
			c.Expect.Value = c.Expect.Accept.Value
		}
		if err != nil || len(c.Expect.Value) != 1 {
			t.Fatalf("%s: bad case file: %v", dir, err)
		}
		// A case is either a block mapping of "key: value" lines checked against an
		// object, or a flow sequence checked item by item against an array.
		var exp map[string]json.RawMessage
		var items []json.RawMessage
		if err := json.Unmarshal(c.Expect.Value[0], &exp); err != nil {
			if err := json.Unmarshal(c.Expect.Value[0], &items); err != nil {
				t.Fatalf("%s: expected value is neither an object nor an array: %v", dir, err)
			}
		}
		type pair struct{ key, val string }
		var inputs []pair
		text := strings.TrimRight(string(in), "\n")
		if items != nil {
			body := strings.TrimSuffix(strings.TrimPrefix(text, "["), "]")
			exp = map[string]json.RawMessage{}
			for i, item := range strings.Split(body, ", ") {
				inputs = append(inputs, pair{strconv.Itoa(i), item})
			}
			for i, it := range items {
				exp[strconv.Itoa(i)] = it
			}
		} else {
			for _, line := range strings.Split(text, "\n") {
				key, val, _ := strings.Cut(line, ":")
				inputs = append(inputs, pair{key, strings.TrimPrefix(val, " ")})
			}
		}
		n := 0
		for _, in := range inputs {
			key, val := in.key, in.val
			r := Core([]byte(val))
			e := exp[key]
			n++
			name := filepath.Base(dir) + "/" + key
			var v any
			d := json.NewDecoder(bytes.NewReader(e))
			d.UseNumber()
			if err := d.Decode(&v); err != nil {
				t.Fatal(err)
			}
			switch x := v.(type) {
			case nil:
				if r.Kind != Null {
					t.Errorf("%s %q: got %v, want null", name, val, r.Kind)
				}
			case bool:
				if r.Kind != Bool || r.B != x {
					t.Errorf("%s %q: got %v, want bool %v", name, val, r.Kind, x)
				}
			case string:
				if r.Kind != String {
					t.Errorf("%s %q: got %v, want string", name, val, r.Kind)
				}
			case json.Number:
				s := x.String()
				if strings.ContainsAny(s, ".eE") {
					f, _ := x.Float64()
					if r.Kind != Float || r.F != f {
						t.Errorf("%s %q: got %v %v, want float %v", name, val, r.Kind, r.F, f)
					}
				} else {
					want, _ := new(big.Int).SetString(s, 10)
					if r.Kind != Int || r.Big().Cmp(want) != 0 {
						t.Errorf("%s %q: got %v, want int %s", name, val, r.Kind, s)
					}
				}
			case map[string]any:
				var f float64
				switch x["$float"] {
				case "inf":
					f = math.Inf(1)
				case "-inf":
					f = math.Inf(-1)
				default:
					f = math.NaN()
				}
				if r.Kind != Float || (math.IsNaN(f) != math.IsNaN(r.F)) || (!math.IsNaN(f) && f != r.F) {
					t.Errorf("%s %q: got %v %v, want float %v", name, val, r.Kind, r.F, f)
				}
			}
		}
		if n != len(exp) {
			t.Errorf("%s: %d inputs, %d expected values", dir, n, len(exp))
		}
	}
}

func TestResolve11(t *testing.T) {
	tests := []want{
		boolean("y", true), boolean("Y", true), boolean("yes", true), boolean("Yes", true), boolean("ON", true),
		boolean("n", false), boolean("No", false), boolean("off", false), boolean("false", false),
		null("~"), null("Null"), null(""),
		integer("0b1010", "10"), integer("-0b11", "-3"), integer("0755", "493"), integer("1_000", "1000"),
		integer("0x_1F", "31"), integer("1:30", "90"), integer("190:20:30", "685230"), integer("-1:00", "-60"),
		integer("0", "0"), integer("+12", "12"),
		float("1_000.5", 1000.5), float("1.5", 1.5), float(".5", .5), float("1.", 1), float("1.5e+3", 1500),
		float("1:30.5", 90.5), float(".inf", math.Inf(1)), float("-.Inf", math.Inf(-1)), float(".NaN", math.NaN()),
		str("1e3"), str("08"), str("0b"), str("0x"), str("1:60"), str("1:"), str("_1"), str("0_8"),
		str("1,000"), str("yEs"), str("nil"), str("."), str(".e3"), str("2001-12-14"), str("-.nan"),
		str("1.5e3"), str("1:30:"), str("0:30"),
	}
	for _, w := range tests {
		check(t, "1.1", Resolve11([]byte(w.in)), w)
	}
}

func TestTimestampAndReadsAsString(t *testing.T) {
	for _, s := range []string{"2001-12-14", "2001-12-14t21:59:43.10-05:00", "2001-12-14 21:59:43.10 -5",
		"2001-1-1", "2001-12-14T21:59:43Z", "2001-12-14 21:59:43.1 Z", "2001-12-14T01:02:03+5:30"} {
		if !IsTimestamp11([]byte(s)) {
			t.Errorf("IsTimestamp11(%q) = false", s)
		}
		if ReadsAsString([]byte(s)) {
			t.Errorf("ReadsAsString(%q) = true", s)
		}
	}
	for _, s := range []string{"2001-12", "20011214", "2001-12-14x", "hello", "2001-12-14T21:59", "2001-13-1-1"} {
		if IsTimestamp11([]byte(s)) {
			t.Errorf("IsTimestamp11(%q) = true", s)
		}
	}
	unsafe := []string{"", "~", "null", "Null", "true", "yes", "No", "on", "OFF", "y", "N", "0", "12", "-1", "+1",
		"0x1F", "0o7", "0777", "0b11", "1_000", "1:30", ".5", "1.", "1e3", ".inf", "-.INF", ".NaN", "<<", "=", "1.5", "0O17", "0X1f", "0B11", "-0o17", "+0b1", "0o_7", "+-1", "-+0", "--5", "0_", "-_1", "0_."}
	for _, s := range unsafe {
		if ReadsAsString([]byte(s)) {
			t.Errorf("ReadsAsString(%q) = true, want false", s)
		}
	}
	safe := []string{"hello", "yes please", "nope", "nul", "e3", "0x", "1e", "-", "+", ".", "a=b", "<<<", "==", "1,000",
		"0xG", "0o", "v1.2", "1.2.3", "0.0.0.0", "x:1", "Ok", "tru", "falsey", "0b", "12abc", "-x"}
	for _, s := range safe {
		if !ReadsAsString([]byte(s)) {
			t.Errorf("ReadsAsString(%q) = false, want true", s)
		}
	}
}

func TestNoAllocs(t *testing.T) {
	inputs := [][]byte{[]byte("12345"), []byte("-0.25"), []byte("true"), []byte("hello world"), []byte("1.5e10"), []byte("0xFF")}
	n := testing.AllocsPerRun(100, func() {
		for _, in := range inputs {
			_ = Core(in)
			_ = ReadsAsString(in)
		}
	})
	if n != 0 {
		t.Fatalf("classification allocates: %v allocs", n)
	}
}

func TestCoreFloatsExact(t *testing.T) {
	// The fast path must agree with strconv for every input it accepts.
	for _, s := range []string{"0.1", "1.7976931348623157e308", "4.9e-324", "123456789.123456789", "9007199254740993.0",
		"1e22", "1e23", "0.000001", "1.0000000000000002", "3.141592653589793", "100000000000000000000000.0"} {
		r := Core([]byte(s))
		if r.Kind != Float {
			t.Fatalf("%q kind %v", s, r.Kind)
		}
		var want float64
		if err := json.Unmarshal([]byte(s), &want); err != nil {
			t.Fatal(err)
		}
		if r.F != want {
			t.Errorf("%q = %v, want %v", s, r.F, want)
		}
	}
}

func FuzzCore(f *testing.F) {
	for _, w := range coreTable {
		for _, x := range w {
			f.Add(x.in)
		}
	}
	f.Fuzz(func(t *testing.T, s string) {
		b := []byte(s)
		r := Core(b)
		r11 := Resolve11(b)
		_ = ReadsAsString(b)
		_ = IsTimestamp11(b)
		if r.Kind == Int {
			_ = r.Big()
		}
		if r11.Kind == Int {
			_ = r11.Big()
		}
		// A string the plain-safety check accepts is a string under both schemas.
		if ReadsAsString(b) && (r.Kind != String || r11.Kind != String) {
			t.Fatalf("%q reads as string but resolves to %v / %v", s, r.Kind, r11.Kind)
		}
	})
}

func BenchmarkCore(b *testing.B) {
	inputs := [][]byte{[]byte("12345"), []byte("-0.25"), []byte("true"), []byte("hello world"), []byte("1.5e10"), []byte("0xFF"), []byte("null")}
	b.ReportAllocs()
	for b.Loop() {
		for _, in := range inputs {
			_ = Core(in)
		}
	}
}

func BenchmarkReadsAsString(b *testing.B) {
	inputs := [][]byte{[]byte("12345"), []byte("hello world"), []byte("nginx-ingress"), []byte("registry.k8s.io/ingress"), []byte("yes"), []byte("2001-12-14")}
	b.ReportAllocs()
	for b.Loop() {
		for _, in := range inputs {
			_ = ReadsAsString(in)
		}
	}
}

func TestReadsAsStringS(t *testing.T) {
	for _, s := range []string{"", "~", "yes", "hello", "12", "2001-12-14", "<<", "=", "a=b", "v1.2", "-x", strings.Repeat("1", 100), strings.Repeat("a", 100)} {
		if got, want := ReadsAsStringS(s), ReadsAsString([]byte(s)); got != want {
			t.Errorf("%q: %v vs %v", s, got, want)
		}
	}
	if n := testing.AllocsPerRun(100, func() { _ = ReadsAsStringS("nginx-ingress"); _ = ReadsAsStringS("1.2.3") }); n != 0 {
		t.Fatalf("allocates: %v", n)
	}
}

// A sign and an exponent without a mantissa are text, not a float (section
// 10.3.2 needs a digit before the exponent).
func TestExponentNeedsMantissa(t *testing.T) {
	for _, s := range []string{"+e5", "-E1", "+.e5", "-.E1", "+e", "-e-5", "+e+5", "e5"} {
		if r := Core([]byte(s)); r.Kind != String {
			t.Errorf("Core(%q) = %v, want string", s, r.Kind)
		}
	}
	for s, f := range map[string]float64{"0e5": 0, "+0e5": 0, ".5e1": 5, "1e+2": 100, "-1E1": -10} {
		if r := Core([]byte(s)); r.Kind != Float || r.F != f {
			t.Errorf("Core(%q) = %v %v, want float %v", s, r.Kind, r.F, f)
		}
	}
}

// strconv.ParseInt with base 0 takes a sign after the radix prefix, so readers
// built on it read these as integers; none may be written plain.
func TestSignAfterRadixPrefix(t *testing.T) {
	for _, s := range []string{"0b-1", "0b+1", "0o-7", "0o+7", "0x-1", "0x+F", "0b-1_0", "-0b-1", "+0o+7", "0B-1", "0O+7", "0X-f"} {
		if ReadsAsString([]byte(s)) || ReadsAsStringS(s) {
			t.Errorf("%q is read as an integer by some readers but ReadsAsString says it is safe", s)
		}
	}
	for _, s := range []string{"0b-", "0b+", "0x--1", "0b-g", "0o-", "0b"} {
		if !ReadsAsString([]byte(s)) {
			t.Errorf("%q should stay plain", s)
		}
	}
}

// Plain-safety classes beyond the schemas: underscores that vanish before a
// number is parsed, timestamps with one-digit fields, and the words and
// hexadecimal forms that strconv.ParseFloat accepts.
func TestPlainSafetyClasses(t *testing.T) {
	quoted := []string{
		"_1", "_.5", "_0x1", "1_", "+_1", "1e3_", "0_x123", "1_0.5",
		"2001-1-1 1:2:3", "2001-1-1T1:2:3", "2001-1-1t1:2:3.5", "2001-1-1 \t1:2:3", "2001-1-1 1:2:3.5 Z",
		"2001-12-14 21:59:43.10 -5", "2001-12-14t21:59:43.10-05:00",
		"inf", "Inf", "INF", "+inf", "-Infinity", "infinity", "nan", "NaN", "i_nf",
		"0x1p-2", "0x1.8p3", "0X1P3", "-0x1p0", "0x_1p2",
		"<<", "=",
	}
	for _, s := range quoted {
		if ReadsAsString([]byte(s)) || ReadsAsStringS(s) {
			t.Errorf("%q must not be written plain", s)
		}
	}
	plain := []string{
		"_", "__", "_a", "_id", "a_b", "info", "Informal", "index", "nano", "name", "nancy", "+nan", "in", "nan0", "infinit",
		"0x1p", "1_a", "2001-1-1 1:2", "2001-1-1 1:2:x", "2001-1-1x", "x2001-1-1", "<<<", "=x",
	}
	for _, s := range plain {
		if !ReadsAsString([]byte(s)) || !ReadsAsStringS(s) {
			t.Errorf("%q should stay plain", s)
		}
	}
}
