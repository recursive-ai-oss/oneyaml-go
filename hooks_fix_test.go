package oneyaml_test

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
)

func TestMarshalStaleNodeIsAnError(t *testing.T) {
	doc, err := oneyaml.Parse([]byte("a: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	var n oneyaml.Node
	if err := doc.Get("/a", &n); err != nil {
		t.Fatal(err)
	}
	if err := doc.Set("/a", 2); err != nil {
		t.Fatal(err)
	}
	out, err := oneyaml.Marshal(struct {
		N oneyaml.Node `yaml:"x"`
	}{n})
	if !errors.Is(err, oneyaml.ErrStaleNode) {
		t.Fatalf("got %q %v", out, err)
	}
	// The zero Node is still null.
	out, err = oneyaml.Marshal(struct {
		N oneyaml.Node `yaml:"x"`
	}{})
	if err != nil || string(out) != "x: null\n" {
		t.Fatalf("zero Node: %q %v", out, err)
	}
}

// rawNum writes the JSON text it holds.
type rawNum string

func (r rawNum) MarshalJSON() ([]byte, error) { return []byte(r), nil }

func TestJSONMarshalKeepsNumberText(t *testing.T) {
	cases := []string{
		"18446744073709551616",
		"1180591620717411303424",
		"0.1000000000000000055511151231257827",
		"-9223372036854775809",
		"1E400",
		"1.0",
		"-0",
		"12345678901234567890123456789e-5",
		"7",
	}
	for _, text := range cases {
		out, err := oneyaml.Marshal(map[string]any{"num": rawNum(text), "list": []any{rawNum(text)}})
		if err != nil {
			t.Errorf("%s: %v", text, err)
			continue
		}
		want := "list:\n  - " + text + "\nnum: " + text + "\n"
		if string(out) != want {
			t.Errorf("%s: got %q", text, out)
		}
		// Read back: the scalar has the same digits.
		doc, err := oneyaml.Parse(out)
		if err != nil {
			t.Errorf("%s: reparse: %v", text, err)
			continue
		}
		var n oneyaml.Node
		if err := doc.Get("/num", &n); err != nil || n.Value() != text {
			t.Errorf("%s: read back %q %v", text, n.Value(), err)
		}
	}
	// An integer too big for int64 reads back as the same integer.
	out, _ := oneyaml.Marshal(rawNum("1180591620717411303424"))
	var x any
	if err := oneyaml.Unmarshal(out, &x); err != nil {
		t.Fatal(err)
	}
	if b, ok := x.(*big.Int); !ok || b.String() != "1180591620717411303424" {
		t.Fatalf("read back %T %v", x, x)
	}
}

func TestJSONMarshalNumberDigitLimit(t *testing.T) {
	big := rawNum(strings.Repeat("9", 50))
	_, err := oneyaml.Marshal(big, oneyaml.Limits(oneyaml.LimitSet{MaxNumberDigits: 10}))
	var le *oneyaml.LimitError
	if !errors.As(err, &le) || le.Limit != oneyaml.LimitMaxNumberDigits || le.Max != 10 {
		t.Fatalf("got %v", err)
	}
	if _, err := oneyaml.Marshal(big, oneyaml.NoLimits()); err != nil {
		t.Fatalf("NoLimits: %v", err)
	}
	if _, err := oneyaml.Marshal(big); err != nil {
		t.Fatalf("default: %v", err)
	}
}

// bombSource has ten fields that each read the same 100-item list through an
// alias: one read is far under the limit, the ten together are over it.
func bombSource() string {
	var b strings.Builder
	b.WriteString("a: &a [" + strings.TrimSuffix(strings.Repeat("x, ", 100), ", ") + "]\n")
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&b, "f%d: [*a]\n", i)
	}
	return b.String()
}

type anyFunc struct{}

func (*anyFunc) UnmarshalYAML(unmarshal func(any) error) error {
	var x any
	return unmarshal(&x)
}

type anyJSON struct{}

func (*anyJSON) UnmarshalJSON([]byte) error { return nil }

func TestHooksShareOneAliasBudget(t *testing.T) {
	limits := oneyaml.Limits(oneyaml.LimitSet{MaxAliasNodes: 450})
	src := []byte(bombSource())

	type fn struct {
		A                                       any
		F1, F2, F3, F4, F5, F6, F7, F8, F9, F10 anyFunc
	}
	type js struct {
		A                                       any
		F1, F2, F3, F4, F5, F6, F7, F8, F9, F10 anyJSON
	}
	for name, target := range map[string]any{"func": &fn{}, "json": &js{}} {
		// One field alone is under the limit.
		one := []byte(strings.Join(strings.SplitAfter(string(src), "\n")[:3], ""))
		if err := oneyaml.Unmarshal(one, target, limits); err != nil {
			t.Errorf("%s: two reads should pass: %v", name, err)
		}
		// All ten are over it, and it is one budget for the call.
		err := oneyaml.Unmarshal(src, target, limits)
		var le *oneyaml.LimitError
		if !errors.As(err, &le) {
			t.Errorf("%s: want *LimitError, got %T %v", name, err, err)
		}
		// The same through a Document.
		doc, perr := oneyaml.Parse(src, limits)
		if perr != nil {
			t.Fatal(perr)
		}
		if err := doc.Decode(target); !errors.As(err, &le) {
			t.Errorf("%s: Document.Decode: want *LimitError, got %T %v", name, err, err)
		}
	}
}

// failInside decodes its value into an int, which fails for text.
type failInside struct{}

func (*failInside) UnmarshalYAML(unmarshal func(any) error) error {
	var n struct{ X int }
	return unmarshal(&n)
}

func TestHookErrorIsReportedOnce(t *testing.T) {
	src := "a: 1\nv:\n  x: text\n"
	var s struct {
		A int        `yaml:"a"`
		V failInside `yaml:"v"`
	}
	err := oneyaml.Unmarshal([]byte(src), &s)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) {
		t.Fatalf("got %T %v", err, err)
	}
	if n := strings.Count(err.Error(), "oneyaml:"); n != 1 {
		t.Fatalf("prefix repeated %d times: %v", n, err)
	}
	if te.Path != "/v/x" || te.Line == 0 || strings.Count(err.Error(), "line ") != 1 {
		t.Fatalf("got %+v / %v", te, err)
	}
}
