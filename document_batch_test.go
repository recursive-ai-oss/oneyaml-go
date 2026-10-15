package oneyaml

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// batchShapes are documents in which runs of entries are removed from a mapping.
// Each has a mapping at the pointer in the second column whose keys are in the
// third.
var batchShapes = []struct {
	name, src, parent string
	keys              []string
}{
	{"plain", "a: 1\nb: 2\nc: 3\nd: 4\ne: 5\nf: 6\n", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"comments", "# top\na: 1 # one\n# about b\nb: 2\n\n# about c\n\nc: 3\n  # indented\nd: 4\n# tail\ne: 5\nf: 6 # end\n# last\n", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"blank lines", "a: 1\n\n\nb: 2\n\nc: 3\nd: 4\n\ne: 5\n\n\nf: 6\n", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"no final break", "a: 1\nb: 2\nc: 3\nd: 4\ne: 5\nf: 6", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"crlf", "a: 1\r\nb: 2\r\n# c\r\nc: 3\r\nd: 4\r\ne: 5\r\nf: 6\r\n", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"nested", "p:\n  a: 1\n  b: 2\n  c: 3\n  d: 4\n  e: 5\nq: 1\n", "/p", []string{"a", "b", "c", "d", "e"}},
	{"nested in sequence", "- x\n- a: 1\n  b: 2\n  c: 3\n  d: 4\n  e: 5\n- z\n", "/1", []string{"a", "b", "c", "d", "e"}},
	{"block scalars", "a: |\n  one\n  # not a comment\nb: >-\n  two\nc: |+\n  three\n\nd: |\n  four\n\ne: x\nf: y\n", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"hash lines in scalars", "a: |-\n  # c\nb: |\n  # d\nc: 3\nd: |+\n  # e\n\ne: 5\nf: 6\n", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"collections", "a: [1, 2]\nb: {x: 1}\nc:\n  - 1\n  - 2\nd:\n  k: v\n  l: w\ne: 5\nf: 6\n", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"anchors", "a: &x 1\nb: *x\nc: &y {m: 1}\nd: 4\ne: *y\nf: 6\n", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"explicit keys", "? a\n: 1\n? b\n: 2\nc: 3\n? d\n: 4\ne: 5\nf: 6\n", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"flow mapping", "{a: 1, b: 2, c: 3, d: 4, e: 5, f: 6}\n", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"quoted and tagged", "'a': 1\n\"b\": 2\n!!str c: 3\nd: !!int 4\n? e\nf: 6\n", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"documents", "a: 1\nb: 2\nc: 3\nd: 4\ne: 5\nf: 6\n---\nx: 1\ny: 2\n", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"with start marker", "---\n# c\na: 1\nb: 2\nc: 3\nd: 4\ne: 5\nf: 6\n...\n", "", []string{"a", "b", "c", "d", "e", "f"}},
	{"missing keys", "a: 1\nb: 2\nc: 3\nd: 4\ne: 5\nf: 6\n", "", []string{"a", "zz", "c", "yy", "e", "f"}},
	{"anchor on a key's value", "a: &x 1\nb: 2\nc: 3\nd: *x\ne: 5\nf: 6\n", "", []string{"a", "b", "c", "e"}},
}

// removalPatches returns the merge patch and the RFC 6902 patch that remove keys
// from the mapping at parent.
func removalPatches(parent string, keys []string) (merge, rfc []byte) {
	var m, r []string
	for _, k := range keys {
		m = append(m, fmt.Sprintf("%q:null", k))
		r = append(r, fmt.Sprintf(`{"op":"remove","path":"%s/%s"}`, parent, k))
	}
	body := strings.Join(m, ",")
	if parent != "" {
		toks := strings.Split(strings.TrimPrefix(parent, "/"), "/")
		open, shut := "", ""
		for _, tk := range toks {
			open += `{"` + tk + `":`
			shut += "}"
		}
		if toks[0] == "1" { // the second item of a sequence has no merge-patch form
			return nil, []byte("[" + strings.Join(r, ",") + "]")
		}
		return []byte(open + "{" + body + "}" + shut), []byte("[" + strings.Join(r, ",") + "]")
	}
	return []byte("{" + body + "}"), []byte("[" + strings.Join(r, ",") + "]")
}

