package oneyaml

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

// getAll decodes the whole first document of d.
func getAll(t *testing.T, d *Document) any {
	t.Helper()
	var x any
	if err := d.Get("", &x); err != nil {
		t.Fatal(err)
	}
	return x
}

// An empty value that ends a nested explicit-key mapping must not be placed after
// a colon that belongs to an outer entry, or the nested mapping looks as long as
// that entry and an edit of it removes the entry.
func TestEditKeepsEntryAfterNestedExplicitKey(t *testing.T) {
	const src = "0:\n : ? 0\n:"
	d := mustParse(t, src)
	before := getAll(t, d)
	if err := d.Set("/0", "x"); err != nil {
		t.Fatal(err)
	}
	want := map[any]any{0: "x", nil: nil}
	if got := getAll(t, d); !reflect.DeepEqual(got, want) {
		t.Fatalf("after Set(/0): %#v, want %#v (output %q, before %#v)", got, want, d.Bytes(), before)
	}
	if err := sameAsFreshParse(d); err != nil {
		t.Fatal(err)
	}
	// Adding below the nested mapping lands inside it, not after the outer entry.
	d = mustParse(t, src)
	if err := d.Set("/0/-", "x"); err != nil {
		t.Fatal(err)
	}
	var v any
	if err := d.Get("/0", &v); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(map[any]any)[nil]; !ok || len(getAll(t, d).(map[any]any)) != 2 {
		t.Fatalf("output %q reads as %#v", d.Bytes(), getAll(t, d))
	}
}

// A block scalar that ends the file on a blank-looking line with no break already
// has the break after its content, so appending after it must not change its
// header to strip.
func TestAppendAfterBlockScalarEndingInBlankLine(t *testing.T) {
	d := mustParse(t, "? |\n 0\n ")
	if err := d.Set("/-", "x"); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"0\n": nil, "-": "x"}
	if got := getAll(t, d); !reflect.DeepEqual(got, want) {
		t.Fatalf("output %q reads as %#v, want %#v", d.Bytes(), got, want)
	}
}

// Removing the entry of an empty explicit key, or the only entry of a flow
// mapping that has an empty key, reads back as the document without it, or is
// refused with the bytes untouched.
func TestRemoveEmptyKeyEntries(t *testing.T) {
	cases := []struct {
		src, ptr string
		want     any // nil: the edit may be refused
	}{
		{" ?\n new0: null", "/", map[string]any{"new0": nil}},
		{" ?\n '-': null\n 'new0': {}", "/", map[string]any{"-": nil, "new0": map[string]any{}}},
		{"?\n::", "/", nil},
		{"[0,: 0]", "/1", []any{0}},
		{"[: 0]", "/0", []any{}},
	}
	for _, c := range cases {
		d := mustParse(t, c.src)
		before := getAll(t, d)
		err := d.Remove(c.ptr)
		if err != nil {
			if string(d.Bytes()) != c.src || !reflect.DeepEqual(getAll(t, d), before) {
				t.Errorf("%q: refused edit changed the document: %q", c.src, d.Bytes())
			}
			if c.want != nil {
				t.Errorf("%q: unexpected error %v", c.src, err)
			}
			continue
		}
		var got any
		if err := Unmarshal(d.Bytes(), &got); err != nil {
			t.Errorf("%q: output %q does not parse: %v", c.src, d.Bytes(), err)
			continue
		}
		if c.want != nil && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: output %q reads as %#v, want %#v", c.src, d.Bytes(), got, c.want)
		}
		if err := sameAsFreshParse(d); err != nil {
			t.Errorf("%q: %v", c.src, err)
		}
	}
}

// An edit of a UTF-16 stream changes only the text of the edited value; every
// other byte stays as written.
func TestUTF16EditKeepsOtherBytes(t *testing.T) {
	var in []byte
	for _, u := range utf16.Encode([]rune("\ufeff- a\n- b\n- c\n")) {
		in = append(in, byte(u>>8), byte(u))
	}
	d, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Set("/1", "x"); err != nil {
		t.Fatal(err)
	}
	var want []byte
	for _, u := range utf16.Encode([]rune("\ufeff- a\n- x\n- c\n")) {
		want = append(want, byte(u>>8), byte(u))
	}
	if !bytes.Equal(d.Bytes(), want) {
		t.Fatalf("got % x, want % x", d.Bytes(), want)
	}
}

// A value set after an explicit key whose content starts on a later line goes
// after that entry's colon, and a key that holds an implicit mapping gets its
// value after its own colon.
func TestSetValueOfEntryWithLateKey(t *testing.T) {
	for _, c := range []struct{ src, ptr, want string }{
		{"?\n  a\n:\nb: 1\n", "/a", "?\n  a\n: 1\nb: 1\n"},
		{"? # c\n  a\n:\nb: 1\n", "/a", "? # c\n  a\n: 1\nb: 1\n"},
		{"?\n  !!str a\n:\nb: 1\n", "/a", "?\n  !!str a\n: 1\nb: 1\n"},
	} {
		d := mustParse(t, c.src)
		if err := d.Set(c.ptr, 1); err != nil {
			t.Errorf("%q: %v", c.src, err)
			continue
		}
		if got := string(d.Bytes()); got != c.want {
			t.Errorf("%q: got %q, want %q", c.src, got, c.want)
		}
		if err := sameAsFreshParse(d); err != nil {
			t.Errorf("%q: %v", c.src, err)
		}
	}
}

// Adding, removing and adding again below a mapping with an explicit key whose
// content starts on a later line must leave text that parses and reads back.
func TestPatchAroundLateExplicitKey(t *testing.T) {
	var pad string
	for i := 0; i < 200; i++ {
		pad += fmt.Sprintf("p%02d: 0\n", i)
	}
	d := mustParse(t, pad+"x:\n  ?\n    a\n  :\n  b: 1\n")
	if err := d.ApplyPatch([]byte(`[{"op":"add","path":"/x/b","value":"z"},{"op":"remove","path":"/x/b"},{"op":"add","path":"/nk1","value":[1]}]`)); err != nil {
		t.Fatal(err)
	}
	if want := "x:\n  ?\n    a\n  :\nnk1:\n  - 1\n"; !strings.HasSuffix(string(d.Bytes()), want) {
		t.Fatalf("tail is %q, want %q", d.Bytes()[len(pad):], want)
	}
	if _, err := Parse(d.Bytes()); err != nil {
		t.Fatalf("output does not parse: %v", err)
	}
}
