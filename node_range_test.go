package oneyaml_test

import (
	"fmt"
	"log"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
)

func TestRangeEditsEveryEntry(t *testing.T) {
	doc, _ := oneyaml.Parse([]byte(imagesDoc))
	imgs, _ := doc.Root().Lookup("images")
	n := 0
	for k, v := range imgs.Entries() {
		n++
		if k.Stale() || v.Stale() {
			t.Fatal("yielded a stale node")
		}
		if err := doc.Set("/images/"+k.Value(), v.Value()+"-patched"); err != nil {
			t.Fatal(err)
		}
	}
	want := "images:\n  web: nginx:1.25-patched\n  db: postgres:15-patched\n  cache: redis:7-patched\n"
	if n != 3 || string(doc.Bytes()) != want {
		t.Errorf("yielded %d times:\n%s", n, doc.Bytes())
	}

	doc, _ = oneyaml.Parse([]byte("- 1\n- 2\n- 3\n"))
	n = 0
	for it := range doc.Root().Items() {
		n++
		if err := doc.Set(fmt.Sprintf("/%d", n-1), "x"+it.Value()); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(doc.Bytes()); n != 3 || got != "- x1\n- x2\n- x3\n" {
		t.Errorf("Items yielded %d times:\n%s", n, got)
	}
}

func TestRangeNested(t *testing.T) {
	doc, _ := oneyaml.Parse([]byte("a: {x: 1, y: 2}\nb: {x: 3, y: 4}\n"))
	n := 0
	for ak, av := range doc.Root().Entries() {
		parent := ak.Value() // ak is stale after the first Set below
		for xk, xv := range av.Entries() {
			n++
			if err := doc.Set("/"+parent+"/"+xk.Value(), "n"+xv.Value()); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := string(doc.Bytes()); n != 4 || got != "a: {x: n1, y: n2}\nb: {x: n3, y: n4}\n" {
		t.Errorf("n=%d\n%s", n, got)
	}
}

func TestRangeRemoveCurrent(t *testing.T) {
	doc, _ := oneyaml.Parse([]byte("m:\n  a: 1\n  b: 2\n  c: 3\n  d: 4\n"))
	m, _ := doc.Root().Lookup("m")
	var seen []string
	for k := range m.Entries() {
		seen = append(seen, k.Value())
		if k.Value() == "a" {
			if err := doc.Remove("/m/a"); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Positions move up when an entry goes: b is skipped, the rest are seen once.
	if strings.Join(seen, ",") != "a,c,d" {
		t.Errorf("seen %v", seen)
	}
	if got := string(doc.Bytes()); got != "m:\n  b: 2\n  c: 3\n  d: 4\n" {
		t.Errorf("document:\n%s", got)
	}
}

func TestRangeAddDuringLoop(t *testing.T) {
	doc, _ := oneyaml.Parse([]byte("m:\n  a: 1\n  b: 2\n"))
	m, _ := doc.Root().Lookup("m")
	n := 0
	for k, v := range m.Entries() {
		n++
		if k.Stale() || v.Stale() || n > 10 {
			t.Fatal("stale node or runaway loop")
		}
		if n == 1 {
			if err := doc.Set("/m/z", 9); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Added entries may or may not be produced; here z is at the end and is.
	if n != 3 {
		t.Errorf("yielded %d times, want 3", n)
	}
}

func TestRangeCollectionReplacedOrGone(t *testing.T) {
	for name, edit := range map[string]func(*oneyaml.Document) error{
		"removed":    func(d *oneyaml.Document) error { return d.Remove("/m") },
		"scalar":     func(d *oneyaml.Document) error { return d.Set("/m", 5) },
		"other kind": func(d *oneyaml.Document) error { return d.Set("/m", []int{1, 2, 3}) },
		"shorter":    func(d *oneyaml.Document) error { return d.Set("/m", map[string]int{"a": 1}) },
		"root":       func(d *oneyaml.Document) error { return d.Set("", 7) },
	} {
		t.Run(name, func(t *testing.T) {
			doc, _ := oneyaml.Parse([]byte("m: {a: 1, b: 2, c: 3}\nz: 0\n"))
			m, _ := doc.Root().Lookup("m")
			n := 0
			for range m.Entries() {
				n++
				if n == 1 {
					if err := edit(doc); err != nil {
						t.Fatal(err)
					}
				}
			}
			if n != 1 {
				t.Errorf("yielded %d times, want 1", n)
			}
		})
	}
}

func TestRangeCollectionWithoutPointer(t *testing.T) {
	// A duplicate key and a complex key: no pointer, so the loop ends after an edit.
	for name, src := range map[string]string{
		"duplicate key": "m: {a: 1}\nm: {a: 1, b: 2, c: 3}\nz: 0\n",
		"complex key":   "? {a: 1, b: 2, c: 3}\n: v\nz: 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := oneyaml.Parse([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			var coll oneyaml.Node
			for k, v := range doc.Root().Entries() {
				if k.Kind() == oneyaml.MappingNode {
					coll = k
				} else if k.Value() == "m" {
					coll = v
				}
			}
			if _, err := coll.Pointer(); err == nil {
				t.Fatal("collection has a pointer")
			}
			n := 0
			for range coll.Entries() {
				n++
				_ = doc.Set("/z", n)
			}
			if n != 1 {
				t.Errorf("yielded %d times, want 1", n)
			}
		})
	}
}

func TestRangeFailedPatchKeepsGoing(t *testing.T) {
	doc, _ := oneyaml.Parse([]byte(imagesDoc))
	imgs, _ := doc.Root().Lookup("images")
	n := 0
	for k, v := range imgs.Entries() {
		n++
		if err := doc.ApplyPatch([]byte(`[{"op":"replace","path":"/images/web","value":1},{"op":"remove","path":"/nope"}]`)); err == nil {
			t.Fatal("patch should fail")
		}
		if k.Stale() || v.Stale() || v.Value() == "" {
			t.Fatal("nodes stale after a failed patch")
		}
	}
	if n != 3 || string(doc.Bytes()) != imagesDoc {
		t.Errorf("n=%d\n%s", n, doc.Bytes())
	}
	// A failed patch, then a good edit in the same loop.
	n = 0
	for k, v := range imgs.Entries() {
		n++
		_ = doc.ApplyPatch([]byte(`[{"op":"remove","path":"/nope"}]`))
		if err := doc.Set("/images/"+k.Value(), v.Value()+"!"); err != nil {
			t.Fatal(err)
		}
	}
	if n != 3 || !strings.Contains(string(doc.Bytes()), "cache: redis:7!") {
		t.Errorf("n=%d\n%s", n, doc.Bytes())
	}
}

func TestRangeBreakAndPanicLeaveNoState(t *testing.T) {
	doc, _ := oneyaml.Parse([]byte(imagesDoc))
	imgs, _ := doc.Root().Lookup("images")
	func() {
		defer func() { _ = recover() }()
		for range imgs.Entries() {
			panic("stop")
		}
	}()
	for range imgs.Entries() {
		break
	}
	n := 0
	for k, v := range imgs.Entries() {
		n++
		_ = doc.Set("/images/"+k.Value(), v.Value()+"x")
	}
	if n != 3 {
		t.Errorf("yielded %d times", n)
	}
}

func TestRangeWithoutEditsAllocatesNothing(t *testing.T) {
	doc, _ := oneyaml.Parse([]byte(imagesDoc))
	imgs, _ := doc.Root().Lookup("images")
	sum := 0
	run := func() {
		for k, v := range imgs.Entries() {
			sum += k.Len() + v.Len()
		}
	}
	run()
	if a := testing.AllocsPerRun(100, run); a != 0 {
		t.Errorf("Entries allocates %v times per loop", a)
	}
}

// Ranging over a collection and setting each value as the loop goes.
func ExampleNode_Entries_edit() {
	doc, err := oneyaml.Parse([]byte("images:\n  web: nginx:1.25\n  db: postgres:15\n"))
	if err != nil {
		log.Fatal(err)
	}
	imgs, ok := doc.Root().Lookup("images")
	if !ok {
		log.Fatal("no images")
	}
	for k, v := range imgs.Entries() {
		// Each Set makes the nodes read so far stale, but the loop goes on with fresh ones.
		if err := doc.Set("/images/"+k.Value(), v.Value()+"-patched"); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// images:
	//   web: nginx:1.25-patched
	//   db: postgres:15-patched
}

func BenchmarkRangeEntries(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&sb, "k%d: v\n", i)
	}
	doc, _ := oneyaml.Parse([]byte(sb.String()))
	root := doc.Root()
	b.ReportAllocs()
	for b.Loop() {
		for range root.Entries() {
		}
	}
}

func BenchmarkParseThenRangeEntries(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&sb, "k%d: v\n", i)
	}
	data := []byte(sb.String())
	b.ReportAllocs()
	for b.Loop() {
		doc, _ := oneyaml.Parse(data)
		root := doc.Root()
		for range root.Entries() {
		}
	}
}

func BenchmarkRangeItems(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&sb, "- v%d\n", i)
	}
	doc, _ := oneyaml.Parse([]byte(sb.String()))
	root := doc.Root()
	b.ReportAllocs()
	for b.Loop() {
		for range root.Items() {
		}
	}
}

func BenchmarkRangeNestedItems(b *testing.B) {
	doc, _ := oneyaml.Parse([]byte("- [[1, 2], [3, 4]]\n- [[5, 6], [7, 8]]\n"))
	root := doc.Root()
	b.ReportAllocs()
	for b.Loop() {
		for a := range root.Items() {
			for c := range a.Items() {
				for d := range c.Items() {
					for range d.Items() {
					}
				}
			}
		}
	}
}
