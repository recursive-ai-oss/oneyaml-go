package yaml

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
)

func TestYAMLToJSON(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"empty", "", `null`},
		{"comment only", "# c\n", `null`},
		{"marker only", "---\n", `null`},
		{"1.1 booleans", "a: yes\nb: No\nc: on\nd: OFF\ne: y\nf: n\ng: True\n", `{"a":true,"b":false,"c":true,"d":false,"e":true,"f":false,"g":true}`},
		{"quoted stays text", `a: "yes"` + "\n" + `b: 'on'` + "\n", `{"a":"yes","b":"on"}`},
		{"null words", "a:\nb: ~\nc: null\nd: Null\ne: NULL\n", `{"a":null,"b":null,"c":null,"d":null,"e":null}`},
		{"integers", "a: 12\nb: -3\nc: 0x1F\nd: 0755\ne: 0b11\nf: 1_000\ng: 0o17\n", `{"a":12,"b":-3,"c":31,"d":493,"e":3,"f":1000,"g":15}`},
		{"big integers", "a: 9223372036854775807\nb: 18446744073709551615\nc: 18446744073709551616\n", `{"a":9223372036854775807,"b":18446744073709551615,"c":18446744073709552000}`},
		{"floats", "a: 1.5\nb: 1e3\nc: .5\nd: 1.0\ne: -0.25\nf: 2.\n", `{"a":1.5,"b":1000,"c":0.5,"d":1,"e":-0.25,"f":2}`},
		{"base 60 is text", "a: 1:30\nb: 12:30:45\n", `{"a":"1:30","b":"12:30:45"}`},
		{"timestamps are text", "a: 2001-12-14\nb: 2001-12-14t21:59:43.10-05:00\n", `{"a":"2001-12-14","b":"2001-12-14t21:59:43.10-05:00"}`},
		{"keys sorted", "b: 1\na: 2\nB: 3\n", `{"B":3,"a":2,"b":1}`},
		{"non-string keys", "1: a\ntrue: b\n2.5: c\n0x10: d\n", `{"1":"a","16":"d","2.5":"c","true":"b"}`},
		{"duplicate keys, last wins", "a: 1\na: 2\n", `{"a":2}`},
		{"tags", "a: !!str 123\nb: !!int \"7\"\nc: !!float 3\nd: !!binary aGVsbG8=\ne: !custom foo\nf: !!null ''\n", `{"a":"123","b":7,"c":3,"d":"hello","e":"foo","f":null}`},
		{"html is escaped like encoding/json", `a: "<b> & c"`, `{"a":"\u003cb\u003e \u0026 c"}`},
		{"first document only", "a: 1\n---\nb: 2\n", `{"a":1}`},
		{"first document, later one is broken", "a: 1\n---\n: : :\n", `{"a":1}`},
		{"block scalars", "a: |\n  x\n  y\nb: >\n  p\n  q\n", `{"a":"x\ny\n","b":"p q\n"}`},
		{"anchors", "a: &x [1, 2]\nb: *x\n", `{"a":[1,2],"b":[1,2]}`},
		{"anchor redefined", "a: &x 1\nb: *x\nc: &x 2\nd: *x\n", `{"a":1,"b":1,"c":2,"d":2}`},
		{"merge", "base: &b {a: 1, b: 2}\nd:\n  <<: *b\n  b: 3\n  c: 4\n", `{"base":{"a":1,"b":2},"d":{"a":1,"b":3,"c":4}}`},
		{"merge after the key it would override", "base: &b {a: 1}\nd:\n  a: 9\n  <<: *b\n", `{"base":{"a":1},"d":{"a":9}}`},
		{"merge list, first wins", "x: &x {a: 1}\nq: &q {a: 2, b: 2}\nd: {<<: [*x, *q]}\n", `{"d":{"a":1,"b":2},"q":{"a":2,"b":2},"x":{"a":1}}`},
		{"nested merge", "x: &x {a: 1}\nq: &q {<<: *x, b: 2}\nd: {<<: *q}\n", `{"d":{"a":1,"b":2},"q":{"a":1,"b":2},"x":{"a":1}}`},
		{"quoted << is a key", `{"<<": 1}`, `{"\u003c\u003c":1}`},
		{"alias as key", "a: &k key\n*k : v\n", `{"a":"key","key":"v"}`},
		{"utf-8", "a: é日本😀\n", `{"a":"é日本😀"}`},
		{"line separator escaped", "a: \"x\\u2028y\"\n", `{"a":"x\u2028y"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := YAMLToJSON([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("got  %s\nwant %s", got, c.want)
			}
		})
	}
}

func TestYAMLToJSONErrors(t *testing.T) {
	for _, c := range []struct{ name, in string }{
		{"syntax", "a: [1, 2\n"},
		{"null key", "~: x\n"},
		{"sequence key", "? [1]\n: x\n"},
		{"tag mismatch", "a: !!int abc\n"},
		{"bad binary", "a: !!binary '!!'\n"},
		{"unknown anchor", "a: *nope\n"},
		{"merge of a scalar", "a:\n  <<: 5\n"},
		{"merge of a sequence of scalars", "a:\n  <<: [1]\n"},
		{"merge of an alias to a sequence", "s: &s [{a: 1}]\na:\n  <<: *s\n"},
		{"NaN has no JSON form", "a: .nan\n"},
		{"infinity has no JSON form", "a: .inf\n"},
		{"self-containing anchor", "a: &x [*x]\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := YAMLToJSON([]byte(c.in))
			if err == nil {
				t.Fatalf("no error, got %s", got)
			}
		})
	}
}

func TestStrictRejectsDuplicates(t *testing.T) {
	for _, in := range []string{"a: 1\na: 2\n", "{a: 1, a: 2}", "x:\n  b: 1\n  b: 2\n"} {
		if _, err := YAMLToJSONStrict([]byte(in)); err == nil || !strings.Contains(err.Error(), "already set") {
			t.Errorf("%q: %v", in, err)
		}
	}
	// 1 and "1" are two keys in YAML, even though both become "1" in JSON.
	if _, err := YAMLToJSONStrict([]byte("1: a\n\"1\": b\n")); err != nil {
		t.Errorf("distinct keys: %v", err)
	}
	// A merge overridden by a key written in the mapping is not a duplicate.
	if _, err := YAMLToJSONStrict([]byte("b: &b {a: 1}\nd:\n  <<: *b\n  a: 2\n")); err != nil {
		t.Errorf("merge override: %v", err)
	}
}

type meta struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

type embedded struct {
	meta
	Count  string   `json:"count"`
	Values []string `json:"values"`
	Ptr    *string  `json:"ptr"`
}

type self struct{ Raw string }

func (s *self) UnmarshalJSON(b []byte) error { s.Raw = string(b); return nil }

func TestUnmarshalConvertsScalarsToStringFields(t *testing.T) {
	var v embedded
	err := Unmarshal([]byte("name: 1\nlabels: {a: 1, b: yes, c: 1.50}\ncount: 0755\nvalues: [1, true, x, ~]\nptr: 2\n"), &v)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "1" || v.Count != "493" || v.Labels["b"] != "true" || v.Labels["c"] != "1.5" || v.Ptr == nil || *v.Ptr != "2" {
		t.Fatalf("%+v", v)
	}
	if !reflect.DeepEqual(v.Values, []string{"1", "true", "x", ""}) {
		t.Fatalf("values %q", v.Values)
	}
}

func TestUnmarshalerSeesTheJSONForm(t *testing.T) {
	var m map[string]self
	if err := Unmarshal([]byte("a: 5\nb: yes\nc: x\n"), &m); err != nil {
		t.Fatal(err)
	}
	if m["a"].Raw != "5" || m["b"].Raw != "true" || m["c"].Raw != `"x"` {
		t.Fatalf("%v", m)
	}
}

func TestUnmarshalOptions(t *testing.T) {
	var n struct{ Big json.Number }
	if err := Unmarshal([]byte("big: 12345678901234567890\n"), &n, func(d *json.Decoder) *json.Decoder { d.UseNumber(); return d }); err != nil || n.Big != "12345678901234567890" {
		t.Fatalf("%v %v", n, err)
	}
	var v meta
	if err := Unmarshal([]byte("name: a\nextra: 1\n"), &v); err != nil {
		t.Fatalf("unknown field: %v", err)
	}
	if err := Unmarshal([]byte("name: a\nextra: 1\n"), &v, DisallowUnknownFields); err == nil {
		t.Fatal("DisallowUnknownFields accepted an unknown field")
	}
	if err := UnmarshalStrict([]byte("name: a\nextra: 1\n"), &v); err == nil {
		t.Fatal("UnmarshalStrict accepted an unknown field")
	}
	// A caller's option slice is not changed by UnmarshalStrict.
	opts := make([]JSONOpt, 0, 4)
	_ = UnmarshalStrict([]byte("name: a\n"), &v, opts...)
	if len(opts[:cap(opts)]) != 4 || opts[:1][0] != nil {
		t.Fatal("UnmarshalStrict wrote into the caller's slice")
	}
}

func TestUnmarshalOddTargets(t *testing.T) {
	// As in sigs.k8s.io/yaml, a non-pointer target is decoded into a copy and not
	// reported. A nil target, which makes sigs.k8s.io/yaml panic, is an error.
	if err := Unmarshal([]byte("a: 1"), nil); err == nil {
		t.Errorf("nil target accepted")
	}
	if err := Unmarshal([]byte("a: 1"), meta{}); err != nil {
		t.Errorf("value target: %v", err)
	}
	if err := Unmarshal([]byte("name: [1]"), &meta{}); err == nil {
		t.Error("type error not reported")
	}
}

func TestLimits(t *testing.T) {
	var bomb strings.Builder
	bomb.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i < 12; i++ {
		bomb.WriteString("a" + itoa(i) + ": &a" + itoa(i) + " [")
		for j := 0; j < 10; j++ {
			bomb.WriteString("*a" + itoa(i-1) + ",")
		}
		bomb.WriteString("]\n")
	}
	_, err := YAMLToJSON([]byte(bomb.String()))
	var le *oneyaml.LimitError
	if !errors.As(err, &le) {
		t.Fatalf("alias bomb: %v", err)
	}
	deep := strings.Repeat("[", 20000) + strings.Repeat("]", 20000)
	if _, err := YAMLToJSON([]byte(deep)); !errors.As(err, &le) {
		t.Fatalf("deep nesting: %v", err)
	}
	if _, err := JSONToYAML([]byte(deep)); err == nil {
		t.Fatal("deep JSON accepted")
	}
	ok := strings.Repeat("[", 500) + strings.Repeat("]", 500)
	if _, err := YAMLToJSON([]byte(ok)); err != nil {
		t.Fatalf("500 levels: %v", err)
	}
	// Aliases that stay small are fine.
	if _, err := YAMLToJSON([]byte("a: &x [1,2,3]\nb: [*x, *x, *x]\n")); err != nil {
		t.Fatal(err)
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func TestSyntaxErrorsAreOneyamlErrors(t *testing.T) {
	_, err := YAMLToJSON([]byte("a: [1\n"))
	var se *oneyaml.SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("%v", err)
	}
	err = Unmarshal([]byte("a: [1\n"), &map[string]any{})
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "error converting YAML to JSON") {
		t.Fatalf("%v", err)
	}
	err = Unmarshal([]byte("name: [1]\n"), &meta{})
	var ute *json.UnmarshalTypeError
	if !errors.As(err, &ute) {
		t.Fatalf("%v", err)
	}
}

func TestJSONToYAML(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"empty input", ``, "null\n"},
		{"scalars", `"x"`, "x\n"},
		{"number", `12`, "12\n"},
		{"empty collections", `{"a":[],"b":{}}`, "a: []\nb: {}\n"},
		{"compact sequences", `{"a":[1,2,{"b":3,"c":[4]}],"d":"e"}`, "a:\n- 1\n- 2\n- b: 3\n  c:\n  - 4\nd: e\n"},
		{"nested sequences", `[[1,2],[3]]`, "- - 1\n  - 2\n- - 3\n"},
		{"natural key order", `{"a10":1,"a2":2,"a1":3,"B":4,"b":5,"10":6,"9":7}`, "\"9\": 7\n\"10\": 6\nB: 4\na1: 3\na2: 2\na10: 1\nb: 5\n"},
		{"1.1 words are quoted", `{"a":"yes","b":"No","c":"on","d":"null","e":"~","f":"1","g":"1.5","h":"0755","i":"1:30","j":"2001-12-14","k":"","l":"y"}`,
			"a: \"yes\"\nb: \"No\"\nc: \"on\"\nd: \"null\"\ne: \"~\"\nf: \"1\"\ng: \"1.5\"\nh: \"0755\"\ni: \"1:30\"\nj: \"2001-12-14\"\nk: \"\"\nl: \"y\"\n"},
		{"plain when safe", `{"a":"hello world","b":"a-b","c":"1.2.3","d":"x:y"}`, "a: hello world\nb: a-b\nc: 1.2.3\nd: x:y\n"},
		{"single quotes for indicators", `{"a":"a: b","b":"- x","c":"x #y","d":" lead","e":"trail ","f":"{x}","g":"it's: ok"}`,
			"a: 'a: b'\nb: '- x'\nc: 'x #y'\nd: ' lead'\ne: 'trail '\nf: '{x}'\ng: 'it''s: ok'\n"},
		{"literal blocks", `{"a":"x\ny","b":"x\ny\n","c":"x\n\n","d":"\nx"}`, "a: |-\n  x\n  y\nb: |\n  x\n  y\nc: |+\n  x\n\nd: |2-\n\n  x\n"},
		{"escapes", `{"a":"tab\there","b":"\u0000","c":"é","d":"\ud83d\ude00","e":"\u007f"}`, "a: \"tab\\there\"\nb: \"\\0\"\nc: é\nd: \"\\U0001F600\"\ne: \"\\x7F\"\n"},
		{"integers keep every digit", `{"a":18446744073709551615,"b":-9223372036854775808,"c":1.5,"d":1e21,"e":100}`, "a: 18446744073709551615\nb: -9223372036854775808\nc: 1.5\nd: 1e+21\ne: 100\n"},
		{"long strings fold after column 80", `{"a":"The quick brown fox jumps over the lazy dog and keeps running through the forest until night falls"}`,
			"a: The quick brown fox jumps over the lazy dog and keeps running through the forest\n  until night falls\n"},
		{"long keys use the explicit form", `{"` + strings.Repeat("k", 130) + `":1}`, "? " + strings.Repeat("k", 130) + "\n: 1\n"},
		{"duplicate keys, last wins", `{"a":1,"a":2}`, "a: 2\n"},
		{"quoted merge key", `{"<<":"<<"}`, "\"<<\": \"<<\"\n"},
		{"line separators are escaped", `{"a":"x\u2028y"}`, "a: \"x\\Ly\"\n"},
		{"JSON read as YAML", "{a: 1, 'b': [x, yes]}", "a: 1\nb:\n- x\n- true\n"},
		{"not strict JSON", "a: 1\nb: 2\n", "a: 1\nb: 2\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := JSONToYAML([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("got\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

func TestMarshal(t *testing.T) {
	type port struct {
		Name string `json:"name,omitempty"`
		Port int    `json:"port"`
	}
	v := struct {
		Name  string            `json:"name"`
		Ports []port            `json:"ports"`
		Env   map[string]string `json:"env"`
		Skip  string            `json:"-"`
		Nil   []string          `json:"nil"`
	}{"web", []port{{"http", 80}, {Port: 443}}, map[string]string{"B": "yes", "A": "1"}, "x", nil}
	got, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	want := "env:\n  A: \"1\"\n  B: \"yes\"\nname: web\nnil: null\nports:\n- name: http\n  port: 80\n- port: 443\n"
	if string(got) != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if _, err := Marshal(make(chan int)); err == nil {
		t.Fatal("channel accepted")
	}
}

// Marshal's output reads back as the same JSON.
func TestRoundTrip(t *testing.T) {
	for _, j := range []string{
		`{"a":[1,2.5,"x",null,true,{"b":"yes"}],"c":{"d":"multi\nline\ntext","e":"  spaced  ","f":"\u2028"}}`,
		`["\ufeff", "a\tb", "\ud83d\ude00", "<<", "~", "1e3", "0x1F", ""]`,
		`{"k":"` + strings.Repeat("word ", 60) + `"}`,
	} {
		y, err := JSONToYAML([]byte(j))
		if err != nil {
			t.Fatal(err)
		}
		back, err := YAMLToJSON(y)
		if err != nil {
			t.Fatalf("%s: %v\n%s", j, err, y)
		}
		var a, b any
		if err := json.Unmarshal([]byte(j), &a); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(back, &b); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("round trip changed the value\n in: %s\nyaml:\n%s\nout: %s", j, y, back)
		}
	}
}

func TestUTF16Input(t *testing.T) {
	u16 := []byte{0xFF, 0xFE}
	for _, r := range "a: yes\n" {
		u16 = append(u16, byte(r), 0)
	}
	got, err := YAMLToJSON(u16)
	if err != nil || string(got) != `{"a":true}` {
		t.Fatalf("%s %v", got, err)
	}
}

func TestFirstDocument(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a: 1\n", "a: 1\n"},
		{"a: 1\n---\nb: 2\n", "a: 1\n"},
		{"---\na: 1\n---\nb: 2\n", "---\na: 1\n"},
		{"# c\n%YAML 1.2\n---\na: 1\n...\n---\nb: 2\n", "# c\n%YAML 1.2\n---\na: 1\n...\n"},
		{"--- |\n  x\n--- y\n", "--- |\n  x\n"},
		{"a: ---\nb: ...\n", "a: ---\nb: ...\n"},
		{"a: |\n  ---\n  x\n", "a: |\n  ---\n  x\n"},
		{"---x\n", "---x\n"},
		{"\xef\xbb\xbfa: 1\n---\nb: 2\n", "\xef\xbb\xbfa: 1\n---\nb: 2\n"},
	} {
		if got := string(firstDocument([]byte(c.in))); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestConcurrentUse(t *testing.T) {
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			var err error
			for j := 0; j < 200 && err == nil; j++ {
				var m embedded
				err = Unmarshal([]byte("name: x\ncount: 5\nvalues: [1, 2]\n"), &m)
				if err == nil {
					_, err = Marshal(m)
				}
			}
			done <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

// The alias budget is the one the primary decoder computes: the larger of the
// default limit and ten times the size of the input, not their sum.
func TestAliasBudgetFollowsTheDefaultLimits(t *testing.T) {
	limit := oneyaml.DefaultLimits().MaxAliasNodes
	var b strings.Builder
	b.WriteString("a: &a [")
	const items = 12000
	for i := 0; i < items; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("x")
	}
	b.WriteString("]\nb: [")
	// Enough aliases to pass the limit by a little. The text is far too small for
	// ten times its size to matter, which the sum of the limit and that size
	// (the budget this package used to have) would have allowed.
	n := limit/items + 4
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("*a")
	}
	b.WriteString("]\n")
	_, err := YAMLToJSON([]byte(b.String()))
	var le *oneyaml.LimitError
	if !errors.As(err, &le) {
		t.Fatalf("%d bytes expanding past %d nodes: %v", b.Len(), limit, err)
	}
}

// Resolving an alias must not walk the earlier definitions of its name: a
// document that defines one anchor many times has to stay linear (n log n at
// worst). The count of definitions compared is deterministic, unlike a timing.
func TestRedefinedAnchorsScaleLinearly(t *testing.T) {
	probes := func(pairs int) int {
		var b strings.Builder
		b.WriteString("[")
		for i := 0; i < pairs; i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("&a 0, *a")
		}
		b.WriteString("]")
		w := getWalker()
		defer w.release()
		if err := w.convert([]byte(b.String()), false, nil); err != nil {
			t.Fatal(err)
		}
		return w.probes
	}
	p1, p2, p4 := probes(2000), probes(4000), probes(8000)
	// Doubling the input may not much more than double the work.
	if p2 > 5*p1/2 || p4 > 5*p2/2 {
		t.Fatalf("probes for 2000, 4000, 8000 pairs: %d, %d, %d", p1, p2, p4)
	}
	// A later definition wins over an earlier one for the aliases after it.
	got, err := YAMLToJSON([]byte("[&a 1, *a, &a 2, *a]"))
	if err != nil || string(got) != "[1,1,2,2]" {
		t.Fatalf("%q, %v", got, err)
	}
}

// Deeply nested input is read and written without recursion that follows the
// depth, and the depth limit still applies.
func TestDeepNestingBothWays(t *testing.T) {
	for _, n := range []int{100, 9000} {
		y := strings.Repeat("[", n) + "1" + strings.Repeat("]", n)
		j, err := YAMLToJSON([]byte(y))
		if err != nil || string(j) != y {
			t.Fatalf("depth %d: %v", n, err)
		}
		out, err := JSONToYAML(j)
		if err != nil {
			t.Fatalf("depth %d to YAML: %v", n, err)
		}
		back, err := YAMLToJSON(out)
		if err != nil || string(back) != y {
			t.Fatalf("depth %d does not read back: %v", n, err)
		}
	}
	deep := strings.Repeat("[", 10100) + strings.Repeat("]", 10100)
	if _, err := YAMLToJSON([]byte(deep)); err == nil {
		t.Fatal("no depth error for YAML")
	}
	if _, err := JSONToYAML([]byte(deep)); err == nil {
		t.Fatal("no depth error for JSON")
	}
}
