package oneyaml

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// readBack decodes the document's text, the way a user reads the result of an edit.
func readBack(t *testing.T, d *Document) any {
	t.Helper()
	var got any
	if err := Unmarshal(d.Bytes(), &got); err != nil {
		t.Fatalf("edited text does not decode: %v\n%s", err, d.Bytes())
	}
	return got
}

// A sequence item set to a value that reads differently in the form the edit
// first tried: an edit that commits must read back exactly, or be refused.
func TestRootSequenceEditReadsBackTheEditedItem(t *testing.T) {
	src := "- a\n- |+\n  b\n\n" + strings.Repeat("- - |+\n    tail\n\n", 30)
	want := func() []any {
		var x []any
		if err := Unmarshal([]byte(src), &x); err != nil {
			t.Fatal(err)
		}
		return x
	}
	for _, mode := range []string{"set", "patch"} {
		d := mustParse(t, src)
		var err error
		if mode == "set" {
			err = d.Set("/1", []any{"tail\n\n"})
		} else {
			err = d.ApplyPatch([]byte(`[{"op":"replace","path":"/1","value":["tail\n\n"]}]`))
		}
		if err != nil {
			continue // a refusal is allowed; a wrong commit is not
		}
		w := want()
		w[1] = []any{"tail\n\n"}
		got := readBack(t, d)
		if !reflect.DeepEqual(got, any(w)) {
			t.Fatalf("%s: item 1 reads back %#v, want %#v\n%q", mode, got.([]any)[1], w[1], d.Bytes())
		}
		if err := sameAsFreshParse(d); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
	}
}

// Edits by index on a sequence that is the root of the document are written
// through a region, and are checked against the right item.
func TestRootSequenceEditsUseARegion(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "- item %d\n", i)
	}
	src := b.String()
	cases := []struct {
		name string
		do   func(d *Document) error
		edit func(x []any) []any
	}{
		{"set collection", func(d *Document) error { return d.Set("/150", []any{1, 2}) },
			func(x []any) []any { x[150] = []any{1, 2}; return x }},
		{"remove", func(d *Document) error { return d.Remove("/150") },
			func(x []any) []any { return append(x[:150], x[151:]...) }},
		{"append", func(d *Document) error { return d.Set("/-", map[string]any{"k": 1}) },
			func(x []any) []any { return append(x, map[string]any{"k": 1}) }},
		{"patch add", func(d *Document) error {
			return d.ApplyPatch([]byte(`[{"op":"add","path":"/150","value":{"k":[1,2]}}]`))
		}, func(x []any) []any {
			x = append(x, nil)
			copy(x[151:], x[150:])
			x[150] = map[string]any{"k": []any{1, 2}}
			return x
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var x []any
			if err := Unmarshal([]byte(src), &x); err != nil {
				t.Fatal(err)
			}
			want := c.edit(x)
			d := mustParse(t, src)
			w := d.s.es().work.wholes
			if err := c.do(d); err != nil {
				t.Fatal(err)
			}
			if n := d.s.es().work.wholes - w; n != 0 {
				t.Errorf("parsed the whole stream %d times", n)
			}
			got := readBack(t, d)
			if !reflect.DeepEqual(got, any(want)) {
				t.Fatalf("reads back differently\n%q", d.Bytes()[:200])
			}
			if err := sameAsFreshParse(d); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A new nested collection copies the indentation of its siblings, however far
// into the mapping the first nested one is.
func TestNewNestedCollectionCopiesLayoutFarIntoAMapping(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "k%d: v\n", i)
	}
	b.WriteString("x:\n    y: 1\n    z:\n        - 1\n")
	d := mustParse(t, b.String())
	if err := d.Set("/new", map[string]any{"a": map[string]any{"b": 1}}); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if tail := out[strings.Index(out, "new:"):]; tail != "new:\n    a:\n        b: 1\n" {
		t.Fatalf("got %q", tail)
	}
	var got map[string]any
	if err := d.Get("/new", &got); err != nil || !reflect.DeepEqual(got, map[string]any{"a": map[string]any{"b": 1}}) {
		t.Fatalf("reads back %#v, %v", got, err)
	}
}

// Removing the entry of a flow pair mapping reads back as the document without it.
func TestRemoveFromFlowPairs(t *testing.T) {
	cases := []struct{ src, ptr, want string }{
		{"[: 0]\n", "/0", "[]"},
		{"[: 0]\n", "/0/", "[{}]"},
		{"[0,: 0]\n", "/0", "[: 0]"},
		{"[0,: 0]\n", "/1", "[0]"},
		{"[0, : 0]\n", "/1", "[0]"},
		{"[0,: 0]\n", "/1/", "[0, {}]"},
	}
	for _, c := range cases {
		d := mustParse(t, c.src)
		if err := d.Remove(c.ptr); err != nil {
			t.Fatalf("%q remove %s: %v", c.src, c.ptr, err)
		}
		var want any
		if err := Unmarshal([]byte(c.want), &want); err != nil {
			t.Fatal(err)
		}
		if got := readBack(t, d); !reflect.DeepEqual(got, want) {
			t.Fatalf("%q remove %s: reads back %#v, want %#v (text %q)", c.src, c.ptr, got, want, d.Bytes())
		}
		if err := sameAsFreshParse(d); err != nil {
			t.Fatal(err)
		}
	}
}

// A pointer that is not valid inside a patch makes the patch invalid, and is still
// an invalid pointer.
func TestPatchWithBadPointerIsInvalid(t *testing.T) {
	for _, p := range []string{
		`[{"op":"add","path":"a","value":1}]`,
		`[{"op":"replace","path":"a","value":1}]`,
		`[{"op":"remove","path":"a"}]`,
		`[{"op":"test","path":"a","value":1}]`,
		`[{"op":"copy","from":"a","path":"/b"}]`,
		`[{"op":"move","from":"/a","path":"b"}]`,
	} {
		d := mustParse(t, "a: 1\n")
		err := d.ApplyPatch([]byte(p))
		if !errors.Is(err, ErrInvalidPatch) || !errors.Is(err, ErrInvalidPointer) {
			t.Errorf("%s: got %v", p, err)
		}
		if string(d.Bytes()) != "a: 1\n" {
			t.Errorf("%s: document changed", p)
		}
	}
	d := mustParse(t, "a: 1\n")
	if err := d.Set("a", 1); !errors.Is(err, ErrInvalidPointer) {
		t.Errorf("Set: %v", err)
	}
}
