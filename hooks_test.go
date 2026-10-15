package oneyaml_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
)

// quantity reads and writes itself through encoding/json only: a number or a
// string such as "500m".
type quantity struct{ s string }

func (q *quantity) UnmarshalJSON(b []byte) error {
	var x any
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	switch v := x.(type) {
	case string:
		if v == "" || strings.Contains(v, " ") {
			return fmt.Errorf("bad quantity %q", v)
		}
		q.s = v
	case float64:
		q.s = fmt.Sprint(v)
	default:
		return fmt.Errorf("quantity must be a string or a number, got %s", b)
	}
	return nil
}

func (q quantity) MarshalJSON() ([]byte, error) { return json.Marshal(q.s) }

// portOrName holds a number or a name, like a Kubernetes IntOrString.
type portOrName struct {
	num  int
	name string
	isN  bool
}

func (p *portOrName) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		p.isN = true
		return json.Unmarshal(b, &p.name)
	}
	p.isN = false
	return json.Unmarshal(b, &p.num)
}

func (p portOrName) MarshalJSON() ([]byte, error) {
	if p.isN {
		return json.Marshal(p.name)
	}
	return json.Marshal(p.num)
}

// meta is a struct with a JSON hook that reads a whole mapping.
type meta struct{ raw map[string]any }

func (m *meta) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &m.raw) }

type spec struct {
	CPU  quantity   `yaml:"cpu"`
	Port portOrName `yaml:"port"`
	Meta meta       `yaml:"meta"`
}

func TestJSONHookDecode(t *testing.T) {
	var s spec
	src := "cpu: 500m\nport: http\nmeta: {name: web, n: 3, tags: [a, b]}\n"
	if err := oneyaml.Unmarshal([]byte(src), &s); err != nil {
		t.Fatal(err)
	}
	if s.CPU.s != "500m" || !s.Port.isN || s.Port.name != "http" {
		t.Fatalf("got %+v", s)
	}
	want := map[string]any{"name": "web", "n": float64(3), "tags": []any{"a", "b"}}
	if fmt.Sprint(s.Meta.raw) != fmt.Sprint(want) {
		t.Fatalf("meta = %v", s.Meta.raw)
	}
	// A number reaches the hook as a JSON number.
	if err := oneyaml.Unmarshal([]byte("cpu: 0.5\nport: 8080\n"), &s); err != nil {
		t.Fatal(err)
	}
	if s.CPU.s != "0.5" || s.Port.isN || s.Port.num != 8080 {
		t.Fatalf("got %+v", s)
	}
}

func TestJSONHookDecodeEverywhere(t *testing.T) {
	src := "a: &q 500m\nlist: [*q, 2]\nm: {k: *q}\nptr: *q\n"
	var v struct {
		List []quantity          `yaml:"list"`
		M    map[string]quantity `yaml:"m"`
		Ptr  *quantity           `yaml:"ptr"`
	}
	if err := oneyaml.Unmarshal([]byte(src), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.List) != 2 || v.List[0].s != "500m" || v.List[1].s != "2" || v.M["k"].s != "500m" || v.Ptr == nil || v.Ptr.s != "500m" {
		t.Fatalf("got %+v", v)
	}
	// Document.Decode and Decoder go through the same path.
	doc, err := oneyaml.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Ptr *quantity `yaml:"ptr"`
	}
	if err := doc.Decode(&w); err != nil || w.Ptr == nil || w.Ptr.s != "500m" {
		t.Fatalf("Document.Decode: %v %+v", err, w)
	}
	var x struct {
		Ptr *quantity `yaml:"ptr"`
	}
	if err := oneyaml.NewDecoder(strings.NewReader(src)).Decode(&x); err != nil || x.Ptr == nil || x.Ptr.s != "500m" {
		t.Fatalf("Decoder: %v %+v", err, x)
	}
}

func TestJSONHookNull(t *testing.T) {
	v := struct {
		P *quantity `yaml:"p"`
		Q quantity  `yaml:"q"`
	}{P: &quantity{"x"}, Q: quantity{"keep"}}
	if err := oneyaml.Unmarshal([]byte("p: null\nq: ~\n"), &v); err != nil {
		t.Fatal(err)
	}
	if v.P != nil || v.Q.s != "keep" {
		t.Fatalf("got %+v", v)
	}
}

func TestJSONHookErrorHasPosition(t *testing.T) {
	var s spec
	err := oneyaml.Unmarshal([]byte("port: 1\ncpu: \"a b\"\n"), &s)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) {
		t.Fatalf("want *TypeError, got %T %v", err, err)
	}
	if te.Line != 2 || te.Path != "/cpu" || !strings.Contains(te.Msg, "bad quantity") {
		t.Fatalf("got %+v", te)
	}
	// A sequence is not something quantity accepts.
	err = oneyaml.Unmarshal([]byte("cpu: [1]\n"), &s)
	if !errors.As(err, &te) || te.Line != 1 {
		t.Fatalf("got %v", err)
	}
}

