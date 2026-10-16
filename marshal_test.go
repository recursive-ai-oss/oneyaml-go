package oneyaml_test

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"

	"oneyaml.dev/go"
)

type inner struct {
	A int `yaml:"a"`
	B string
}

type Embedded struct {
	E1 int
	E2 string `json:"e2name"`
}

type sample struct {
	Name    string            `yaml:"name"`
	Count   int               `yaml:"count,omitempty"`
	Tags    []string          `yaml:"tags,omitempty"`
	Labels  map[string]string `yaml:"labels,omitempty"`
	Inner   inner             `yaml:"inner"`
	Ptr     *inner            `yaml:"ptr"`
	Flow    []int             `yaml:"flow,flow"`
	Skip    string            `yaml:"-"`
	Default string
	JSONTag string `json:"json_tag,omitempty"`
	Embedded
	Extra map[string]any `yaml:",inline"`
	priv  int
}

func mustMarshal(t *testing.T, v any, opts ...oneyaml.Option) string {
	t.Helper()
	b, err := oneyaml.Marshal(v, opts...)
	if err != nil {
		t.Fatalf("Marshal(%#v): %v", v, err)
	}
	return string(b)
}

func TestMarshalScalars(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{nil, "null\n"},
		{true, "true\n"},
		{false, "false\n"},
		{42, "42\n"},
		{int8(-3), "-3\n"},
		{uint64(math.MaxUint64), "18446744073709551615\n"},
		{1.5, "1.5\n"},
		{3.0, "3.0\n"},
		{1e21, "1.0e+21\n"},
		{1e-7, "1.0e-07\n"},
		{1.5e-7, "1.5e-07\n"},
		{123456789.0, "123456789.0\n"},
		{-0.25, "-0.25\n"},
		{float32(0.1), "0.1\n"},
		{math.Inf(1), ".inf\n"},
		{math.Inf(-1), "-.inf\n"},
		{math.NaN(), ".nan\n"},
		{"hello", "hello\n"},
		{"", "''\n"},
		{"it's", "it's\n"},
		{" lead", "' lead'\n"},
		{"trail ", "'trail '\n"},
		{"a: b", "'a: b'\n"},
		{"a #b", "'a #b'\n"},
		{"a#b", "a#b\n"},
		{"#x", "'#x'\n"},
		{"- x", "'- x'\n"},
		{"-x", "-x\n"},
		{"x:", "'x:'\n"},
		{"it's: ok", "'it''s: ok'\n"},
		{"---", "'---'\n"},
		{"@at", "'@at'\n"},
		{"tab\there", "'tab\there'\n"},
		{"line\nbreak", "|-\n  line\n  break\n"},
		{"line\nbreak\n", "|\n  line\n  break\n"},
		{"line\nbreak\n\n\n", "|+\n  line\n  break\n\n\n"},
		{"  indented\nbody\n", `"  indented\nbody\n"` + "\n"},
		{"\nstarts with newline", `"\nstarts with newline"` + "\n"},
		{"a\n  \nb", `"a\n  \nb"` + "\n"},
		{"cr\r\nlf", `"cr\r\nlf"` + "\n"},
		{"bell\a", `"bell\a"` + "\n"},
		{"nel\u0085", `"nel\N"` + "\n"},
		{"sep\u2028", `"sep\L"` + "\n"},
		{"héllo ✓", "héllo ✓\n"},
		{"yes", "'yes'\n"},
		{"No", "'No'\n"},
		{"on", "'on'\n"},
		{"y", "'y'\n"},
		{"null", "'null'\n"},
		{"~", "'~'\n"},
		{"true", "'true'\n"},
		{"123", "'123'\n"},
		{"0755", "'0755'\n"},
		{"0x1F", "'0x1F'\n"},
		{"1_000", "'1_000'\n"},
		{"1:30", "'1:30'\n"},
		{"1e3", "'1e3'\n"},
		{".5", "'.5'\n"},
		{".inf", "'.inf'\n"},
		{"<<", "'<<'\n"},
		{"2001-12-14", "'2001-12-14'\n"},
		{"v1.2.3", "v1.2.3\n"},
		{"1.2.3", "1.2.3\n"},
		{[]byte("hi"), "!!binary aGk=\n"},
		{[]byte{}, "!!binary \"\"\n"},
		{big.NewInt(0).Lsh(big.NewInt(1), 100), "1267650600228229401496703205376\n"},
		{time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC), "2024-05-06T07:08:09Z\n"},
		{90 * time.Second, "1m30s\n"},
		{"bad\xffutf8", "!!binary YmFkL3V0Zjg=\n"},
	}
	for _, tc := range tests {
		got := mustMarshal(t, tc.in)
		if tc.in == "bad\xffutf8" {
			tc.want = "!!binary YmFk/3V0Zjg=\n"
		}
		if got != tc.want {
			t.Errorf("Marshal(%#v)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestLiteralIndicator(t *testing.T) {
	got := mustMarshal(t, map[string]any{"k": "  indented\nbody\n", "l": []any{"  a\nb"}})
	want := "k: |2\n    indented\n  body\nl:\n  - |2-\n      a\n    b\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMarshalCollections(t *testing.T) {
	v := map[string]any{
		"b": []any{1, "two", []any{3, 4}, map[string]any{"k": "v", "z": nil}},
		"a": map[string]any{},
		"c": []any{},
		"d": map[string]any{"nested": map[string]any{"deep": []any{map[string]any{"x": 1, "w": 2}}}},
		"e": []any{[]any{}, map[string]any{}},
	}
	want := `a: {}
b:
  - 1
  - two
  - - 3
    - 4
  - k: v
    z: null
c: []
d:
  nested:
    deep:
      - w: 2
        x: 1
e:
  - []
  - {}
`
	if got := mustMarshal(t, v); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	want4 := strings.ReplaceAll(strings.ReplaceAll(want, "  nested", "    nested"), "    deep", "        deep")
	_ = want4
	got := mustMarshal(t, map[string]any{"a": map[string]any{"b": []any{1}}}, oneyaml.Indent(4))
	if w := "a:\n    b:\n        - 1\n"; got != w {
		t.Errorf("Indent(4): got %q want %q", got, w)
	}
	if got := mustMarshal(t, []any{map[string]any{"a": 1}, []string{"x"}}); got != "- a: 1\n- - x\n" {
		t.Errorf("root sequence: %q", got)
	}
}

func TestMarshalStruct(t *testing.T) {
	_ = sample{}.priv // unexported fields are never written
	s := sample{
		Name:     "nm",
		Inner:    inner{A: 1, B: "b"},
		Flow:     []int{1, 2, 3},
		Default:  "d",
		JSONTag:  "j",
		Embedded: Embedded{E1: 5, E2: "x"},
		Extra:    map[string]any{"zz": 1, "aa": []any{true}},
	}
	want := `name: nm
inner:
  a: 1
  b: b
ptr: null
flow: [1, 2, 3]
default: d
json_tag: j
e1: 5
e2name: x
aa:
  - true
zz: 1
`
	if got := mustMarshal(t, s); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	s.Count = 3
	s.Tags = []string{"a", "b"}
	s.Labels = map[string]string{"l": "1"}
	s.Ptr = &inner{A: 2}
	got := mustMarshal(t, &s)
	for _, frag := range []string{"count: 3\n", "tags:\n  - a\n  - b\n", "labels:\n  l: '1'\n", "ptr:\n  a: 2\n  b: ''\n"} {
		if !strings.Contains(got, frag) {
			t.Errorf("output lacks %q:\n%s", frag, got)
		}
	}
}

type dominant struct {
	X int `yaml:"x"`
	Inner2
	Inner3
}
type Inner2 struct{ Y, Z int }
type Inner3 struct {
	Y int // clashes with Inner2.Y: dropped
	W int `yaml:"w"`
}

func TestEmbeddedRules(t *testing.T) {
	got := mustMarshal(t, dominant{X: 1, Inner2: Inner2{Y: 2, Z: 3}, Inner3: Inner3{Y: 4, W: 5}})
	if want := "x: 1\nz: 3\nw: 5\n"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	type ptrEmb struct {
		*Inner2
		A int
	}
	if got := mustMarshal(t, ptrEmb{A: 1}); got != "a: 1\n" {
		t.Errorf("nil embedded pointer: %q", got)
	}
	if got := mustMarshal(t, ptrEmb{Inner2: &Inner2{Y: 1, Z: 2}, A: 3}); got != "'y': 1\nz: 2\na: 3\n" {
		t.Errorf("embedded pointer: %q", got)
	}
}

type hookT struct{ n int }

func (h hookT) MarshalYAML() (any, error) { return map[string]any{"nn": h.n}, nil }

type ptrHook struct{ N int }

func (h *ptrHook) MarshalYAML() (any, error) { return fmt.Sprintf("ptr%d", h.N), nil }

type textT int

func (t textT) MarshalText() ([]byte, error) { return []byte(fmt.Sprintf("text-%d", int(t))), nil }

type failT struct{}

func (failT) MarshalYAML() (any, error) { return nil, errors.New("boom") }

type zeroT struct{ v int }

func (z zeroT) IsZero() bool { return z.v == 0 }

type level int

func (l level) String() string { return "ignored" }

func TestHooks(t *testing.T) {
	if got := mustMarshal(t, []any{hookT{1}, &hookT{2}, textT(3), level(4)}); got != "- nn: 1\n- nn: 2\n- text-3\n- 4\n" {
		t.Errorf("hooks: %q", got)
	}
	var np *hookT
	if got := mustMarshal(t, struct{ P *hookT }{np}); got != "p: null\n" {
		t.Errorf("nil pointer with value receiver hook: %q", got)
	}
	// A pointer-receiver hook runs only when the value is addressable.
	if got := mustMarshal(t, &struct{ H ptrHook }{ptrHook{7}}); got != "h: ptr7\n" {
		t.Errorf("addressable ptr hook: %q", got)
	}
	if got := mustMarshal(t, struct{ H ptrHook }{ptrHook{7}}); got != "h:\n  'n': 7\n" {
		t.Errorf("unaddressable ptr hook should be skipped: %q", got)
	}
	if got := mustMarshal(t, map[textT]int{2: 1, 1: 2}); got != "text-1: 2\ntext-2: 1\n" {
		t.Errorf("text keys: %q", got)
	}
	got := mustMarshal(t, struct {
		Z  zeroT     `yaml:"z,omitempty"`
		Z2 zeroT     `yaml:"z2,omitzero"`
		T  time.Time `yaml:"t,omitzero"`
		O  zeroT     `yaml:"o,omitempty"`
	}{O: zeroT{1}})
	if got != "o:\n  {}\n" && got != "o: {}\n" {
		t.Errorf("zero detection: %q", got)
	}
}

func TestErrors(t *testing.T) {
	_, err := oneyaml.Marshal(failT{})
	var te *oneyaml.TypeError
	if !errors.As(err, &te) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("hook error: %v", err)
	}
	if !strings.Contains(te.Error(), "boom") || te.Unwrap() == nil {
		t.Errorf("hook error lost: %v", te)
	}
	_, err = oneyaml.Marshal(map[string]any{"a": []any{1, make(chan int)}})
	if !errors.As(err, &te) || te.Path != "/a/1" || !strings.Contains(te.GoType.String(), "chan") {
		t.Errorf("chan error: %#v (%v)", te, err)
	}
	_, err = oneyaml.Marshal(complex(1, 2))
	if !errors.As(err, &te) {
		t.Errorf("complex: %v", err)
	}
	_, err = oneyaml.Marshal(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC))
	if !errors.As(err, &te) {
		t.Errorf("year: %v", err)
	}
	type dupInline struct {
		A     int            `yaml:"a"`
		Extra map[string]any `yaml:",inline"`
	}
	_, err = oneyaml.Marshal(dupInline{Extra: map[string]any{"a": 1}})
	if !errors.As(err, &te) {
		t.Errorf("inline duplicate: %v", err)
	}
}

