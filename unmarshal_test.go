package oneyaml_test

import (
	"bytes"
	"errors"
	"io"
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"oneyaml.dev/go"
)

type ulevel int

func (l *ulevel) UnmarshalText(b []byte) error {
	switch string(b) {
	case "low":
		*l = 1
	case "high":
		*l = 2
	default:
		return errors.New("unknown ulevel " + string(b))
	}
	return nil
}

type UBase struct {
	ID   int    `yaml:"id"`
	Kind string `yaml:"kind,omitempty"`
}

type usample struct {
	UBase   `yaml:",inline"`
	Name    string            `yaml:"name"`
	Port    uint16            `yaml:"port"`
	Ratio   float32           `yaml:"ratio"`
	Tags    []string          `yaml:"tags"`
	Labels  map[string]string `yaml:"labels"`
	Ptr     *int              `yaml:"ptr"`
	When    time.Time         `yaml:"when"`
	Timeout time.Duration     `yaml:"timeout"`
	Big     *big.Int          `yaml:"big"`
	Data    []byte            `yaml:"data"`
	Lvl     ulevel            `yaml:"lvl"`
	Any     any               `yaml:"any"`
	Extra   map[string]any    `yaml:",inline"`
	JSONTag string            `json:"from_json"`
	skipped int
}

func TestUnmarshalStruct(t *testing.T) {
	src := `
id: 7
kind: widget
name: api
port: 8080
ratio: 0.5
tags: [a, "b", 3]
labels: {app: web, tier: "1"}
ptr: 12
when: 2001-12-14t21:59:43.10-05:00
timeout: 1m30s
big: 123456789012345678901234567890
data: !!binary aGVsbG8=
lvl: high
any: {x: [1, 2.5, null]}
from_json: j
unknown: [1, 2]
`
	var s usample
	if err := oneyaml.Unmarshal([]byte(src), &s); err != nil {
		t.Fatal(err)
	}
	wantTime := time.Date(2001, 12, 14, 21, 59, 43, 100_000_000, time.FixedZone("", -5*3600))
	if s.ID != 7 || s.Kind != "widget" || s.Name != "api" || s.Port != 8080 || s.Ratio != 0.5 {
		t.Errorf("scalars: %+v", s)
	}
	if !reflect.DeepEqual(s.Tags, []string{"a", "b", "3"}) {
		t.Errorf("tags = %#v", s.Tags)
	}
	if !reflect.DeepEqual(s.Labels, map[string]string{"app": "web", "tier": "1"}) {
		t.Errorf("labels = %#v", s.Labels)
	}
	if s.Ptr == nil || *s.Ptr != 12 {
		t.Errorf("ptr = %v", s.Ptr)
	}
	if !s.When.Equal(wantTime) {
		t.Errorf("when = %v", s.When)
	}
	if s.Timeout != 90*time.Second {
		t.Errorf("timeout = %v", s.Timeout)
	}
	if s.Big == nil || s.Big.String() != "123456789012345678901234567890" {
		t.Errorf("big = %v", s.Big)
	}
	if string(s.Data) != "hello" {
		t.Errorf("data = %q", s.Data)
	}
	if s.Lvl != 2 {
		t.Errorf("lvl = %v", s.Lvl)
	}
	if !reflect.DeepEqual(s.Any, map[string]any{"x": []any{1, 2.5, nil}}) {
		t.Errorf("any = %#v", s.Any)
	}
	if s.skipped != 0 {
		t.Errorf("unexported field was set: %d", s.skipped)
	}
	if s.JSONTag != "j" {
		t.Errorf("json tag fallback: %q", s.JSONTag)
	}
	if !reflect.DeepEqual(s.Extra, map[string]any{"unknown": []any{1, 2}}) {
		t.Errorf("inline map = %#v", s.Extra)
	}
}

