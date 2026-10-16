package oneyaml_test

import (
	"errors"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
)

// A merge patch written in YAML gives the result of the same patch in JSON.
func TestMergePatchYAMLMatchesJSON(t *testing.T) {
	const src = "# top\nname: api   # keep\nlimits:\n  cpu: 1\n  mem: 2\ntags: [a, b]\nold: 1\n"
	for _, c := range []struct{ yaml, json string }{
		{"name: web\nold: null\n", `{"name":"web","old":null}`},
		{"limits:\n  cpu: 4\n  disk: 9\ntags: [c]\n", `{"limits":{"cpu":4,"disk":9},"tags":["c"]}`},
		{"limits: {mem: null}\nnew: {x: [1, 2]}\n", `{"limits":{"mem":null},"new":{"x":[1,2]}}`},
		{"name: 31\nratio: 1.50\n", `{"name":31,"ratio":1.50}`},
		{"name: !!str 12\n", `{"name":"12"}`},
		{"name: ! 12\n", `{"name":"12"}`},
		{"---\nname: doc\n", `{"name":"doc"}`},
		{"just text\n", `"just text"`},
	} {
		want := mustParseDoc(t, src)
		if err := want.ApplyMergePatch([]byte(c.json)); err != nil {
			t.Fatalf("%s: %v", c.json, err)
		}
		got := mustParseDoc(t, src)
		if err := got.ApplyMergePatch([]byte(c.yaml)); err != nil {
			t.Fatalf("%q: %v", c.yaml, err)
		}
		if string(got.Bytes()) != string(want.Bytes()) {
			t.Errorf("%q:\n got %q\nwant %q", c.yaml, got.Bytes(), want.Bytes())
		}
	}
}

func TestMergePatchYAMLRefusals(t *testing.T) {
	const src = "a: 1\nb: {c: 2}\n"
	for _, c := range []struct{ name, patch string }{
		{"alias", "x: &p 1\ny: *p\n"},
		{"local tag", "x: !local 1\n"},
		{"collection key", "? [1]\n: 2\n"},
		{"repeated key", "x: 1\nx: 2\n"},
		{"key repeated by value", "1: a\n0x1: b\n"},
		{"bad escape", "x: \"\\ud800\"\n"},
		{"two documents", "x: 1\n---\ny: 2\n"},
		{"syntax", "x: [1\ny: 2\n"},
	} {
		d := mustParseDoc(t, src)
		err := d.ApplyMergePatch([]byte(c.patch))
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		var ee *oneyaml.EditError
		if !errors.As(err, &ee) || ee.Op != "merge-patch" {
			t.Errorf("%s: got %T %v", c.name, err, err)
		}
		if c.name != "syntax" && !errors.Is(err, oneyaml.ErrInvalidPatch) {
			t.Errorf("%s: not ErrInvalidPatch: %v", c.name, err)
		}
		if string(d.Bytes()) != src {
			t.Errorf("%s changed the document: %q", c.name, d.Bytes())
		}
	}
}

// Text that starts like JSON keeps its JSON errors, and a YAML merge patch that
// nests past the depth limit is a limit error.
func TestMergePatchJSONLookingTextStaysJSON(t *testing.T) {
	d := mustParseDoc(t, "a: 1\n")
	for _, p := range []string{`{a: 1}`, `[a, b]`, `{"a": 1,}`, ``, "# only a comment\n"} {
		err := d.ApplyMergePatch([]byte(p))
		if err == nil {
			t.Errorf("%q: accepted", p)
			continue
		}
		if !strings.Contains(err.Error(), "JSON") && p != "" && p[0] != '#' {
			t.Errorf("%q: not a JSON error: %v", p, err)
		}
	}
	deep := ""
	for i := 0; i < 50; i++ {
		deep += strings.Repeat(" ", i) + "k:\n"
	}
	deep += strings.Repeat(" ", 50) + "k: 1\n"
	err := d.ApplyMergePatch([]byte(deep))
	if err != nil {
		t.Fatalf("depth within the limit: %v", err)
	}
	e := mustParseDoc(t, "a: 1\n", oneyaml.Limits(oneyaml.LimitSet{MaxDepth: 20}))
	var le *oneyaml.LimitError
	if err := e.ApplyMergePatch([]byte(deep)); !errors.As(err, &le) {
		t.Fatalf("depth past the limit: %v", err)
	}
}

func mustParseDoc(t *testing.T, src string, opts ...oneyaml.Option) *oneyaml.Document {
	t.Helper()
	d, err := oneyaml.Parse([]byte(src), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