func TestCyclesAndDepth(t *testing.T) {
	m := map[string]any{}
	m["self"] = m
	_, err := oneyaml.Marshal(m)
	var le *oneyaml.LimitError
	var te *oneyaml.TypeError
	if err == nil || (!errors.As(err, &le) && !errors.As(err, &te)) {
		t.Errorf("map cycle with default limits: %v", err)
	}
	// Without limits a cycle is still an error, not a stack overflow.
	_, err = oneyaml.Marshal(m, oneyaml.NoLimits())
	if !errors.As(err, &te) || !strings.Contains(err.Error(), "itself") {
		t.Errorf("map cycle without limits: %v", err)
	}
	type node struct {
		Next *node
		V    int
	}
	n := &node{V: 1}
	n.Next = n
	if _, err = oneyaml.Marshal(n, oneyaml.NoLimits()); !errors.As(err, &te) {
		t.Errorf("pointer cycle: %v", err)
	}
	s := []any{nil}
	s[0] = s
	if _, err = oneyaml.Marshal(s, oneyaml.NoLimits()); !errors.As(err, &te) {
		t.Errorf("slice cycle: %v", err)
	}
	// Deep but finite data is fine within the limit, and refused past it.
	var deep any = "x"
	for i := 0; i < 500; i++ {
		deep = []any{deep}
	}
	if _, err = oneyaml.Marshal(deep); err != nil {
		t.Errorf("depth 500: %v", err)
	}
	if _, err = oneyaml.Marshal(deep, oneyaml.Limits(oneyaml.LimitSet{MaxDepth: 100})); !errors.As(err, &le) {
		t.Errorf("depth limit 100: %v", err)
	}
}