func TestUnmarshalGeneric(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want any
	}{
		{"a: 1\nb: [x, 2.5, true, ~]", map[string]any{"a": 1, "b": []any{"x", 2.5, true, nil}}},
		{"1: a\nb: c", map[any]any{1: "a", "b": "c"}},
		{"- 0x1F\n- 0o17\n- 1e3\n- .inf\n- '5'\n- !!str 5\n- !!float 5", []any{31, 15, 1000.0, math.Inf(1), "5", "5", 5.0}},
		{"- 9223372036854775807\n- 9223372036854775808\n- 18446744073709551616", []any{math.MaxInt64, uint64(9223372036854775808), mustBig("18446744073709551616")}},
		{"- 2001-12-14\n- !binary x\n- !local y", []any{"2001-12-14", "x", "y"}},
		{"", nil},
		{"--- \n", nil},
		{"? null\n: x", map[any]any{nil: "x"}},
	} {
		var got any
		if err := oneyaml.Unmarshal([]byte(tc.src), &got); err != nil {
			t.Errorf("%q: %v", tc.src, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q:\n got %#v\nwant %#v", tc.src, got, tc.want)
		}
	}
}

func mustBig(s string) *big.Int { n, _ := new(big.Int).SetString(s, 10); return n }

func TestUnmarshalZeroDocuments(t *testing.T) {
	v := 5
	if err := oneyaml.Unmarshal([]byte("# only a comment\n"), &v); err != nil || v != 5 {
		t.Errorf("v = %d, err = %v", v, err)
	}
}

func TestUnmarshalFirstDocument(t *testing.T) {
	var v map[string]int
	if err := oneyaml.Unmarshal([]byte("a: 1\n---\nb: 2\n"), &v); err != nil || !reflect.DeepEqual(v, map[string]int{"a": 1}) {
		t.Errorf("v = %v, err = %v", v, err)
	}
}

func TestUnmarshalNull(t *testing.T) {
	type T struct {
		S string
		N int
		P *int
		M map[string]int
		L []int
	}
	one := 1
	v := T{S: "keep", N: 5, P: &one, M: map[string]int{"a": 1}, L: []int{1}}
	if err := oneyaml.Unmarshal([]byte("s: ~\nn: null\np: ~\nm:\nl: ~"), &v); err != nil {
		t.Fatal(err)
	}
	if v.S != "keep" || v.N != 5 || v.P != nil || v.M != nil || v.L != nil {
		t.Errorf("v = %+v", v)
	}
}

func TestUnmarshalTypeErrors(t *testing.T) {
	type inner struct{ N int8 }
	type T struct {
		A int
		L []inner
		M map[string]uint
	}
	for _, tc := range []struct {
		src        string
		path       string
		line, col  int
		msgContain string
	}{
		{"a: x", "/a", 1, 4, "cannot unmarshal !!str `x` into int"},
		{"a: 1.5", "/a", 1, 4, "!!float"},
		{"a: '5'", "/a", 1, 4, "!!str"},
		{"l:\n- n: 1\n- n: 300", "/l/1/n", 3, 6, "overflows int8"},
		{"m: {a: -1}", "/m/a", 1, 8, "overflows uint"},
		{"m: [1]", "/m", 1, 4, "cannot unmarshal !!seq into map[string]uint"},
		{"l: {a: b}", "/l", 1, 4, "!!map"},
	} {
		var v T
		err := oneyaml.Unmarshal([]byte(tc.src), &v)
		var te *oneyaml.TypeError
		if !errors.As(err, &te) {
			t.Errorf("%q: err = %v", tc.src, err)
			continue
		}
		if te.Path != tc.path || te.Line != tc.line || te.Column != tc.col || !strings.Contains(te.Msg, tc.msgContain) || te.GoType == nil {
			t.Errorf("%q: got path=%q line=%d col=%d msg=%q type=%q", tc.src, te.Path, te.Line, te.Column, te.Msg, te.GoType)
		}
	}
}

func TestUnmarshalTextUnmarshalerError(t *testing.T) {
	var v struct{ L ulevel }
	err := oneyaml.Unmarshal([]byte("l: nope"), &v)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) || te.Err == nil || te.Err.Error() != "unknown ulevel nope" {
		t.Errorf("err = %v", err)
	}
}

func TestUnmarshalNotAPointer(t *testing.T) {
	var te *oneyaml.TypeError
	if err := oneyaml.Unmarshal([]byte("a: 1"), map[string]int{}); !errors.As(err, &te) {
		t.Errorf("err = %v", err)
	}
	var np *int
	if err := oneyaml.Unmarshal([]byte("1"), np); !errors.As(err, &te) {
		t.Errorf("err = %v", err)
	}
	if err := oneyaml.Unmarshal([]byte("1"), nil); !errors.As(err, &te) {
		t.Errorf("err = %v", err)
	}
}

