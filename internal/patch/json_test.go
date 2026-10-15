package patch

import (
	"errors"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/edit"
)

func TestParseValue(t *testing.T) {
	v, err := ParseValue([]byte(` {"a": [1, -2.5e3, true, null, "x\u00e9\ud83d\ude00\n"], "b": {}, "c": []} `), 0)
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != edit.Map || len(v.Items) != 6 || v.Items[0].Text != "a" || v.Items[2].Text != "b" {
		t.Fatalf("shape: %+v", v)
	}
	a := v.Items[1]
	kinds := []edit.Kind{edit.Int, edit.Float, edit.Bool, edit.Null, edit.String}
	for i, k := range kinds {
		if a.Items[i].Kind != k {
			t.Errorf("item %d kind %d, want %d", i, a.Items[i].Kind, k)
		}
	}
	if a.Items[1].Text != "-2.5e3" || a.Items[4].Text != "xé😀\n" {
		t.Errorf("texts: %q %q", a.Items[1].Text, a.Items[4].Text)
	}
}

func TestParseValueRejects(t *testing.T) {
	for _, in := range []string{
		``, `[`, `]`, `{"a"}`, `{"a":1,}`, `[1,]`, `[1 2]`, `{"a":1 "b":2}`, `{1:2}`, `01`, `1.`, `.5`, `-`, `+1`, `1e`,
		`tru`, `nul`, `"abc`, "\"a\x01\"", `"\x"`, `"\ud800"`, `"\udc00"`, `"\ud800\u0041"`, `"\u12"`, "\"\xff\"",
		`{"a":1,"a":2}`, `1 2`, `[1]]`, `'a'`, `NaN`,
	} {
		if _, err := ParseValue([]byte(in), 0); !errors.Is(err, ErrSyntax) {
			t.Errorf("%q: got %v", in, err)
		}
	}
}

func TestParseValueDepth(t *testing.T) {
	deep := strings.Repeat("[", 100000) + strings.Repeat("]", 100000)
	if _, err := ParseValue([]byte(deep), 0); !errors.Is(err, ErrDepth) {
		t.Fatalf("got %v", err)
	}
	ok := strings.Repeat("[", 50) + strings.Repeat("]", 50)
	if _, err := ParseValue([]byte(ok), 50); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseValue([]byte(ok), 49); !errors.Is(err, ErrDepth) {
		t.Fatalf("got %v", err)
	}
	// Many members: the duplicate check switches to a set.
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 100; i++ {
		b.WriteString(`"k` + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `":1,`)
	}
	b.WriteString(`"kaa":2}`)
	if _, err := ParseValue([]byte(b.String()), 0); !errors.Is(err, ErrSyntax) {
		t.Fatalf("duplicate among many members: got %v", err)
	}
}

func TestParseOps(t *testing.T) {
	ops, _, err := ParseOps([]byte(`[{"op":"add","path":"/a","value":null},{"op":"move","from":"/a","path":"/b"},{"op":"remove","path":"/b"}]`), 0)
	if err != nil || len(ops) != 3 || ops[0].Value.Kind != edit.Null || ops[1].From != "/a" {
		t.Fatalf("%+v %v", ops, err)
	}
	_, bad, err := ParseOps([]byte(`[{"op":"remove","path":"/a"},{"op":"add","path":"/b"}]`), 0)
	if !errors.Is(err, ErrSyntax) || bad != 1 {
		t.Fatalf("bad %d, %v", bad, err)
	}
}
