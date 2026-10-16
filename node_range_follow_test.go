package oneyaml

import (
	"fmt"
	"iter"
	"runtime"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/cst"
)

func slotsInUse(d *Document) int {
	n := 0
	for i := 0; i < d.s.nslots(); i++ {
		if d.s.slotAt(i).id != 0 {
			n++
		}
	}
	return n
}

func parseDoc(t *testing.T, src string) *Document {
	t.Helper()
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func items(t *testing.T, d *Document, ptr string) Node {
	t.Helper()
	var n Node
	if err := d.Get(ptr, &n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Loops that are stopped out of the order they were started in must not disturb
// each other.
func TestRangePullStoppedOutOfOrder(t *testing.T) {
	d := parseDoc(t, "a: [1, 2, 3]\nb: [4, 5, 6]\n")
	nextA, stopA := iter.Pull(items(t, d, "/a").Items())
	nextB, stopB := iter.Pull(items(t, d, "/b").Items())
	defer stopB()
	nextA()
	if v, _ := nextB(); v.Value() != "4" {
		t.Fatalf("B starts with %q", v.Value())
	}
	stopA()
	if err := d.Set("/a/0", 9); err != nil {
		t.Fatal(err)
	}
	var got []string
	for {
		v, ok := nextB()
		if !ok {
			break
		}
		got = append(got, v.Value())
	}
	if strings.Join(got, ",") != "5,6" {
		t.Errorf("B yields %v after A stopped and an edit, want [5 6]", got)
	}
}

// A slot that a stopped loop gave up and later loops took must not be read as the
// first loop's own.
func TestRangePullSlotsReused(t *testing.T) {
	d := parseDoc(t, "a: [1, 2, 3]\nb: [4, 5, 6]\nc: [7, 8, 9]\n")
	nextA, stopA := iter.Pull(items(t, d, "/a").Items())
	nextB, stopB := iter.Pull(items(t, d, "/b").Items())
	defer stopB()
	nextA()
	nextB()
	stopA()
	nextC, stopC := iter.Pull(items(t, d, "/c").Items())
	defer stopC()
	nextC()
	nextC2, stopC2 := iter.Pull(items(t, d, "/c").Items())
	defer stopC2()
	nextC2()
	if err := d.Set("/z", 1); err != nil {
		t.Fatal(err)
	}
	var got []string
	for {
		v, ok := nextB()
		if !ok {
			break
		}
		got = append(got, v.Value())
	}
	if strings.Join(got, ",") != "5,6" {
		t.Errorf("B yields %v, want [5 6]", got)
	}
}

// Every loop gives its slot back when it ends, however it ends.
func TestRangeSlotsAreFreed(t *testing.T) {
	d := parseDoc(t, "a: [1, 2, 3]\nm: {x: 1, y: 2}\n")
	a := items(t, d, "/a")
	m := items(t, d, "/m")
	for range a.Items() {
	}
	for range a.Items() {
		break
	}
	for range m.Entries() {
		for range a.Items() {
		}
	}
	for range a.Items() {
		_ = d.Set("/m/x", 5)
	}
	next, stop := iter.Pull2(m.Entries())
	next()
	stop()
	if n := slotsInUse(d); n != 0 {
		t.Errorf("%d slots still taken after every loop ended", n)
	}
}

// A loop that a panic leaves gives its slot back, so any number of recovered
// panics costs nothing and later loops still follow their edits.
func TestRangeAbandonedLoopsAreBounded(t *testing.T) {
	d := parseDoc(t, "a: [1, 2, 3]\nz: 0\n")
	a := items(t, d, "/a")
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < 20000; i++ {
		func() {
			defer func() { _ = recover() }()
			for range a.Items() {
				panic("stop")
			}
		}()
	}
	if n := slotsInUse(d); n != 0 {
		t.Fatalf("%d slots taken after the panics", n)
	}
	var after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&after)
	if grew := int64(after.HeapAlloc) - int64(before.HeapAlloc); grew > 1<<20 {
		t.Errorf("heap grew by %d bytes", grew)
	}
	for i := 0; i < 1000; i++ {
		if err := d.Set("/z", i); err != nil {
			t.Fatal(err)
		}
	}
	if n := slotsInUse(d); n != 0 {
		t.Fatalf("%d slots taken after the edits", n)
	}
}

// After recovered panics, a new loop that edits still goes on to the end.
func TestRangeEditsAfterRecoveredPanics(t *testing.T) {
	d := parseDoc(t, "a: [1, 2, 3, 4]\n")
	a := items(t, d, "/a")
	for i := 0; i < 10; i++ {
		func() {
			defer func() { _ = recover() }()
			for range a.Items() {
				panic("stop")
			}
		}()
	}
	n := 0
	for it := range items(t, d, "/a").Items() {
		ptr, err := it.Pointer()
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Set(ptr, "x"); err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != 4 || string(d.Bytes()) != "a: [x, x, x, x]\n" {
		t.Errorf("loop saw %d items; document %q", n, d.Bytes())
	}
}

// Loops nested deeper than the fixed slots all go on after the edits their
// bodies make.
func TestRangeNestedEditingLoops(t *testing.T) {
	for _, depth := range []int{5, 40} {
		var sb strings.Builder
		for i := 0; i < depth; i++ {
			sb.WriteString(strings.Repeat("  ", i) + fmt.Sprintf("n%d:\n", i))
		}
		sb.WriteString(strings.Repeat("  ", depth) + "leaf: 0\n")
		for i := depth - 1; i >= 0; i-- {
			sb.WriteString(strings.Repeat("  ", i) + "s: 0\n")
		}
		d := parseDoc(t, sb.String())
		var walk func(n Node)
		max := 0
		cur := 0
		walk = func(n Node) {
			cur++
			max = max2(max, cur)
			for _, v := range n.Entries() {
				if v.Kind() == MappingNode {
					walk(v)
					continue
				}
				ptr, err := v.Pointer()
				if err != nil {
					t.Fatal(err)
				}
				if err := d.Set(ptr, "done"); err != nil {
					t.Fatal(err)
				}
			}
			cur--
		}
		walk(d.Root())
		if got := strings.Count(string(d.Bytes()), "done"); got != depth+1 {
			t.Errorf("depth %d: %d values edited, want %d\n%s", depth, got, depth+1, d.Bytes())
		}
		if strings.Contains(string(d.Bytes()), ": 0") {
			t.Errorf("depth %d: a value was left unedited", depth)
		}
		if n := slotsInUse(d); n != 0 {
			t.Errorf("depth %d: %d slots taken at the end", depth, n)
		}
	}
}

func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func collect(seq iter.Seq[Node], each func(i int)) []string {
	var got []string
	for v := range seq {
		got = append(got, v.Value())
		if each != nil {
			each(len(got))
		}
	}
	return got
}

// An edit that inserts or removes a sibling of an ancestor must not carry the loop
// to a neighbouring collection.
func TestRangeKeepsItsCollectionWhenAncestorSiblingsMove(t *testing.T) {
	const src = "l:\n  - [a, b, c]\n  - [d, e, f]\n  - [g, h, i]\n"
	cases := []struct {
		name string
		edit func(d *Document) error
		want string
	}{
		{"remove before", func(d *Document) error { return d.Remove("/l/0") }, "d,e,f"},
		{"add before", func(d *Document) error {
			return d.ApplyPatch([]byte(`[{"op":"add","path":"/l/0","value":["x","y","z"]}]`))
		}, "d,e,f"},
		{"add before twice", func(d *Document) error {
			return d.ApplyPatch([]byte(`[{"op":"add","path":"/l/0","value":[1]},{"op":"add","path":"/l/0","value":[2]}]`))
		}, "d,e,f"},
		{"two edits", func(d *Document) error {
			if err := d.ApplyPatch([]byte(`[{"op":"add","path":"/l/0","value":[1]}]`)); err != nil {
				return err
			}
			return d.Remove("/l/3")
		}, "d,e,f"},
		{"move before to the end", func(d *Document) error {
			return d.ApplyPatch([]byte(`[{"op":"move","from":"/l/0","path":"/l/2"}]`))
		}, "d,e,f"},
		{"move after to the front", func(d *Document) error {
			return d.ApplyPatch([]byte(`[{"op":"move","from":"/l/2","path":"/l/0"}]`))
		}, "d,e,f"},
		{"add at the collection's index", func(d *Document) error {
			return d.ApplyPatch([]byte(`[{"op":"add","path":"/l/1","value":[1]}]`))
		}, "d,e,f"},
		{"add after", func(d *Document) error {
			return d.ApplyPatch([]byte(`[{"op":"add","path":"/l/2","value":[1]}]`))
		}, "d,e,f"},
		{"append", func(d *Document) error { return d.Set("/l/-", []string{"x"}) }, "d,e,f"},
		{"set a sibling", func(d *Document) error { return d.Set("/l/2", "x") }, "d,e,f"},
		{"remove the collection", func(d *Document) error { return d.Remove("/l/1") }, "d"},
		{"replace the collection", func(d *Document) error { return d.Set("/l/1", []string{"p", "q", "r"}) }, "d"},
		{"remove before then the collection", func(d *Document) error {
			if err := d.Remove("/l/0"); err != nil {
				return err
			}
			return d.Remove("/l/0")
		}, "d"},
		{"replace the list", func(d *Document) error { return d.Set("/l", 5) }, "d"},
		{"root", func(d *Document) error { return d.Set("", []int{1}) }, "d"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := parseDoc(t, src)
			got := collect(items(t, d, "/l/1").Items(), func(i int) {
				if i == 1 {
					if err := c.edit(d); err != nil {
						t.Fatal(err)
					}
				}
			})
			if strings.Join(got, ",") != c.want {
				t.Errorf("yields %v, want %v", got, c.want)
			}
		})
	}
}

// Edits that go through the batched removal, and a merge patch, follow the same rules.
func TestRangeAcrossBatchedAndMergeEdits(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("l: [a, b, c]\nm:\n")
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&sb, "  k%d: %d\n", i, i)
	}
	src := sb.String()
	var ops []string
	var merge []string
	for i := 0; i < 1000; i++ {
		ops = append(ops, fmt.Sprintf(`{"op":"remove","path":"/m/k%d"}`, i))
		merge = append(merge, fmt.Sprintf(`"k%d":null`, i))
	}
	patch := "[" + strings.Join(ops, ",") + "]"
	mergePatch := `{"m":{` + strings.Join(merge, ",") + `}}`
	for name, edit := range map[string]func(d *Document) error{
		"remove patch": func(d *Document) error { return d.ApplyPatch([]byte(patch)) },
		"merge patch":  func(d *Document) error { return d.ApplyMergePatch([]byte(mergePatch)) },
	} {
		t.Run(name, func(t *testing.T) {
			d := parseDoc(t, src)
			if edit == nil {
				return
			}
			got := collect(items(t, d, "/l").Items(), func(i int) {
				if i == 1 {
					if err := edit(d); err != nil {
						t.Fatal(err)
					}
				}
			})
			if strings.Join(got, ",") != "a,b,c" {
				t.Errorf("yields %v, want [a b c]", got)
			}
			if d.Root().Len() != 2 {
				t.Fatal("document unexpected")
			}
			var m Node
			_ = d.Get("/m", &m)
			if m.Len() != 1000 {
				t.Errorf("patch left %d keys", m.Len())
			}
		})
	}
}