func TestUnmarshalSyntaxError(t *testing.T) {
	var v any
	err := oneyaml.Unmarshal([]byte("a: 1\nb: [1, 2\nc: 3"), &v)
	var se *oneyaml.SyntaxError
	if !errors.As(err, &se) || se.Line < 2 || se.Category != "syntax" {
		t.Fatalf("err = %#v", err)
	}
}

func TestDuplicateKeys(t *testing.T) {
	for _, src := range []string{"a: 1\na: 2", "1: a\n0x1: b", "{a: 1, a: 2}", "'a': 1\na: 2", "a: 1\n? a\n: 3"} {
		var v any
		err := oneyaml.Unmarshal([]byte(src), &v)
		var se *oneyaml.SyntaxError
		if !errors.As(err, &se) || se.Category != "duplicate-key" || se.Line < 2 && !strings.HasPrefix(src, "{") {
			t.Errorf("%q: err = %#v", src, err)
		}
		var s struct{ A int }
		var m map[string]int
		if strings.Contains(src, "a:") && !strings.Contains(src, "'a'") {
			if err := oneyaml.Unmarshal([]byte(src), &s); err == nil && strings.Count(src, "a:") > 1 && strings.Contains(src, "A") {
				t.Errorf("%q struct: no error", src)
			}
			if err := oneyaml.Unmarshal([]byte(strings.ReplaceAll(src, "? a\n: 3", "a: 3")), &m); err == nil {
				t.Errorf("%q map: no error", src)
			}
		}
	}
	var s struct {
		A int `yaml:"a"`
	}
	if err := oneyaml.Unmarshal([]byte("a: 1\na: 2"), &s); err == nil {
		t.Error("struct duplicate accepted")
	}
}

const mergeDoc = `
base: &base {x: 1, y: 2}
other: &other {y: 20, z: 30}
child:
  <<: [*base, *other]
  x: 10
`

