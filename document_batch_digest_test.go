package oneyaml

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/edit"
)

// A pass over a large stream checks its result with a digest of the nodes that
// stay; for every subset of the keys of every shape, and for the shapes in which
// a survivor would change, it must accept and refuse exactly as the pass that
// holds both trees does, and give the same bytes.
func TestBatchDigestAgreesWithTwoTrees(t *testing.T) {
	shapes := append([]struct {
		name, src, parent string
		keys              []string
	}(nil), batchShapes...)
	// Removals that leave a comment line behind, which the new parse could read as
	// part of a surviving scalar.
	survivor := "s:\n  q: |\n    t\ngone: 12\n      # note\nkeep: 1\n"
	var keys []string
	for i := 0; i < 8; i++ {
		keys = append(keys, fmt.Sprintf("p%d", i))
		survivor += fmt.Sprintf("p%d: %d\n", i, i)
	}
	shapes = append(shapes, struct {
		name, src, parent string
		keys              []string
	}{"survivor", survivor, "", append(keys, "gone")})
	shapes = append(shapes, struct {
		name, src, parent string
		keys              []string
	}{"block scalar then comment", "a: |\n  t\nb: 1\n    # c\nc: 3\nd: 4\ne: 5\nf: 6\ng: 7\nh: 8\n", "", []string{"b", "c", "d", "e", "f", "g", "h"}})

	oldMin, oldExact := batchMin, batchExactBelow
	defer func() { batchMin, batchExactBelow = oldMin, oldExact }()
	batchMin = 1
	accepted, refused := 0, 0
	for _, sh := range shapes {
		n := min(len(sh.keys), 10)
		for mask := 1; mask < 1<<n; mask++ {
			var keys []string
			for i, k := range sh.keys[:n] {
				if mask>>i&1 == 1 {
					keys = append(keys, k)
				}
			}
			merge, rfc := removalPatches(sh.parent, keys)
			for kind, p := range map[string][]byte{"merge": merge, "6902": rfc} {
				if p == nil {
					continue
				}
				run := func(exact int) (*Document, error) {
					batchExactBelow = exact
					d := mustParse(t, sh.src)
					if kind == "merge" {
						return d, d.ApplyMergePatch(p)
					}
					return d, d.ApplyPatch(p)
				}
				two, errTwo := run(1 << 30)
				dig, errDig := run(0)
				if (errTwo == nil) != (errDig == nil) || !bytes.Equal(two.Bytes(), dig.Bytes()) {
					t.Fatalf("%s %s %v: two trees %v, %q; digest %v, %q", sh.name, kind, keys, errTwo, two.Bytes(), errDig, dig.Bytes())
				}
				var b2, bd int
				if two.s.ed != nil {
					b2 = two.s.ed.work.batches
				}
				if dig.s.ed != nil {
					bd = dig.s.ed.work.batches
				}
				if b2 != bd {
					t.Fatalf("%s %s %v: the pass ran %d times with two trees and %d with the digest", sh.name, kind, keys, b2, bd)
				}
				if bd > 0 {
					accepted++
				} else {
					refused++
				}
				if err := sameAsFreshParse(dig); err != nil {
					t.Fatalf("%s %s %v: %v", sh.name, kind, keys, err)
				}
			}
		}
	}
	t.Logf("pass accepted %d times and declined %d times, identically", accepted, refused)
	if accepted < 500 {
		t.Fatalf("the pass ran %d times; the digest is not being tested", accepted)
	}
}