// With the one-pass removal forced, a loop over an entry that is removed ends and a
// loop over another entry goes on.
func TestRangeAcrossForcedBatchRemoval(t *testing.T) {
	defer func(a, b int) { batchMin, batchWork = a, b }(batchMin, batchWork)
	batchMin, batchWork = 1, 0
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&sb, "key%d: [p%d, q%d, r%d]\n", i, i, i, i)
	}
	var ops []string
	for i := 0; i < 10; i++ {
		ops = append(ops, fmt.Sprintf(`{"op":"remove","path":"/key%d"}`, i))
	}
	// The key is written again after the removals: a loop over the old entry must
	// not go on in the new one.
	ops = append(ops, `{"op":"add","path":"/key5","value":["x","y","z"]}`)
	patch := []byte("[" + strings.Join(ops, ",") + "]")
	for ptr, want := range map[string]string{"/key20": "p20,q20,r20", "/key5": "p5"} {
		d := parseDoc(t, sb.String())
		got := collect(items(t, d, ptr).Items(), func(i int) {
			if i == 1 {
				if err := d.ApplyPatch(patch); err != nil {
					t.Fatal(err)
				}
			}
		})
		if strings.Join(got, ",") != want {
			t.Errorf("%s yields %v, want %v", ptr, got, want)
		}
		if d.s.ed.work.batches == 0 {
			t.Error("the one-pass removal did not run")
		}
	}
}