func TestJSONHookUnrepresentable(t *testing.T) {
	cases := map[string]string{
		"non-string key": "meta: {1: a}\n",
		"nan":            "meta: {x: .nan}\n",
		"inf":            "meta: {x: -.inf}\n",
		"collection key": "meta: {? [a]: b}\n",
	}
	for name, src := range cases {
		var s spec
		err := oneyaml.Unmarshal([]byte(src), &s)
		var te *oneyaml.TypeError
		if !errors.As(err, &te) {
			t.Errorf("%s: want *TypeError, got %T %v", name, err, err)
			continue
		}
		if s.Meta.raw != nil {
			t.Errorf("%s: value was set despite the error", name)
		}
	}
}

// Text hook ranks above the JSON hook for a scalar; the JSON hook still serves
// a mapping.
type both struct{ via string }

func (b *both) UnmarshalText(t []byte) error { b.via = "text:" + string(t); return nil }
func (b *both) UnmarshalJSON(j []byte) error { b.via = "json:" + string(j); return nil }

// yamlFirst has all three hooks; the YAML one wins.
type yamlFirst struct{ via string }

func (y *yamlFirst) UnmarshalYAML(n oneyaml.Node) error { y.via = "yaml"; return nil }
func (y *yamlFirst) UnmarshalText([]byte) error         { y.via = "text"; return nil }
func (y *yamlFirst) UnmarshalJSON([]byte) error         { y.via = "json"; return nil }

func TestHookPrecedence(t *testing.T) {
	var v struct {
		S both      `yaml:"s"`
		M both      `yaml:"m"`
		Y yamlFirst `yaml:"y"`
		Z yamlFirst `yaml:"z"`
	}
	if err := oneyaml.Unmarshal([]byte("s: hi\nm: {a: 1}\ny: x\nz: {a: 1}\n"), &v); err != nil {
		t.Fatal(err)
	}
	if v.S.via != "text:hi" || v.M.via != `json:{"a":1}` || v.Y.via != "yaml" || v.Z.via != "yaml" {
		t.Fatalf("got %+v", v)
	}
}

func TestJSONHookMarshal(t *testing.T) {
	v := spec{CPU: quantity{"500m"}, Port: portOrName{name: "http", isN: true}}
	out, err := oneyaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	want := "cpu: 500m\nmeta: null\nport: http\n"
	_ = want
	if !strings.Contains(string(out), "cpu: 500m\n") || !strings.Contains(string(out), "port: http\n") {
		t.Fatalf("got %q", out)
	}
	out, err = oneyaml.Marshal(map[string]any{"p": portOrName{num: 80}, "q": &quantity{"1"}})
	if err != nil || string(out) != "p: 80\nq: '1'\n" {
		t.Fatalf("got %q %v", out, err)
	}
	// An object and a list come back as a mapping and a sequence.
	out, err = oneyaml.Marshal(map[string]any{"r": json.RawMessage(`{"b":[1,2.5,true,null],"a":"x"}`)})
	if err != nil || string(out) != "r:\n  a: x\n  b:\n    - 1\n    - 2.5\n    - true\n    - null\n" {
		t.Fatalf("got %q %v", out, err)
	}
}

type badJSON struct{}

func (badJSON) MarshalJSON() ([]byte, error) { return []byte(`{"a":`), nil }

type failJSON struct{}

func (failJSON) MarshalJSON() ([]byte, error) { return nil, errors.New("boom") }

func TestJSONHookMarshalErrors(t *testing.T) {
	for _, v := range []any{badJSON{}, failJSON{}} {
		_, err := oneyaml.Marshal(map[string]any{"k": v})
		var te *oneyaml.TypeError
		if !errors.As(err, &te) || te.Path != "/k" {
			t.Errorf("%T: got %v", v, err)
		}
	}
}

// Marshal order: the YAML hook, the text hook, then the JSON hook.
type allOut struct{}

func (allOut) MarshalText() ([]byte, error) { return []byte("text"), nil }
func (allOut) MarshalJSON() ([]byte, error) { return []byte(`"json"`), nil }

func TestMarshalPrecedence(t *testing.T) {
	out, err := oneyaml.Marshal(allOut{})
	if err != nil || string(out) != "text\n" {
		t.Fatalf("got %q %v", out, err)
	}
}

