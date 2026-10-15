package oneyaml

import (
	"errors"
	"testing"
)

// A scalar edit that the in-place path rejects (the kept tag contradicts the new
// value) must leave the tree exactly as it was.
func TestRejectedInPlaceEditKeepsTree(t *testing.T) {
	cases := []struct {
		name, src, ptr string
		v              any
		want           string
	}{
		{"binary to int", "a: !!binary aGVsbG8gd29ybGQ=\n", "/a", 1, "a: 1\n"},
		{"custom tag to bytes", "a: !foo longoldvaluexxxxxxx\nb: 1\nc: 2\n", "/a", []byte("hi"), "a: !!binary aGk=\nb: 1\nc: 2\n"},
		{"timestamp to int", "a: !!timestamp 2001-12-14t21:59:43.10-05:00\nb: 1\n", "/a", 5, "a: 5\nb: 1\n"},
		{"non-specific tag to int", "k: ! a\n", "/k", 7, "k: 7\n"},
		{"non-specific tag keeps a string", "k: ! 12\n", "/k", "x", "k: ! x\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := mustParse(t, c.src)
			if err := d.Set(c.ptr, c.v); err != nil {
				t.Fatal(err)
			}
			if got := string(d.Bytes()); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
			if err := sameAsFreshParse(d); err != nil {
				t.Fatal(err)
			}
			// The value reads back through the decoder.
			var x any
			if err := Unmarshal(d.Bytes(), &x); err != nil {
				t.Fatal(err)
			}
		})
	}
	// The follow-up edit that used to delete a line.
	d := mustParse(t, "a: !foo longoldvaluexxxxxxx\nb: 1\nc: 2\n")
	if err := d.Set("/a", "z"); err != nil {
		t.Fatal(err)
	}
	if got := string(d.Bytes()); got != "a: !foo z\nb: 1\nc: 2\n" {
		t.Fatalf("got %q", got)
	}
}

// A non-specific tag makes a scalar a string, whatever it looks like.
func TestNonSpecificTagIsString(t *testing.T) {
	d := mustParse(t, "a: ! 12\nb: 12\n")
	var x any
	if err := d.Get("/a", &x); err != nil || x != "12" {
		t.Fatalf("a = %#v, %v", x, err)
	}
	if err := d.ApplyPatch([]byte(`[{"op":"test","path":"/a","value":"12"}]`)); err != nil {
		t.Fatal(err)
	}
	if err := d.ApplyPatch([]byte(`[{"op":"test","path":"/a","value":12}]`)); !errors.Is(err, ErrTestFailed) {
		t.Fatalf("got %v", err)
	}
}

func TestStaleNodeIsRefused(t *testing.T) {
	d := mustParse(t, "a: secret\nb: public\nc: 0\n")
	var n Node
	if err := d.Get("/b", &n); err != nil {
		t.Fatal(err)
	}
	if err := d.Remove("/a"); err != nil {
		t.Fatal(err)
	}
	before := string(d.Bytes())
	err := d.Set("/c", n)
	var ee *EditError
	if !errors.Is(err, ErrStaleNode) || !errors.As(err, &ee) {
		t.Fatalf("got %v", err)
	}
	if string(d.Bytes()) != before {
		t.Fatal("document changed")
	}

	d = mustParse(t, "a: [1, 2, 3, 4, 5]\nb: 0\n")
	if err := d.Get("/a/4", &n); err != nil {
		t.Fatal(err)
	}
	if err := d.Set("/a", 1); err != nil {
		t.Fatal(err)
	}
	if err := d.Set("/b", n); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("got %v", err)
	}
	if err := d.Apply(PatchAdd("/c", n)); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("Apply: got %v", err)
	}
	// A fresh node works.
	if err := d.Get("/a", &n); err != nil {
		t.Fatal(err)
	}
	if err := d.Set("/b", n); err != nil {
		t.Fatal(err)
	}
}

// A Node carries what it reads, not a reference to an anchor of its own document.
func TestSetFromNodeWritesAliasesOut(t *testing.T) {
	src := mustParse(t, "zz: &zz 99\nx: *zz\ny: [*zz, 1]\n")
	var x, y Node
	if err := src.Get("/x", &x); err != nil {
		t.Fatal(err)
	}
	if err := src.Get("/y", &y); err != nil {
		t.Fatal(err)
	}
	// Into a document that has an anchor of the same name with another meaning.
	d := mustParse(t, "a: &zz 1\nb: 2\nc: 3\n")
	if err := d.Set("/b", x); err != nil {
		t.Fatal(err)
	}
	if err := d.Set("/c", y); err != nil {
		t.Fatal(err)
	}
	var b int
	var c []any
	if err := d.Get("/b", &b); err != nil || b != 99 {
		t.Fatalf("b = %d, %v\n%s", b, err, d.Bytes())
	}
	if err := d.Get("/c", &c); err != nil || len(c) != 2 || c[0] != 99 {
		t.Fatalf("c = %v, %v\n%s", c, err, d.Bytes())
	}
	// Into a document without the anchor: no dangling alias.
	bare := mustParse(t, "b: 2\n")
	if err := bare.Set("/b", x); err != nil {
		t.Fatal(err)
	}
	if got := string(bare.Bytes()); got != "b: 99\n" {
		t.Fatalf("got %q", got)
	}
}

// The anchor of a mapping key goes with the entry (kit repro).
func TestRemoveKeyAnchorKitRepro(t *testing.T) {
	d := mustParse(t, "&k a: 1\nb: *k\n")
	err := d.Remove("/a")
	if !errors.Is(err, ErrAnchorInUse) || errorCategory(err) != "refused-alias" {
		t.Fatalf("got %v", err)
	}
	if string(d.Bytes()) != "&k a: 1\nb: *k\n" {
		t.Fatal("document changed")
	}
	// Replacing the value leaves the key, and so its anchor, alone.
	if err := d.Set("/a", 2); err != nil {
		t.Fatal(err)
	}
	if got := string(d.Bytes()); got != "&k a: 2\nb: *k\n" {
		t.Fatalf("got %q", got)
	}
}