type selfT struct{}

func (s selfT) MarshalYAML() (any, error) { return s, nil }

func TestHookThatReturnsItself(t *testing.T) {
	for _, opts := range [][]oneyaml.Option{nil, {oneyaml.NoLimits()}} {
		_, err := oneyaml.Marshal(selfT{}, opts...)
		var le *oneyaml.LimitError
		if !errors.As(err, &le) || le.Max == 0 {
			t.Errorf("got %v", err)
		}
	}
}

func TestComplexKeys(t *testing.T) {
	got := mustMarshal(t, map[any]any{
		"b":                       1,
		2:                         "two",
		1.5:                       "f",
		true:                      "t",
		nil:                       "nn",
		[2]int{1, 2}:              "arr",
		strings.Repeat("k", 1100): "long",
	})
	if !strings.HasPrefix(got, "null: nn\ntrue: t\n2: two\n1.5: f\nb: 1\n") {
		t.Errorf("key order/forms:\n%s", got)
	}
	if !strings.Contains(got, "? - 1\n  - 2\n: arr\n") {
		t.Errorf("explicit key form:\n%s", got)
	}
	if !strings.Contains(got, "? "+strings.Repeat("k", 1100)+"\n: long\n") {
		t.Errorf("long key:\n%s", got)
	}
}