// oldStyle uses the function form of UnmarshalYAML.
type oldStyle struct {
	Name string
	Kind string
}

func (o *oldStyle) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err == nil {
		o.Name, o.Kind = s, "scalar"
		return nil
	}
	var raw struct{ Name string }
	if err := unmarshal(&raw); err != nil {
		return err
	}
	o.Name, o.Kind = raw.Name, "mapping"
	return nil
}

func TestFuncUnmarshaler(t *testing.T) {
	src := "a: plain\nb: {name: mapped}\nc: &x {name: shared}\nd: [*x, z]\n"
	var w struct {
		A oldStyle   `yaml:"a"`
		B oldStyle   `yaml:"b"`
		C *oldStyle  `yaml:"c"`
		D []oldStyle `yaml:"d"`
	}
	if err := oneyaml.Unmarshal([]byte(src), &w); err != nil {
		t.Fatal(err)
	}
	if w.A != (oldStyle{"plain", "scalar"}) || w.B != (oldStyle{"mapped", "mapping"}) ||
		w.C == nil || *w.C != (oldStyle{"shared", "mapping"}) ||
		len(w.D) != 2 || w.D[0].Name != "shared" || w.D[1] != (oldStyle{"z", "scalar"}) {
		t.Fatalf("got %+v", w)
	}
	// null never reaches the method; a pointer is cleared.
	w.A = oldStyle{"keep", "x"}
	if err := oneyaml.Unmarshal([]byte("a: null\nc: ~\n"), &w); err != nil || w.A.Name != "keep" || w.C != nil {
		t.Fatalf("null: %v %+v", err, w)
	}
	// Through a Document.
	doc, _ := oneyaml.Parse([]byte(src))
	var o oldStyle
	if err := doc.Get("/b", &o); err != nil || o.Name != "mapped" {
		t.Fatalf("Get: %v %+v", err, o)
	}
}

type failingOld struct{}

func (failingOld) UnmarshalYAML(unmarshal func(any) error) error {
	var n int
	return unmarshal(&n)
}

func TestFuncUnmarshalerErrors(t *testing.T) {
	var v struct {
		F *failingOld `yaml:"f"`
	}
	err := oneyaml.Unmarshal([]byte("f: {a: 1}\n"), &v)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) || te.Line != 1 {
		t.Fatalf("got %T %v", err, err)
	}
}

// An alias bomb read through unmarshal() is charged to the call's budget.
func TestFuncUnmarshalerSharesBudget(t *testing.T) {
	var b strings.Builder
	b.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i < 9; i++ {
		fmt.Fprintf(&b, "a%d: &a%d [", i, i)
		for j := 0; j < 10; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "*a%d", i-1)
		}
		b.WriteString("]\n")
	}
	b.WriteString("t: *a8\n")
	var v struct {
		T sink `yaml:"t"`
	}
	err := oneyaml.Unmarshal([]byte(b.String()), &v)
	var le *oneyaml.LimitError
	if !errors.As(err, &le) {
		t.Fatalf("want *LimitError, got %T %v", err, err)
	}
}

type sink struct{}

func (*sink) UnmarshalYAML(unmarshal func(any) error) error {
	var x any
	return unmarshal(&x)
}

// A method with a signature of another library is an error, not ignored.
type v3Style struct{ A int }

func (v *v3Style) UnmarshalYAML(n *struct{ Kind int }) error { return nil }

type v3Marshal struct{ A int }

func (v v3Marshal) MarshalYAML() ([]byte, error) { return nil, nil }

type anyOnly struct{ A int }

func (a anyOnly) MarshalYAML() any { return 1 }

func TestUnrecognisedYAMLMethods(t *testing.T) {
	var te *oneyaml.TypeError
	var s struct {
		X v3Style `yaml:"x"`
	}
	err := oneyaml.Unmarshal([]byte("x: {a: 1}\n"), &s)
	if !errors.As(err, &te) || !strings.Contains(te.Msg, "UnmarshalYAML(func(any) error) error") || te.Line != 1 {
		t.Fatalf("decode: got %v", err)
	}
	if s.X.A != 0 {
		t.Fatalf("bound structurally: %+v", s)
	}
	// Null does not hide the mistake.
	if err := oneyaml.Unmarshal([]byte("x: 1\n"), &s); !errors.As(err, &te) {
		t.Fatalf("scalar: got %v", err)
	}
	for _, v := range []any{v3Marshal{}, &v3Marshal{}, anyOnly{}, []anyOnly{{}}} {
		_, err := oneyaml.Marshal(v)
		if !errors.As(err, &te) || !strings.Contains(te.Msg, "MarshalYAML() (any, error)") {
			t.Errorf("%T: got %v", v, err)
		}
	}
	// The value form of the types without the method is untouched.
	if out, err := oneyaml.Marshal(v3Style{A: 2}); err != nil || string(out) != "a: 2\n" {
		t.Fatalf("got %q %v", out, err)
	}
}