// A patch that fails after moving the place of a loop's collection must leave the
// place as it was.
func TestRangePlaceSurvivesFailedPatch(t *testing.T) {
	d := parseDoc(t, "z: 0\nl:\n  - [a, b, c]\n  - [d, e, f]\n  - [g, h, i]\n")
	got := collect(items(t, d, "/l/1").Items(), func(i int) {
		if i == 1 {
			if err := d.Set("/z", 1); err != nil { // the loop notes its place
				t.Fatal(err)
			}
			if d.ApplyPatch([]byte(`[{"op":"add","path":"/l/0","value":[1]},{"op":"remove","path":"/nope"}]`)) == nil {
				t.Fatal("patch should fail")
			}
		}
	})
	if strings.Join(got, ",") != "d,e,f" {
		t.Errorf("yields %v, want [d e f]", got)
	}
}

// refind checks the kind itself, as a second guard behind the places that edits
// move: a place that now leads to a collection of another kind is refused.
func TestRefindRefusesAnotherKind(t *testing.T) {
	d := parseDoc(t, "m: {a: 1, b: 2, c: 3}\nz: 0\n")
	m := items(t, d, "/m")
	h := d.s.watch(m, cst.Mapping)
	defer d.s.unwatch(h)
	d.s.freezeRanges()
	// Replace /m behind the loop's back with a longer sequence.
	val, err := valueOf([]int{1, 2, 3, 4, 5, 6}, &d.s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.s.editTree(0, opSet, "/m", &val); err != nil {
		t.Fatal(err)
	}
	if coll, ok := d.s.refind(h); ok {
		t.Errorf("refind followed the place to node %d, a %v", coll, d.s.tree.Nodes[coll].Kind)
	}
}

// An edit in one document must not move a loop over another document.
func TestRangeIgnoresEditsInOtherDocuments(t *testing.T) {
	const src = "l: [[a], [b], [c]]\n---\nl: [[d, d2, d3], [e, e2, e3], [f]]\n"
	for name, edit := range map[string]func(d *Document) error{
		"remove": func(d *Document) error { return d.Remove("/l/0") },
		"set":    func(d *Document) error { return d.Set("/l/1", []string{"z"}) },
		"patch":  func(d *Document) error { return d.ApplyPatch([]byte(`[{"op":"remove","path":"/l/0"}]`)) },
	} {
		t.Run(name, func(t *testing.T) {
			d := parseDoc(t, src)
			second := d.Doc(1)
			var coll Node
			if err := second.Get("/l/1", &coll); err != nil {
				t.Fatal(err)
			}
			got := collect(coll.Items(), func(i int) {
				if i == 1 {
					if err := edit(d); err != nil {
						t.Fatal(err)
					}
				}
			})
			if strings.Join(got, ",") != "e,e2,e3" {
				t.Errorf("yields %v, want [e e2 e3]", got)
			}
		})
	}
}

// Pointers and loops have no depth limit of their own.
func TestPointerAndRangeDeeperThanSixtyFour(t *testing.T) {
	const depth = 70
	src := strings.Repeat("[", depth) + "1, 2, 3" + strings.Repeat("]", depth) + "\n"
	d := parseDoc(t, src)
	n := d.Root()
	for i := 1; i < depth; i++ {
		n = n.Item(0)
	}
	ptr, err := n.Pointer()
	if err != nil {
		t.Fatalf("Pointer: %v", err)
	}
	if want := strings.Repeat("/0", depth-1); ptr != want {
		t.Errorf("pointer %q, want %q", ptr, want)
	}
	got := collect(n.Items(), func(i int) {
		if i == 1 {
			if err := d.Set(ptr+"/0", 9); err != nil {
				t.Fatal(err)
			}
		}
	})
	if strings.Join(got, ",") != "1,2,3" {
		t.Errorf("loop yields %v, want [1 2 3]", got)
	}
}

// Five nested loops use the fixed slots and two more. The innermost makes a patch
// that fails and is rolled back, then an edit that shifts the place of every loop:
// all of them must go on, with the places they had before the failed patch.
func TestRangeOverflowLoopsSurviveRollbackAndShift(t *testing.T) {
	d := parseDoc(t, "x: [0, [1, [2, [3, [4, 5, 6]]]]]\nk: [p, q]\n")
	var got []string
	step := 0
	var nest func(level int, seq Node)
	nest = func(level int, seq Node) {
		for it := range seq.Items() {
			got = append(got, string(rune('A'+level))+":"+it.Value())
			if it.Kind() == SequenceNode {
				nest(level+1, it)
				continue
			}
			if level == 4 {
				if step++; step == 1 {
					patch := `[{"op":"remove","path":"/x/0"},{"op":"test","path":"/k/0","value":"nope"}]`
					if err := d.ApplyPatch([]byte(patch)); err == nil {
						t.Error("the patch should have failed")
					}
					if err := d.Remove("/x/0"); err != nil {
						t.Error(err)
					}
				}
			}
		}
	}
	nest(0, items(t, d, "/x"))
	want := "A:0 A: B:1 B: C:2 C: D:3 D: E:4 E:5 E:6"
	if s := strings.Join(got, " "); s != want {
		t.Errorf("loops saw\n %s\nwant\n %s", s, want)
	}
	if s := string(d.Bytes()); s != "x: [[1, [2, [3, [4, 5, 6]]]]]\nk: [p, q]\n" {
		t.Errorf("document %q", s)
	}
	if n := slotsInUse(d); n != 0 {
		t.Errorf("%d slots taken", n)
	}
}

// A loop in an overflow slot follows an edit that shifts the list it is in.
func TestRangeOverflowLoopFollowsRemove(t *testing.T) {
	d := parseDoc(t, "p: [0]\nq: [0]\ns: [[1, 2, 3], [4, 5, 6]]\n")
	var got []string
	started := false
	for range d.Root().Entries() {
		if started {
			continue
		}
		started = true
		for range items(t, d, "/p").Items() {
			for range items(t, d, "/q").Items() {
				for it := range items(t, d, "/s/1").Items() { // the fourth loop
					got = append(got, it.Value())
					if len(got) == 1 {
						if err := d.Remove("/s/0"); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
		}
	}
	if s := strings.Join(got, " "); s != "4 5 6" {
		t.Errorf("the fourth loop saw %q", s)
	}
	if s := string(d.Bytes()); s != "p: [0]\nq: [0]\ns: [[4, 5, 6]]\n" {
		t.Errorf("document %q", s)
	}
}