func TestDeterministic(t *testing.T) {
	m := map[string]any{}
	for i := 0; i < 200; i++ {
		m[fmt.Sprintf("k%03d", (i*37)%200)] = i
	}
	first := mustMarshal(t, m)
	for i := 0; i < 5; i++ {
		if got := mustMarshal(t, m); got != first {
			t.Fatal("output differs between runs")
		}
	}
	if !strings.HasPrefix(first, "k000: ") {
		t.Errorf("not sorted: %s", first[:20])
	}
}

func TestEncoderStream(t *testing.T) {
	var buf bytes.Buffer
	enc := oneyaml.NewEncoder(&buf, oneyaml.Indent(2))
	if err := enc.Encode(map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := enc.Encode("two"); err != nil {
		t.Fatal(err)
	}
	if err := enc.Encode(make(chan int)); err == nil {
		t.Fatal("expected error")
	}
	if err := enc.Encode([]int{3}); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if want := "a: 1\n---\ntwo\n---\n- 3\n"; buf.String() != want {
		t.Errorf("got %q want %q", buf.String(), want)
	}
	if enc.Encode(1) == nil {
		t.Error("Encode after Close succeeded")
	}
}

func TestOptionsAreSafe(t *testing.T) {
	// Options for reading are accepted and ignored by the writer; nil is skipped.
	got := mustMarshal(t, 1, oneyaml.KnownFields(), oneyaml.MergeKeys(), oneyaml.Untrusted(), nil, oneyaml.Indent(-5), oneyaml.Indent(100))
	if got != "1\n" {
		t.Errorf("got %q", got)
	}
	if got := mustMarshal(t, map[string]any{"a": []int{1}}, oneyaml.Indent(100)); got != "a:\n         - 1\n" {
		t.Errorf("indent clamp: %q", got)
	}
}

func TestPlainStringsAreStable(t *testing.T) {
	// Characters that are always safe plain should never be quoted needlessly.
	for _, s := range []string{"nginx-ingress", "registry.k8s.io/ingress-nginx/controller", "a b c", "key=value", "50%", "a,b", "x[1]", "https://example.com/a?b=c", "$VAR", "foo@bar"} {
		if got := mustMarshal(t, s); got != s+"\n" {
			t.Errorf("%q written as %q", s, got)
		}
	}
}
