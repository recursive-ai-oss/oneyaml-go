package oneyaml

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/cst"
)

// cursorOK reports whether the stream's child cursor, if it is valid for the
// current tree, names the child at the position it says.
func cursorOK(s *docStream) (valid bool, err error) {
	cu := s.dec.cur
	if cu.gen != s.gen || cu.node == 0 {
		return false, nil
	}
	t := &s.tree
	if int(cu.node) >= len(t.Nodes) || int(cu.parent) >= len(t.Nodes) || t.Nodes[cu.node].Parent != cu.parent {
		return true, fmt.Errorf("cursor node %d is not a child of %d", cu.node, cu.parent)
	}
	pos := 0
	for c := t.First(cu.parent); c != 0 && c != cu.node; c = t.Nodes[c].Next {
		pos++
	}
	if pos != cu.pos {
		return true, fmt.Errorf("cursor says position %d for node %d, which is child %d of %d", cu.pos, cu.node, pos, cu.parent)
	}
	return true, nil
}

// A cursor that an edit keeps must still name the child it names: every edit kind,
// on collections of both kinds, with the cursor before, inside and after the edit.
func TestChildCursorStaysTrueAcrossEdits(t *testing.T) {
	seeds := []string{
		string(annotWideMap(2500)),
		string(annotRecords(2500)),
		"a: 1\nb:\n  c: [1, 2, 3]\n  d: {x: y}\ne: |\n  lit\nf:\n  - 1\n  - k: v\n    w: 2\ng: end\n",
		"- a\n- [b, c]\n- d: 1\n  e: 2\n- |\n  t\n- last\n",
	}
	seeds = append(seeds, func() []string {
		var out []string
		for _, b := range kitSeeds(t) {
			if len(b) < 2000 {
				out = append(out, string(b))
			}
		}
		return out
	}()...)
	runs := 30
	if testing.Short() {
		runs = 6
	}
	kept, dropped := 0, 0
	for si, seed := range seeds {
		for run := 0; run < runs; run++ {
			r := rand.New(rand.NewSource(int64(si*100 + run)))
			d, err := Parse([]byte(seed))
			if err != nil || d.Len() != 1 {
				break
			}
			for step := 0; step < 6; step++ {
				s := d.s
				// Put the cursor in some collection at some position.
				var colls []int32
				for i := 1; i < len(s.tree.Nodes); i++ {
					if k := s.tree.Nodes[i].Kind; (k == cst.Mapping || k == cst.Sequence) && s.tree.First(int32(i)) != 0 {
						colls = append(colls, int32(i))
					}
				}
				if len(colls) == 0 {
					break
				}
				coll := colls[r.Intn(len(colls))]
				s.childAt(coll, r.Intn(s.tree.Count(coll)))
				if ok, err := cursorOK(s); !ok || err != nil {
					t.Fatalf("setting the cursor: %v", err)
				}

				vals, err := streamValues(d)
				if err != nil {
					t.Fatal(err)
				}
				var ptrs []string
				pointers(&vals[0], "", &ptrs)
				ptr := ptrs[r.Intn(len(ptrs))]
				var opErr error
				switch r.Intn(7) {
				case 0, 1:
					v := randValue(r, 2)
					opErr = s.edit(0, opSet, ptr, &v)
				case 2:
					v := randValue(r, 1)
					opErr = s.edit(0, opSet, ptr+"/new"+fmt.Sprint(r.Intn(3)), &v)
				case 3:
					opErr = s.edit(0, opRemove, ptr, nil)
				case 4:
					opErr = d.Rename(ptr, "renamed"+fmt.Sprint(r.Intn(3)))
				case 5:
					opErr = d.SetHeadComment(ptr, "c"+strings.Repeat("x", r.Intn(30)))
				default:
					opErr = d.SetLineComment(ptr, "l")
				}
				_ = opErr
				valid, err := cursorOK(s)
				if err != nil {
					t.Fatalf("seed %d run %d step %d, edit at %q: %v\n%s", si, run, step, ptr, err, d.Bytes())
				}
				if valid {
					kept++
				} else {
					dropped++
				}
			}
		}
	}
	t.Logf("cursors kept %d, dropped %d", kept, dropped)
	if kept == 0 {
		t.Fatal("no edit kept the cursor")
	}
}

// A loop that sets the value of each entry as it goes looks the next entry up
// without walking from the first, however the edit is written.
func TestSetInRangeDoesNotRewalk(t *testing.T) {
	noSpliceCheck(t)
	const n = 3000
	maps := func() []byte {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&sb, "k%d: %d\n", i, i%10)
		}
		return []byte(sb.String())
	}()
	seqs := func() []byte {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&sb, "- %d\n", i%10)
		}
		return []byte(sb.String())
	}()
	values := map[string]any{
		"inline scalar":    "changed value",
		"multi-line text":  "two\nlines",
		"flow collection":  []any{1, 2},
		"nested mapping":   map[string]any{"a": 1, "b": []any{"x"}},
		"empty collection": map[string]any{},
	}
	for name, v := range values {
		d, err := Parse(maps)
		if err != nil {
			t.Fatal(err)
		}
		visited := 0
		for k := range d.Root().Entries() {
			if err := d.Set("/"+k.Value(), v); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			visited++
		}
		if visited != n {
			t.Fatalf("%s: the loop visited %d entries of %d", name, visited, n)
		}
		if w := d.s.es().work.cursorWalks; w > n/50 {
			t.Errorf("mapping, %s: %d lookups walked from the first entry, for %d edits", name, w, n)
		}
		// The text is what collecting the keys first gives.
		e, _ := Parse(maps)
		var keys []string
		for k := range e.Root().Entries() {
			keys = append(keys, k.Value())
		}
		for _, k := range keys {
			if err := e.Set("/"+k, v); err != nil {
				t.Fatal(err)
			}
		}
		if string(d.Bytes()) != string(e.Bytes()) {
			t.Errorf("mapping, %s: the loop and collect-then-set give different text", name)
		}

		d, _ = Parse(seqs)
		visited = 0
		i := 0
		for range d.Root().Items() {
			if err := d.Set(fmt.Sprintf("/%d", i), v); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			i++
			visited++
		}
		if visited != n {
			t.Fatalf("%s: the loop over items visited %d of %d", name, visited, n)
		}
		if w := d.s.es().work.cursorWalks; w > n/50 {
			t.Errorf("sequence, %s: %d lookups walked from the first item, for %d edits", name, w, n)
		}
	}
}

// A loop that edits an entry other than the one it is on keeps its place.
func TestRangeEditingOtherEntriesKeepsPlace(t *testing.T) {
	noSpliceCheck(t)
	const n = 2000
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "k%d: %d\n", i, i%10)
	}
	d, err := Parse([]byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for k := range d.Root().Entries() {
		seen = append(seen, k.Value())
		// A change to the first entry, and to a later one, with values that move nodes.
		if err := d.Set("/k0", fmt.Sprintf("first %d", len(seen))); err != nil {
			t.Fatal(err)
		}
		if err := d.Set("/k1999", []any{len(seen)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != n {
		t.Fatalf("the loop visited %d entries of %d", len(seen), n)
	}
	for i, k := range seen {
		if k != fmt.Sprintf("k%d", i) {
			t.Fatalf("entry %d is %q", i, k)
		}
	}
	if w := d.s.es().work.cursorWalks; w > n/50 {
		t.Errorf("%d lookups walked from the first entry, for %d passes", w, n)
	}
}
