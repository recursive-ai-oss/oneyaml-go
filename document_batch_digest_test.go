package oneyaml

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
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
	if s.commitRemoval(root.i, removed, out) {
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
	if !s.commitRemoval(root.i, removed, out) {
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
		if s.commitRemoval(1, removed, []byte(out)) {
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