// Removals made in one pass give the bytes that the same removals give one at a
// time, for every subset of the keys, whenever both succeed; the pass never
// succeeds with a value that differs from the one-by-one result.
func TestBatchedRemovalsMatchOneByOne(t *testing.T) {
	ran := 0
	for _, sh := range batchShapes {
		n := len(sh.keys)
		for mask := 1; mask < 1<<n; mask++ {
			var keys []string
			for i, k := range sh.keys {
				if mask>>i&1 == 1 {
					keys = append(keys, k)
				}
			}
			merge, rfc := removalPatches(sh.parent, keys)
			for kind, p := range map[string][]byte{"merge": merge, "6902": rfc} {
				if p == nil {
					continue
				}
				apply := func(min int) (*Document, error) {
					old := batchMin
					batchMin = min
					defer func() { batchMin = old }()
					d := mustParse(t, sh.src)
					if kind == "merge" {
						return d, d.ApplyMergePatch(p)
					}
					return d, d.ApplyPatch(p)
				}
				one, errOne := apply(1 << 30)
				all, errAll := apply(1)
				if errAll != nil && errOne == nil {
					t.Fatalf("%s %s %v: one by one: %v; in one pass: %v", sh.name, kind, keys, errOne, errAll)
				}
				if errOne == nil && errAll == nil && !bytes.Equal(one.Bytes(), all.Bytes()) {
					t.Fatalf("%s %s %v:\none by one %q\none pass   %q", sh.name, kind, keys, one.Bytes(), all.Bytes())
				}
				if err := sameAsFreshParse(all); err != nil {
					t.Fatalf("%s %s %v: %v", sh.name, kind, keys, err)
				}
				if all.s.ed != nil && all.s.ed.work.batches > 0 {
					ran++
				}
			}
		}
	}
	if ran < 500 {
		t.Fatalf("the one-pass path ran %d times; it is not being tested", ran)
	}
	t.Logf("%d patches made in one pass", ran)
}

// Thousands of removals from a large mapping are one parse of the text.
func TestManyRemovalsFromALargeMappingAreOnePass(t *testing.T) {
	noSpliceCheck(t)
	const n = 100000
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "k%d: %d\n", i, i%10)
	}
	src := sb.String()
	var m, r []string
	var want strings.Builder
	gone := map[int]bool{}
	for i := 0; i < 3000; i++ {
		j := i * 31
		gone[j] = true
		m = append(m, fmt.Sprintf(`"k%d":null`, j))
		r = append(r, fmt.Sprintf(`{"op":"remove","path":"/k%d"}`, j))
	}
	for i := 0; i < n; i++ {
		if !gone[i] {
			fmt.Fprintf(&want, "k%d: %d\n", i, i%10)
		}
	}
	for _, kind := range []string{"merge", "6902"} {
		for preset, opts := range map[string][]Option{"default": nil, "untrusted": {Untrusted()}} {
			d := mustParse(t, src, opts...)
			start := time.Now()
			var err error
			if kind == "merge" {
				err = d.ApplyMergePatch([]byte("{" + strings.Join(m, ",") + "}"))
			} else {
				err = d.ApplyPatch([]byte("[" + strings.Join(r, ",") + "]"))
			}
			if err != nil {
				t.Fatalf("%s %s: %v", kind, preset, err)
			}
			w := d.s.ed.work
			if w.batches != 1 || w.regions != 0 || w.wholes != 1 {
				t.Errorf("%s %s: batches %d, regions %d, whole parses %d", kind, preset, w.batches, w.regions, w.wholes)
			}
			if string(d.Bytes()) != want.String() {
				t.Fatalf("%s %s: wrong result", kind, preset)
			}
			t.Logf("%s %s: %v", kind, preset, time.Since(start))
		}
	}
}

// A patch that removes a key that is not there fails at that step and leaves the
// document as it was, also when the removals before it run in one pass.
func TestBatchedRemovalsOfMissingKeyFailAtThatStep(t *testing.T) {
	src := "a: 1\nb: 2\nc: 3\nd: 4\ne: 5\nf: 6\ng: 7\nh: 8\ni: 9\nj: 10\n"
	d := mustParse(t, src)
	var ops []string
	for _, k := range strings.Split("abcdefghi", "") {
		ops = append(ops, fmt.Sprintf(`{"op":"remove","path":"/%s"}`, k))
	}
	ops = append(ops, `{"op":"remove","path":"/missing"}`)
	err := d.ApplyPatch([]byte("[" + strings.Join(ops, ",") + "]"))
	if err == nil || !strings.Contains(err.Error(), "operation 9") {
		t.Fatalf("got %v", err)
	}
	if string(d.Bytes()) != src {
		t.Fatalf("document changed: %q", d.Bytes())
	}
}