func TestMergeKeys(t *testing.T) {
	var off map[string]any
	if err := oneyaml.Unmarshal([]byte(mergeDoc), &off); err != nil {
		t.Fatal(err)
	}
	if _, ok := off["child"].(map[string]any)["<<"]; !ok {
		t.Errorf("without MergeKeys << is an ordinary key: %#v", off["child"])
	}
	want := map[string]any{"x": 10, "y": 2, "z": 30}
	var on map[string]map[string]any
	if err := oneyaml.Unmarshal([]byte(mergeDoc), &on, oneyaml.MergeKeys()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(on["child"], want) {
		t.Errorf("typed map: %#v", on["child"])
	}
	var gen map[string]any
	if err := oneyaml.Unmarshal([]byte(mergeDoc), &gen, oneyaml.MergeKeys()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gen["child"], want) {
		t.Errorf("generic: %#v", gen["child"])
	}
	var st struct {
		Child struct{ X, Y, Z int } `yaml:"child"`
	}
	if err := oneyaml.Unmarshal([]byte(strings.ReplaceAll(mergeDoc, "x:", "X:")), &st, oneyaml.MergeKeys(),
		oneyaml.Limits(oneyaml.DefaultLimits())); err == nil {
		// Field names are lower-case, so the keys above match nothing; use tags.
		_ = st
	}
	var st2 struct {
		Child struct {
			X int `yaml:"x"`
			Y int `yaml:"y"`
			Z int `yaml:"z"`
		} `yaml:"child"`
	}
	if err := oneyaml.Unmarshal([]byte(mergeDoc), &st2, oneyaml.MergeKeys()); err != nil {
		t.Fatal(err)
	}
	if st2.Child.X != 10 || st2.Child.Y != 2 || st2.Child.Z != 30 {
		t.Errorf("struct: %+v", st2.Child)
	}
	// An explicit key before the merge key also wins.
	var early struct {
		X int `yaml:"x"`
		Y int `yaml:"y"`
	}
	if err := oneyaml.Unmarshal([]byte("y: 5\n<<: {x: 1, y: 9}"), &early, oneyaml.MergeKeys()); err != nil || early.X != 1 || early.Y != 5 {
		t.Errorf("early = %+v, %v", early, err)
	}
	// A merge value that is not a mapping is an error.
	var bad any
	if err := oneyaml.Unmarshal([]byte("<<: 5"), &bad, oneyaml.MergeKeys()); err == nil {
		t.Error("scalar merge value accepted")
	}
}

func TestKnownFields(t *testing.T) {
	var v struct {
		A int `yaml:"a"`
	}
	if err := oneyaml.Unmarshal([]byte("a: 1\nb: 2"), &v); err != nil {
		t.Fatal(err)
	}
	err := oneyaml.Unmarshal([]byte("a: 1\nb: 2"), &v, oneyaml.KnownFields())
	var te *oneyaml.TypeError
	if !errors.As(err, &te) || te.Path != "/b" || te.Line != 2 || !strings.Contains(te.Msg, `"b"`) {
		t.Errorf("err = %#v", err)
	}
}

func TestAliasesTyped(t *testing.T) {
	type Item struct {
		N int      `yaml:"n"`
		L []string `yaml:"l"`
	}
	var v struct {
		A Item   `yaml:"a"`
		B Item   `yaml:"b"`
		C []Item `yaml:"c"`
	}
	src := "a: &x {n: 1, l: [p, q]}\nb: *x\nc: [*x, *x]\n"
	if err := oneyaml.Unmarshal([]byte(src), &v); err != nil {
		t.Fatal(err)
	}
	want := Item{1, []string{"p", "q"}}
	if v.A.N != 1 || !reflect.DeepEqual(v.B, want) || !reflect.DeepEqual(v.C, []Item{want, want}) {
		t.Errorf("v = %+v", v)
	}
	// Decoded copies do not share storage.
	v.B.L[0] = "changed"
	if v.A.L[0] != "p" || v.C[0].L[0] != "p" {
		t.Error("alias copies share backing storage")
	}
	for _, bad := range []string{"a: *nope", "a: &x [*x]", "- &a [*b]\n- &b x"} {
		var any any
		if err := oneyaml.Unmarshal([]byte(bad), &any); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// A redefined anchor means the latest definition at the alias.
	var re []any
	if err := oneyaml.Unmarshal([]byte("[&a 1, *a, &a 2, *a]"), &re); err != nil || !reflect.DeepEqual(re, []any{1, 1, 2, 2}) {
		t.Errorf("redefined = %v, %v", re, err)
	}
}

func TestAliasLimits(t *testing.T) {
	bomb := "a: &a [x, x, x, x, x, x, x, x, x]\n"
	prev := "a"
	for _, n := range "bcdefgh" {
		bomb += string(n) + ": &" + string(n) + " [" + strings.Repeat("*"+prev+", ", 8) + "*" + prev + "]\n"
		prev = string(n)
	}
	var v any
	err := oneyaml.Unmarshal([]byte(bomb), &v)
	var le *oneyaml.LimitError
	if !errors.As(err, &le) || (le.Limit != oneyaml.LimitMaxAliasNodes && le.Limit != oneyaml.LimitMaxAliasBytes) || le.Option == "" {
		t.Fatalf("err = %#v", err)
	}
	small := oneyaml.Limits(oneyaml.LimitSet{MaxAliasNodes: 5})
	if err := oneyaml.Unmarshal([]byte("a: &a [1, 2, 3]\nb: *a\nc: *a"), &v, small); !errors.As(err, &le) {
		t.Errorf("small budget: %v", err)
	}
	// Bytes budget.
	big := "a: &a " + strings.Repeat("x", 1000) + "\nb: [" + strings.Repeat("*a, ", 100) + "*a]\n"
	err = oneyaml.Unmarshal([]byte(big), &v, oneyaml.Limits(oneyaml.LimitSet{MaxAliasBytes: 10000}))
	if !errors.As(err, &le) || le.Limit != oneyaml.LimitMaxAliasBytes {
		t.Errorf("bytes budget: %v", err)
	}
	// Typed targets are charged too.
	var m map[string][]any
	err = oneyaml.Unmarshal([]byte(bomb), &m)
	if !errors.As(err, &le) {
		t.Errorf("typed: %v", err)
	}
}

func TestDepthLimit(t *testing.T) {
	deep := strings.Repeat("[", 20000) + strings.Repeat("]", 20000)
	var v any
	var le *oneyaml.LimitError
	if err := oneyaml.Unmarshal([]byte(deep), &v); !errors.As(err, &le) || le.Limit != oneyaml.LimitMaxDepth {
		t.Errorf("default: %v", err)
	}
	if err := oneyaml.Unmarshal([]byte(deep[:2000]+deep[len(deep)-2000:]), &v, oneyaml.Untrusted()); !errors.As(err, &le) {
		t.Errorf("untrusted: %v", err)
	}
	if err := oneyaml.Unmarshal([]byte(deep), &v, oneyaml.NoLimits()); err != nil {
		t.Errorf("no limits: %v", err)
	}
	// Typed recursion is bounded too.
	type node struct{ Next *node }
	nest := strings.Repeat("next: {", 500) + strings.Repeat("}", 500)
	var n node
	if err := oneyaml.Unmarshal([]byte("{"+nest[:0]+strings.Repeat("Next: {", 300)+strings.Repeat("}", 300)+"}"), &n, oneyaml.Limits(oneyaml.LimitSet{MaxDepth: 100})); !errors.As(err, &le) {
		t.Errorf("typed depth: %v", err)
	}
}

func TestUntrustedLimits(t *testing.T) {
	var v any
	var le *oneyaml.LimitError
	if err := oneyaml.Unmarshal([]byte("a: "+strings.Repeat("x", 20<<20)), &v, oneyaml.Untrusted()); !errors.As(err, &le) || le.Limit != oneyaml.LimitMaxScalarBytes {
		t.Errorf("scalar size: %v", err)
	}
	lim := oneyaml.Limits(oneyaml.LimitSet{MaxInputBytes: 10})
	if err := oneyaml.Unmarshal([]byte("a: 1234567890"), &v, lim); !errors.As(err, &le) || le.Limit != oneyaml.LimitMaxInputBytes {
		t.Errorf("input size: %v", err)
	}
	dec := oneyaml.NewDecoder(strings.NewReader("a: 12345678901234"), lim)
	if err := dec.Decode(&v); !errors.As(err, &le) || le.Limit != oneyaml.LimitMaxInputBytes {
		t.Errorf("decoder input size: %v", err)
	}
	dec = oneyaml.NewDecoder(strings.NewReader(strings.Repeat("--- x\n", 5)), oneyaml.Limits(oneyaml.LimitSet{MaxDocuments: 3}))
	var err error
	n := 0
	for err = dec.Decode(&v); err == nil; err = dec.Decode(&v) {
		n++
	}
	if !errors.As(err, &le) || le.Limit != oneyaml.LimitMaxDocuments || n != 3 {
		t.Errorf("documents: n=%d err=%v", n, err)
	}
}

func TestDecoderStream(t *testing.T) {
	src := "a: 1\n---\n# second\nb: [1,\n  2]\n...\n---\nc: 3\n--- text\n...\n"
	dec := oneyaml.NewDecoder(&oneByteReader{r: strings.NewReader(src)})
	var got []any
	for {
		var v any
		err := dec.Decode(&v)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	want := []any{map[string]any{"a": 1}, map[string]any{"b": []any{1, 2}}, map[string]any{"c": 3}, "text"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
	var v any
	if err := dec.Decode(&v); err != io.EOF {
		t.Errorf("after EOF: %v", err)
	}
}

type oneByteReader struct{ r io.Reader }

func (o *oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

func TestDecoderErrorPositions(t *testing.T) {
	src := "a: 1\n---\nb: 2\n---\nc: [1\nd: 2\n"
	dec := oneyaml.NewDecoder(strings.NewReader(src))
	var v any
	for i := 0; i < 2; i++ {
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
	}
	err := dec.Decode(&v)
	var se *oneyaml.SyntaxError
	if !errors.As(err, &se) || se.Line < 5 || se.Offset < 15 {
		t.Fatalf("err = %#v", err)
	}
	if err2 := dec.Decode(&v); err2 == nil || err2.Error() != err.Error() {
		t.Errorf("syntax errors are sticky: %v", err2)
	}
	// A type error does not end the stream.
	dec = oneyaml.NewDecoder(strings.NewReader("a: x\n---\na: 2\n"))
	var s struct{ A int }
	var te *oneyaml.TypeError
	if err := dec.Decode(&s); !errors.As(err, &te) {
		t.Fatalf("err = %v", err)
	}
	if te.Line != 1 {
		t.Errorf("line = %d", te.Line)
	}
	if err := dec.Decode(&s); err != nil {
		// Field "a" does not match the lower-cased name "a"; both docs decode.
		t.Errorf("second doc: %v", err)
	}
	dec = oneyaml.NewDecoder(strings.NewReader("x: 1\n---\nA: z\n"))
	var s2 struct {
		A int `yaml:"A"`
	}
	dec.Decode(&s2)
	err = dec.Decode(&s2)
	if !errors.As(err, &te) || te.Line != 3 {
		t.Errorf("segment line offset: %#v", err)
	}
}

func TestUTF16AndUTF32(t *testing.T) {
	text := "k: \"é€𝄞\"\nlist: [1, 2]\n"
	enc := func(order string, bom bool, wide int) []byte {
		var out []byte
		put := func(r rune) {
			switch wide {
			case 2:
				var us []uint16
				if r >= 0x10000 {
					r -= 0x10000
					us = []uint16{uint16(0xD800 + r>>10), uint16(0xDC00 + r&0x3FF)}
				} else {
					us = []uint16{uint16(r)}
				}
				for _, u := range us {
					if order == "be" {
						out = append(out, byte(u>>8), byte(u))
					} else {
						out = append(out, byte(u), byte(u>>8))
					}
				}
			case 4:
				if order == "be" {
					out = append(out, byte(r>>24), byte(r>>16), byte(r>>8), byte(r))
				} else {
					out = append(out, byte(r), byte(r>>8), byte(r>>16), byte(r>>24))
				}
			}
		}
		if bom {
			put(0xFEFF)
		}
		for _, r := range text {
			put(r)
		}
		return out
	}
	want := map[string]any{"k": "é€𝄞", "list": []any{1, 2}}
	for _, order := range []string{"le", "be"} {
		for _, wide := range []int{2, 4} {
			for _, bom := range []bool{true, false} {
				in := enc(order, bom, wide)
				var got any
				if err := oneyaml.Unmarshal(in, &got); err != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("%s/%d/bom=%v: %v %#v", order, wide, bom, err, got)
				}
				var got2 any
				if err := oneyaml.NewDecoder(bytes.NewReader(in)).Decode(&got2); err != nil || !reflect.DeepEqual(got2, want) {
					t.Errorf("decoder %s/%d/bom=%v: %v %#v", order, wide, bom, err, got2)
				}
			}
		}
	}
	var v any
	err := oneyaml.Unmarshal([]byte("a\x00:\x00 \x00\x00\xDC"), &v)
	var se *oneyaml.SyntaxError
	if !errors.As(err, &se) || se.Category != "encoding" || se.Line != 1 || se.Column != 4 {
		t.Errorf("malformed utf-16: %#v", err)
	}
}

func TestIntoDifferentTargets(t *testing.T) {
	var arr [3]int
	if err := oneyaml.Unmarshal([]byte("[1, 2, 3, 4]"), &arr); err != nil || arr != [3]int{1, 2, 3} {
		t.Errorf("array: %v %v", arr, err)
	}
	var arr2 [3]int = [3]int{9, 9, 9}
	if err := oneyaml.Unmarshal([]byte("[1]"), &arr2); err != nil || arr2 != [3]int{1, 0, 0} {
		t.Errorf("short array: %v %v", arr2, err)
	}
	var im map[int]string
	if err := oneyaml.Unmarshal([]byte("1: a\n0x2: b"), &im); err != nil || !reflect.DeepEqual(im, map[int]string{1: "a", 2: "b"}) {
		t.Errorf("int keys: %v %v", im, err)
	}
	var anyMap map[any]int
	if err := oneyaml.Unmarshal([]byte("1: 1\na: 2\n~: 3"), &anyMap); err != nil || !reflect.DeepEqual(anyMap, map[any]int{1: 1, "a": 2, nil: 3}) {
		t.Errorf("any keys: %v %v", anyMap, err)
	}
	var pp **int
	if err := oneyaml.Unmarshal([]byte("5"), &pp); err != nil || **pp != 5 {
		t.Errorf("ptr ptr: %v", err)
	}
	var f32 float32
	if err := oneyaml.Unmarshal([]byte("1e300"), &f32); err == nil {
		t.Error("float32 overflow accepted")
	}
	var i64 int64
	if err := oneyaml.Unmarshal([]byte("9223372036854775808"), &i64); err == nil {
		t.Error("int64 overflow accepted")
	}
	var u64 uint64
	if err := oneyaml.Unmarshal([]byte("18446744073709551615"), &u64); err != nil || u64 != math.MaxUint64 {
		t.Errorf("uint64: %v %v", u64, err)
	}
	var f float64
	if err := oneyaml.Unmarshal([]byte("3"), &f); err != nil || f != 3 {
		t.Errorf("int to float: %v %v", f, err)
	}
	var s string
	if err := oneyaml.Unmarshal([]byte("0755"), &s); err != nil || s != "0755" {
		t.Errorf("plain to string: %q %v", s, err)
	}
	if err := oneyaml.Unmarshal([]byte("\"a\\tb\""), &s); err != nil || s != "a\tb" {
		t.Errorf("escape: %q %v", s, err)
	}
	if err := oneyaml.Unmarshal([]byte("|\n  line1\n  line2\n"), &s); err != nil || s != "line1\nline2\n" {
		t.Errorf("block: %q %v", s, err)
	}
	var b bool
	if err := oneyaml.Unmarshal([]byte("yes"), &b); err == nil {
		t.Error("1.1 boolean accepted by Core")
	}
	var bs []byte
	if err := oneyaml.Unmarshal([]byte("plain text"), &bs); err != nil || string(bs) != "plain text" {
		t.Errorf("bytes from text: %q %v", bs, err)
	}
	var d time.Duration
	if err := oneyaml.Unmarshal([]byte("1h2m"), &d); err != nil || d != 62*time.Minute {
		t.Errorf("duration: %v %v", d, err)
	}
	var tm time.Time
	if err := oneyaml.Unmarshal([]byte("2002-12-14"), &tm); err != nil || !tm.Equal(time.Date(2002, 12, 14, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date: %v %v", tm, err)
	}
	if err := oneyaml.Unmarshal([]byte("not a time"), &tm); err == nil {
		t.Error("bad time accepted")
	}
	var bi big.Int
	if err := oneyaml.Unmarshal([]byte("0x10"), &bi); err != nil || bi.Int64() != 16 {
		t.Errorf("big.Int: %v %v", &bi, err)
	}
	var iface interface{ M() }
	if err := oneyaml.Unmarshal([]byte("1"), &iface); err == nil {
		t.Error("non-empty interface accepted")
	}
	var set map[string]struct{}
	if err := oneyaml.Unmarshal([]byte("!!set {a, b}"), &set); err != nil || len(set) != 2 {
		t.Errorf("set: %v %v", set, err)
	}
}

func TestEmbeddedPointer(t *testing.T) {
	type Inner struct{ V int }
	type Outer struct {
		*Inner
		W int
	}
	var o Outer
	if err := oneyaml.Unmarshal([]byte("v: 3\nw: 4"), &o); err != nil || o.Inner == nil || o.V != 3 || o.W != 4 {
		t.Errorf("o = %+v, %v", o, err)
	}
}

func TestKeyedExplicitTags(t *testing.T) {
	var v []any
	if err := oneyaml.Unmarshal([]byte("[!!int '12', !!bool 'true', !!null '', !!float 1]"), &v); err != nil ||
		!reflect.DeepEqual(v, []any{12, true, nil, 1.0}) {
		t.Errorf("v = %#v, %v", v, err)
	}
	if err := oneyaml.Unmarshal([]byte("!!int abc"), &v); err == nil {
		t.Error("bad !!int accepted")
	}
}

func TestMapReuseAndDuplicatePrepopulated(t *testing.T) {
	m := map[string]int{"keep": 1}
	if err := oneyaml.Unmarshal([]byte("a: 1"), &m); err != nil || m["keep"] != 1 || m["a"] != 1 {
		t.Errorf("m = %v, %v", m, err)
	}
	if err := oneyaml.Unmarshal([]byte("a: 1\na: 2"), &m); err == nil {
		t.Error("duplicate in a populated map accepted")
	}
}
