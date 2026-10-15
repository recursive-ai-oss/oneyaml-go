package edit

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"oneyaml.dev/go/internal/cst"
)

// An add reads a bounded number of the siblings to choose its style, however many
// the container has: the work of one add does not grow with the container.
func TestAddReadsBoundedSiblings(t *testing.T) {
	reads := func(entries int, seq bool) int {
		var b strings.Builder
		for i := 0; i < entries; i++ {
			if seq {
				fmt.Fprintf(&b, "- %d\n", i)
			} else {
				fmt.Fprintf(&b, "k%d: %d\n", i, i)
			}
		}
		src := []byte(b.String())
		var tree cst.Tree
		if err := cst.Build(&tree, src, -1); err != nil {
			t.Fatal(err)
		}
		c := NewCtx(&tree, src, 0)
		ptr := "new"
		if seq {
			ptr = "-"
		}
		loc, err := c.Resolve([]string{ptr}, true)
		if err != nil {
			t.Fatal(err)
		}
		v := Value{Kind: String, Text: "text"}
		if _, err := c.Add(loc, &v, Options{}); err != nil {
			t.Fatal(err)
		}
		return int(c.hintReads)
	}
	for _, seq := range []bool{false, true} {
		small, large := reads(200, seq), reads(20000, seq)
		if large > hintReach || large != small {
			t.Errorf("seq=%v: an add decoded %d siblings in 200 entries and %d in 20000 (reach %d)", seq, small, large, hintReach)
		}
	}
}

// A nested collection far from the start of a mapping, or near its end, still
// gives the layout of new nested collections.
func TestDiscoverFindsNestedCollectionAtTheEnd(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "k%d: v\n", i)
	}
	b.WriteString("x:\n    y: 1\n    z:\n        - 1\n")
	src := []byte(b.String())
	var tree cst.Tree
	if err := cst.Build(&tree, src, -1); err != nil {
		t.Fatal(err)
	}
	c := NewCtx(&tree, src, 0)
	l := c.newLayout(0, Options{})
	c.discover(l, c.Root())
	if l.step != 4 {
		t.Fatalf("step %d, want 4", l.step)
	}
}

// A Ctx is made for every edit, so its size is memory per edit: keep it in the
// 112-byte allocation class.
func TestCtxStaysInItsSizeClass(t *testing.T) {
	if n := unsafe.Sizeof(Ctx{}); n > 112 {
		t.Fatalf("Ctx is %d bytes; keep it at 112 or less", n)
	}
}