// The digest must refuse a text whose tree is not the old tree without the removed
// entries, and leave the stream as it was.
func TestBatchDigestRefusesAndRestores(t *testing.T) {
	oldMin, oldExact := batchMin, batchExactBelow
	defer func() { batchMin, batchExactBelow = oldMin, oldExact }()
	batchMin, batchExactBelow = 1, 0
	var sb strings.Builder
	sb.WriteString("s:\n  q: |\n    t\n")
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&sb, "p%d: %d\n", i, i)
	}
	src := sb.String()
	d := mustParse(t, src)
	s := d.s
	s.es()
	// Make the text a removal would leave read differently by lying about it: ask
	// commitRemoval for a text that keeps a different value.
	removed := map[int32]struct{}{}
	root := d.Root()
	for k := range root.Entries() {
		if k.Value() == "p3" {
			removed[k.i] = struct{}{}
		}
	}
	out := []byte(strings.Replace(src, "p3: 3\n", "", 1))
	out = bytes.Replace(out, []byte("p5: 5"), []byte("p5: 6"), 1)
	treeBefore := append([]byte(nil), fmt.Sprint(s.tree.Nodes)...)
	if s.commitRemoval(root.i, removed, cutRepls(src, "p3: 3\n"), out) {
		t.Fatal("a text that changes a surviving value was accepted")
	}
	if string(s.src) != src || fmt.Sprint(s.tree.Nodes) != string(treeBefore) {
		t.Fatal("the stream changed after a refused pass")
	}
	if err := sameAsFreshParse(d); err != nil {
		t.Fatal(err)
	}
	// And the same text without the change is accepted.
	out = []byte(strings.Replace(src, "p3: 3\n", "", 1))
	if !s.commitRemoval(root.i, removed, cutRepls(src, "p3: 3\n"), out) {
		t.Fatal("the plain removal was refused")
	}
	if string(s.src) != string(out) {
		t.Fatal("the text was not replaced")
	}
	if err := sameAsFreshParse(d); err != nil {
		t.Fatal(err)
	}
}