// A comment line that is indented deeper than a removed entry stays behind when the
// entry goes. If the survivor before it is a block scalar the line would become its
// content, which changes a value that no removal touched: the pass must not commit
// that, whatever the one-by-one path does with it.
func TestBatchedRemovalsNeverChangeASurvivor(t *testing.T) {
	src := "s:\n  q: |\n    t\ngone: 12\n      # note\nkeep: 1\n"
	var keys []string
	for i := 0; i < 8; i++ {
		keys = append(keys, fmt.Sprintf("p%d", i))
		src += fmt.Sprintf("p%d: %d\n", i, i)
	}
	keys = append(keys, "gone")
	merge, rfc := removalPatches("", keys)
	for kind, p := range map[string][]byte{"merge": merge, "6902": rfc} {
		old := batchMin
		batchMin = 1
		d := mustParse(t, src)
		var err error
		if kind == "merge" {
			err = d.ApplyMergePatch(p)
		} else {
			err = d.ApplyPatch(p)
		}
		batchMin = old
		if err != nil {
			if string(d.Bytes()) != src {
				t.Fatalf("%s: refused and changed the document", kind)
			}
			continue
		}
		var got struct{ S struct{ Q string } }
		if err := Unmarshal(d.Bytes(), &got); err != nil || got.S.Q != "t\n" {
			t.Fatalf("%s: /s/q reads %q (%v) in %q", kind, got.S.Q, err, d.Bytes())
		}
	}
}

// Declining a run costs one look at it, not one at every step: removing every key
// of a mapping, last first, takes time in proportion to its size.
func TestDeclinedRunIsNotPlannedAgain(t *testing.T) {
	noSpliceCheck(t)
	const n = 16000
	var sb strings.Builder
	var ops []string
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "k%d: %d\n", i, i)
	}
	for i := n - 1; i >= 0; i-- {
		ops = append(ops, fmt.Sprintf(`{"op":"remove","path":"/k%d"}`, i))
	}
	d := mustParse(t, sb.String(), NoLimits())
	if err := d.ApplyPatch([]byte("[" + strings.Join(ops, ",") + "]")); err != nil {
		t.Fatal(err)
	}
	if d.s.ed.work.batches != 0 {
		t.Errorf("a run of every key was made in one pass")
	}
	if tries := d.s.ed.work.batchTries; tries > 1 {
		t.Errorf("the run was looked at %d times, want at most once", tries)
	}
}

// A removal near the front of a mapping costs at least the text and the nodes it
// moves, so the work bound stands for about as many whole re-reads as it says.
func TestRegionRemovalIsChargedForWhatItMoves(t *testing.T) {
	var sb strings.Builder
	const n = 20000
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "k%d: %d\n", i, i%10)
	}
	d := mustParse(t, sb.String())
	w := d.s.newPatchWork(10)
	if err := d.Remove("/k100"); err != nil {
		t.Fatal(err)
	}
	if d.s.ed.work.regions != 1 {
		t.Fatal("the removal was not a region edit; the test is not measuring one")
	}
	nodes, bytesAfter := len(d.s.tree.Nodes), len(d.s.src)-1000
	if c, min := w.cost(), nodes/2+bytesAfter/8; c < min {
		t.Fatalf("a removal near the front cost %d, want at least %d", c, min)
	}
}

// Eight removals from the front of a mapping do no more key comparisons than the
// same removals made one by one: the one-pass path looks at nothing first.
func TestShortRunOfRemovalsComparesNoMoreKeys(t *testing.T) {
	src := string(largeMapping(20000))
	var ops []string
	for i := 0; i < 8; i++ {
		ops = append(ops, fmt.Sprintf(`{"op":"remove","path":"/k%d"}`, i))
	}
	compared := func(d *Document) int {
		if d.s.ed == nil || d.s.ed.cache.Keys == nil {
			return 0
		}
		return d.s.ed.cache.Keys.Compared
	}
	a := mustParse(t, src)
	if err := a.ApplyPatch([]byte("[" + strings.Join(ops, ",") + "]")); err != nil {
		t.Fatal(err)
	}
	b := mustParse(t, src)
	for i := 0; i < 8; i++ {
		if err := b.Remove(fmt.Sprintf("/k%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if compared(a) > compared(b) {
		t.Errorf("patch compared %d keys, one by one %d", compared(a), compared(b))
	}
	if a.s.ed.work.batchTries != 0 {
		t.Errorf("a run of 8 was looked at by the one-pass path")
	}
	if string(a.Bytes()) != string(b.Bytes()) {
		t.Error("results differ")
	}
}

// Nine removals from the front of a large mapping are cheaper as region edits than
// as one pass that parses the whole text, so no whole parse happens.
func TestFrontRunOfNineRemovalsParsesNoWholeText(t *testing.T) {
	var ops []string
	for i := 0; i < 9; i++ {
		ops = append(ops, fmt.Sprintf(`{"op":"remove","path":"/k%d"}`, i))
	}
	d := mustParse(t, string(largeMapping(20000)))
	if err := d.ApplyPatch([]byte("[" + strings.Join(ops, ",") + "]")); err != nil {
		t.Fatal(err)
	}
	if w := d.s.es().work.wholes; w != 0 {
		t.Errorf("parsed the whole text %d times", w)
	}
	var m map[string]int
	if err := d.Decode(&m); err != nil || len(m) != 19991 {
		t.Errorf("%d keys, %v", len(m), err)
	}
}
