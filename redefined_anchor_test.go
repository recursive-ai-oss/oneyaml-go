package oneyaml_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"oneyaml.dev/go"
	k8syaml "oneyaml.dev/go/compat/k8syaml"
	yamlv3 "oneyaml.dev/go/compat/yamlv3"
)

// An alias names the latest anchor written before it, even when that anchor is
// inside an earlier definition of the same name (kit case
// parse/redefined-anchor-alias-is-the-latest-node). Every reader must agree.
const redefinedAnchor = "a: &b\n se: &b\n   1\ncopy: *b\n"

func TestRedefinedAnchorEveryReader(t *testing.T) {
	in := []byte(redefinedAnchor)
	want := map[string]any{"a": map[string]any{"se": 1}, "copy": 1}
	norm := func(v any) any { // numbers differ by reader; compare through JSON
		b, _ := json.Marshal(v)
		var o any
		json.Unmarshal(b, &o)
		return o
	}
	wantN := norm(want)
	check := func(name string, got any, err error) {
		got = reflect.Indirect(reflect.ValueOf(got)).Interface()
		got = reflect.Indirect(reflect.ValueOf(got)).Interface()
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", name, err)
		} else if !reflect.DeepEqual(norm(got), wantN) {
			t.Errorf("%s: got %v", name, got)
		}
	}

	var a any
	check("Unmarshal any", &a, oneyaml.Unmarshal(in, &a))

	var typed struct {
		A    struct{ Se int } `yaml:"a"`
		Copy int              `yaml:"copy"`
	}
	if err := oneyaml.Unmarshal(in, &typed); err != nil || typed.Copy != 1 || typed.A.Se != 1 {
		t.Errorf("Unmarshal typed: %+v %v", typed, err)
	}

	var d any
	check("Decoder", &d, oneyaml.NewDecoder(strings.NewReader(redefinedAnchor)).Decode(&d))

	doc, err := oneyaml.Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	var dd any
	check("Document.Decode", &dd, doc.Decode(&dd))
	var c int
	if err := doc.Get("/copy", &c); err != nil || c != 1 {
		t.Errorf("Document.Get /copy: %d %v", c, err)
	}

	if al, err := doc.Root().Find("copy"); err != nil || al.Target().Value() != "1" {
		t.Errorf("Node.Target of the alias: %q %v", al.Target().Value(), err)
	}

	// Patch: copy and test read the alias's value.
	p, _ := oneyaml.Parse(in)
	if err := p.ApplyPatch([]byte(`[{"op":"test","path":"/copy","value":1},{"op":"copy","from":"/copy","path":"/dup"}]`)); err != nil {
		t.Errorf("patch: %v", err)
	} else {
		var dup int
		if err := p.Get("/dup", &dup); err != nil || dup != 1 {
			t.Errorf("patch copy: %d %v", dup, err)
		}
	}

	var y any
	check("yamlv3 any", &y, yamlv3.Unmarshal(in, &y))
	var n yamlv3.Node
	if err := yamlv3.Unmarshal(in, &n); err != nil {
		t.Fatal(err)
	}
	var viaNode any
	check("yamlv3 Node.Decode", &viaNode, n.Decode(&viaNode))

	j, err := k8syaml.YAMLToJSON(in)
	if err != nil || string(j) != `{"a":{"se":1},"copy":1}` {
		t.Errorf("k8syaml: %s %v", j, err)
	}
}

// An alias inside the node that holds the latest definition is still a cycle,
// but an alias that follows a finished redefinition inside an outer definition
// of the same name is not.
func TestRedefinedAnchorInsideOuterDefinition(t *testing.T) {
	var v any
	if err := oneyaml.Unmarshal([]byte("a: &b\n s: &b 1\n t: *b\n"), &v); err != nil {
		t.Fatalf("alias to a finished inner definition: %v", err)
	}
	if got, want := v, map[string]any{"a": map[string]any{"s": 1, "t": 1}}; !reflect.DeepEqual(norm2(got), norm2(want)) {
		t.Errorf("got %v", got)
	}
	if err := oneyaml.Unmarshal([]byte("a: &b\n s: *b\n"), &v); err == nil {
		t.Error("alias to its own open node must stay an error")
	}

	// An alias inside a node whose definition is newer than an earlier finished
	// definition of the same name is still inside its own definition.
	for _, in := range []string{
		"a: &b 1\nc: &b [ *b ]\n",
		"a: &b [ &b 1, &b [ *b ] ]\n",
	} {
		var x any
		if err := oneyaml.Unmarshal([]byte(in), &x); err == nil {
			t.Errorf("Unmarshal %q: want an error, got %v", in, x)
		}
		var x2 any
		if err := oneyaml.NewDecoder(strings.NewReader(in)).Decode(&x2); err == nil {
			t.Errorf("Decoder %q: want an error, got %v", in, x2)
		}
		if doc, err := oneyaml.Parse([]byte(in)); err == nil {
			var x3 any
			if err := doc.Decode(&x3); err == nil {
				t.Errorf("Document.Decode %q: want an error, got %v", in, x3)
			}
			var x4 any
			if err := doc.Get("", &x4); err == nil {
				t.Errorf("Document.Get %q: want an error, got %v", in, x4)
			}
		}
		var x5 any
		if err := yamlv3.Unmarshal([]byte(in), &x5); err == nil {
			t.Errorf("yamlv3 %q: want an error, got %v", in, x5)
		}
		if j, err := k8syaml.YAMLToJSON([]byte(in)); err == nil {
			t.Errorf("k8syaml %q: want an error, got %s", in, j)
		}
	}
}

func norm2(v any) string { b, _ := json.Marshal(v); return string(b) }