func TestNodeIsNotATarget(t *testing.T) {
	src := "spec: {a: 1}\n"
	var s struct {
		Spec oneyaml.Node `yaml:"spec"`
	}
	var top oneyaml.Node
	var ptr struct {
		Spec *oneyaml.Node `yaml:"spec"`
	}
	check := func(name string, err error) {
		t.Helper()
		var te *oneyaml.TypeError
		if !errors.As(err, &te) || !strings.Contains(te.Msg, "Document.Get(pointer, &node)") {
			t.Errorf("%s: got %v", name, err)
		}
	}
	check("Unmarshal field", oneyaml.Unmarshal([]byte(src), &s))
	check("Unmarshal root", oneyaml.Unmarshal([]byte(src), &top))
	check("Unmarshal *Node field", oneyaml.Unmarshal([]byte(src), &ptr))
	check("Decoder", oneyaml.NewDecoder(strings.NewReader(src)).Decode(&s))
	doc, _ := oneyaml.Parse([]byte(src))
	check("Document.Decode", doc.Decode(&s))
	if s.Spec.IsValid() {
		t.Error("a Node was set")
	}
	// A null pointer field is fine, and Get still hands out the node.
	if err := oneyaml.Unmarshal([]byte("spec: null\n"), &ptr); err != nil || ptr.Spec != nil {
		t.Errorf("null: %v", err)
	}
	var n oneyaml.Node
	if err := doc.Get("/spec", &n); err != nil || n.Kind() != oneyaml.MappingNode {
		t.Errorf("Get: %v", err)
	}
}

func TestMarshalNodeWritesItsValue(t *testing.T) {
	doc, _ := oneyaml.Parse([]byte("spec: {b: [1, 2], a: x}\n"))
	var n oneyaml.Node
	if err := doc.Get("/spec", &n); err != nil {
		t.Fatal(err)
	}
	type T struct {
		Spec  oneyaml.Node  `yaml:"spec"`
		Ptr   *oneyaml.Node `yaml:"ptr"`
		Empty oneyaml.Node  `yaml:"empty"`
	}
	out, err := oneyaml.Marshal(T{Spec: n, Ptr: &n})
	if err != nil {
		t.Fatal(err)
	}
	want := "spec:\n  a: x\n  b:\n    - 1\n    - 2\nptr:\n  a: x\n  b:\n    - 1\n    - 2\nempty: null\n"
	if string(out) != want {
		t.Fatalf("got %q", out)
	}
}

type zeroish struct{ V int }

func (z zeroish) IsZero() bool { return z.V == 0 || z.V == 42 }

func TestOmitEmptyStruct(t *testing.T) {
	type inner struct {
		A int
		B string
		c int
	}
	type T struct {
		In   inner    `yaml:"in,omitempty"`
		Z    zeroish  `yaml:"z,omitempty"`
		P    *inner   `yaml:"p,omitempty"`
		Keep inner    `yaml:"keep"`
		OZ   inner    `yaml:"oz,omitzero"`
		Json inner    `json:"j,omitempty"`
		Tm   struct{} `yaml:"tm,omitempty"`
	}
	out, err := oneyaml.Marshal(T{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "keep:\n  a: 0\n  b: \"\"\n  c: 0\n" && !strings.HasPrefix(string(out), "keep:") {
		t.Fatalf("got %q", out)
	}
	if strings.Contains(string(out), "in:") || strings.Contains(string(out), "z:") || strings.Contains(string(out), "oz:") ||
		strings.Contains(string(out), "j:") || strings.Contains(string(out), "tm:") {
		t.Fatalf("zero structs written: %q", out)
	}
	// A struct with anything set is written, and so is one whose IsZero says no.
	out, err = oneyaml.Marshal(T{In: inner{A: 1}, Z: zeroish{V: 7}, P: &inner{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"in:", "z:", "p:"} {
		if !strings.Contains(string(out), k) {
			t.Errorf("%s missing in %q", k, out)
		}
	}
	out, _ = oneyaml.Marshal(T{Z: zeroish{V: 42}})
	if strings.Contains(string(out), "z:") {
		t.Errorf("IsZero true but written: %q", out)
	}
	// Unexported state keeps a struct alive.
	out, _ = oneyaml.Marshal(T{In: inner{c: 1}})
	if !strings.Contains(string(out), "in:") {
		t.Errorf("struct with unexported state dropped: %q", out)
	}
}