// Every kind of change to a node that stays is noticed: text, anchor, tag, alias,
// style, structure.
func TestBatchDigestNoticesEveryKindOfChange(t *testing.T) {
	oldMin, oldExact := batchMin, batchExactBelow
	defer func() { batchMin, batchExactBelow = oldMin, oldExact }()
	batchMin, batchExactBelow = 1, 0
	src := "gone: 1\nanch: &a [1, 2]\ntagged: !!str 5\nref: *a\nq: 'single'\nn:\n  x: 1\n  y: 2\nblock: |\n  text\n"
	for _, c := range []struct{ name, from, to string }{
		{"scalar text", "q: 'single'", "q: 'singel'"},
		{"anchor name", "&a [1, 2]", "&b [1, 2]"},
		{"anchor dropped", "&a [1, 2]", "[1, 2]"},
		{"tag", "!!str 5", "!!int 5"},
		{"tag dropped", "!!str 5", "5"},
		{"alias name", "*a", "*b"},
		{"style", "'single'", "\"single\""},
		{"nesting", "  y: 2\n", "y: 2\n"},
		{"flow item", "[1, 2]", "[1, 2, 3]"},
		{"block text", "  text\n", "  texts\n"},
		{"key", "tagged:", "tagged2:"},
	} {
		d := mustParse(t, src)
		s := d.s
		s.es()
		removed := map[int32]struct{}{}
		for k := range d.Root().Entries() {
			if k.Value() == "gone" {
				removed[k.i] = struct{}{}
			}
		}
		out := strings.Replace(src, "gone: 1\n", "", 1)
		if !strings.Contains(out, c.from) {
			t.Fatalf("%s: fixture", c.name)
		}
		out = strings.Replace(out, c.from, c.to, 1)
		if s.commitRemoval(1, removed, cutRepls(src, "gone: 1\n"), []byte(out)) {
			t.Errorf("%s: a changed survivor was accepted", c.name)
		}
		if string(s.src) != src {
			t.Errorf("%s: the text changed after a refusal", c.name)
		}
		if err := sameAsFreshParse(d); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// cutRepls lists the ranges of src that hold the given texts, each cut out.
func cutRepls(src string, subs ...string) []edit.Repl {
	var out []edit.Repl
	for _, sub := range subs {
		i := strings.Index(src, sub)
		out = append(out, edit.Repl{Start: i, End: i + len(sub)})
	}
	slices.SortFunc(out, func(a, b edit.Repl) int { return a.Start - b.Start })
	return out
}

// The text can be the old one with the cut ranges taken out and still read as a
// different tree, when the ranges are not the removed entries. The digest sees that
// a survivor is gone or has moved, and the stream is left as it was.
func TestBatchDigestNoticesStructuralChange(t *testing.T) {
	oldMin, oldExact := batchMin, batchExactBelow
	defer func() { batchMin, batchExactBelow = oldMin, oldExact }()
	batchMin, batchExactBelow = 1, 0
	const base = "gone: 1\nn:\n  x: 1\n  y: 2\nanch: &a [1, 2]\nref: *a\nlast: 'z'\n"
	for _, c := range []struct {
		name string
		src  string
		cut  []string
	}{
		// The removed entry has a continuation line that the cut leaves behind: the
		// text still parses, and a surviving scalar now reads longer.
		{"a continuation joins a scalar", "k: v\ngone: 1\n  cont\nz: 2\n", []string{"gone: 1\n"}},
		{"a continuation fills an empty value", "k:\ngone: 1\n  cont\nz: 2\n", []string{"gone: 1\n"}},
		{"a continuation fills an empty tagged value", "k: !!str\ngone: 1\n  cont\nz: 2\n", []string{"gone: 1\n"}},
		{"a continuation fills an empty anchored value", "k: &a\ngone: 1\n  cont\nz: 2\n", []string{"gone: 1\n"}},
		{"a continuation joins a quoted scalar", "k: 'v\n  w'\ngone: 1\n  cont\nz: 2\nk2: 'a'\n", []string{"gone: 1\n"}},
		{"a continuation joins an alias key line", "a: &x 1\nk: *x\ngone: 1\n  cont\nz: 2\n", []string{"gone: 1\n"}},
		{"a survivor key is cut too", "", []string{"gone: 1\nn:\n"}},
		{"a value is cut", "", []string{"gone: 1\n", "  x: 1\n"}},
		{"the cut ends inside a scalar", "", []string{"gone: 1\nn:\n  x: "}},
		{"an anchor is cut", "", []string{"gone: 1\nn:\n  x: 1\n  y: 2\nanch: &a "}},
		{"an alias is cut", "", []string{"gone: 1\n", "*a"}},
		{"indentation is cut", "", []string{"gone: 1\nn:\n  "}},
	} {
		src := c.src
		if src == "" {
			src = base
		}
		d := mustParse(t, src)
		s := d.s
		s.es()
		removed := map[int32]struct{}{}
		for k := range d.Root().Entries() {
			if k.Value() == "gone" {
				removed[k.i] = struct{}{}
			}
		}
		repls := cutRepls(src, c.cut...)
		out := make([]byte, 0, len(src))
		at := 0
		for _, r := range repls {
			out = append(out, src[at:r.Start]...)
			at = r.End
		}
		out = append(out, src[at:]...)
		if !outIsSrcWithout([]byte(src), out, repls) {
			t.Fatalf("%s: fixture", c.name)
		}
		if s.commitRemoval(1, removed, repls, out) {
			t.Errorf("%s: a changed tree was accepted", c.name)
		}
		if string(s.src) != src {
			t.Errorf("%s: the text changed after a refusal", c.name)
		}
		if err := sameAsFreshParse(d); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestRemovalMapShift(t *testing.T) {
	rm := newRemovalMap([]edit.Repl{{Start: 10, End: 14}, {Start: 20, End: 21}})
	for _, c := range []struct {
		x    int32
		del  int32
		inOK bool
	}{{0, 0, true}, {10, 0, true}, {11, 0, false}, {13, 0, false}, {14, 4, true}, {20, 4, true}, {21, 5, true}, {99, 5, true}} {
		if d, ok := rm.shift(c.x); ok != c.inOK || (ok && d != c.del) {
			t.Errorf("shift(%d) = %d, %v; want %d, %v", c.x, d, ok, c.del, c.inOK)
		}
	}
}

// Two trees with the same kinds in the same order and the same leaf spans, whose
// collections hold different numbers of children, are different trees; the digest
// must see the counts. Here a cut of two spaces turns a sequence of one item that
// holds a sequence of two into a sequence of two whose first holds one.
func TestBatchDigestNoticesChildCounts(t *testing.T) {
	oldMin, oldExact := batchMin, batchExactBelow
	defer func() { batchMin, batchExactBelow = oldMin, oldExact }()
	batchMin, batchExactBelow = 1, 0
	const src = "gone: 1\nk:\n- - a\n  - b\n"
	d := mustParse(t, src)
	s := d.s
	s.es()
	removed := map[int32]struct{}{}
	for k := range d.Root().Entries() {
		if k.Value() == "gone" {
			removed[k.i] = struct{}{}
		}
	}
	repls := cutRepls(src, "gone: 1\n", "  ")
	out := []byte("k:\n- - a\n- b\n")
	if !outIsSrcWithout([]byte(src), out, repls) {
		t.Fatal("fixture")
	}
	if s.commitRemoval(1, removed, repls, out) {
		t.Error("a tree with other child counts was accepted")
	}
	if string(s.src) != src {
		t.Error("the text changed after a refusal")
	}
	if err := sameAsFreshParse(d); err != nil {
		t.Error(err)
	}
}
