package oneyaml

import (
	"fmt"
	"strings"
	"testing"
)

// Renaming the keys of one wide mapping, one after another, must read the keys of
// the mapping a bounded number of times: the key index follows each rename instead
// of being thrown away by it.
func TestRenameRunKeepsKeyIndex(t *testing.T) {
	noSpliceCheck(t)
	const n = 3000
	var src, want strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&src, "key%d: %d\n", i, i)
		fmt.Fprintf(&want, "renamedkey%d: %d\n", i, i)
	}
	run := func(name string, loop func(d *Document)) {
		t.Run(name, func(t *testing.T) {
			d, err := Parse([]byte(src.String()))
			if err != nil {
				t.Fatal(err)
			}
			loop(d)
			if got := string(d.Bytes()); got != want.String() {
				t.Fatalf("wrong text after the renames: %q...", got[:min(len(got), 80)])
			}
			if err := sameAsFreshParse(d); err != nil {
				t.Fatal(err)
			}
			cmp := d.s.ed.cache.Keys.Compared
			if cmp > 20*n {
				t.Fatalf("%d keys read for %d renames", cmp, n)
			}
			// The index still answers for the new names.
			if err := d.Set("/renamedkey7", "seven"); err != nil {
				t.Fatal(err)
			}
			var v string
			if err := d.Get("/renamedkey7", &v); err != nil || v != "seven" {
				t.Fatalf("renamedkey7 = %q, %v", v, err)
			}
			if err := d.Get("/key7", &v); err == nil {
				t.Fatal("the old name is still found")
			}
		})
	}
	run("in-loop", func(d *Document) {
		for k := range d.Root().Entries() {
			old := k.Value()
			if err := d.Rename("/"+old, "renamed"+old); err != nil {
				t.Fatal(err)
			}
		}
	})
	run("collect-then-rename", func(d *Document) {
		var keys []string
		for k := range d.Root().Entries() {
			keys = append(keys, k.Value())
		}
		for _, old := range keys {
			if err := d.Rename("/"+old, "renamed"+old); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// A rename that gives a key the name of another is refused, and one that goes back
// to an old name works, with the index in use.
func TestRenameWithKeyIndexRefusesTakenNames(t *testing.T) {
	noSpliceCheck(t)
	var src strings.Builder
	for i := 0; i < 700; i++ {
		fmt.Fprintf(&src, "key%d: %d\n", i, i)
	}
	d, err := Parse([]byte(src.String()))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ { // enough edits to build the index
		if err := d.Rename(fmt.Sprintf("/key%d", i), fmt.Sprintf("n%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Rename("/key20", "key21"); err == nil {
		t.Fatal("a key was renamed to the name of another")
	}
	if err := d.Rename("/n3", "key3"); err != nil {
		t.Fatal(err)
	}
	if err := d.Rename("/key3", "key2000"); err != nil {
		t.Fatal(err)
	}
	if err := d.Rename("/key2000", "n4"); err == nil {
		t.Fatal("a key was renamed to a name renamed earlier")
	}
	var v int
	if err := d.Get("/key2000", &v); err != nil || v != 3 {
		t.Fatalf("key2000 = %d, %v", v, err)
	}
	if err := sameAsFreshParse(d); err != nil {
		t.Fatal(err)
	}
}
