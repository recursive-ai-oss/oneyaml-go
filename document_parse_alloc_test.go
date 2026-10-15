package oneyaml

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// recordsText is about size bytes of block YAML: a list of records with nested
// mappings and short sequences.
func recordsText(size int) []byte {
	var sb strings.Builder
	for i := 0; sb.Len() < size; i++ {
		fmt.Fprintf(&sb, "- id: %d\n  name: Person %d\n  email: person%d@example.com\n  address:\n    street: %d Main Street\n    city: Springfield\n    geo:\n      lat: 12.5\n      lng: -45.25\n  tags:\n    - alpha\n    - beta\n    - gamma\n  active: true\n", i, i, i, i)
	}
	return []byte(sb.String())
}

// flowText is about size bytes of dense flow collections, the layout with the most
// nodes per byte.
func flowText(size int) []byte {
	var sb strings.Builder
	for i := 0; sb.Len() < size; i++ {
		fmt.Fprintf(&sb, "- {name: item%d, label: some text here, size: %d, tags: [alpha, beta, gamma], extra: {k: 1, v: [1, 2]}}\n", i, i)
	}
	return []byte(sb.String())
}

// totalAllocated returns the bytes f allocates in total.
func totalAllocated(f func()) uint64 {
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// Parse builds the tree in slices sized from the text, so its total allocation
// stays within a small multiple of the input; growing the node slice step by step
// used to allocate about twenty times the input.
func TestParseAllocationStaysWithinMultipleOfInput(t *testing.T) {
	noSpliceCheck(t)
	for _, c := range []struct {
		name  string
		data  []byte
		limit float64
		patch string
	}{
		{"block records", recordsText(64 << 10), 7, `[{"op":"test","path":"/0/id","value":0},{"op":"replace","path":"/1/name","value":"Renamed"},{"op":"remove","path":"/3"}]`},
		{"dense flow", flowText(64 << 10), 8, `[{"op":"test","path":"/0/size","value":0},{"op":"replace","path":"/1/name","value":"Renamed"},{"op":"remove","path":"/3"}]`},
	} {
		var d *Document
		n := totalAllocated(func() {
			var err error
			if d, err = Parse(c.data); err != nil {
				t.Fatal(err)
			}
		})
		if r := float64(n) / float64(len(c.data)); r > c.limit {
			t.Errorf("%s: Parse of %d bytes allocated %d bytes, %.1fx; want at most %.0fx", c.name, len(c.data), n, r, c.limit)
		}
		// A patch of a few operations adds at most a copy of the text and a small
		// re-parse; before, every edit parsed into freshly grown slices as well.
		d, _ = Parse(c.data)
		m := totalAllocated(func() {
			if err := d.ApplyPatch([]byte(c.patch)); err != nil {
				t.Errorf("%s: patch: %v", c.name, err)
			}
		})
		if r := float64(n+m) / float64(len(c.data)); r > c.limit+2 {
			t.Errorf("%s: Parse and a patch allocated %.1fx the input; want at most %.0fx", c.name, r, c.limit+2)
		}
	}
}

// keptHeap returns the heap a value keeps alive, in bytes.
func keptHeap(build func() any) uint64 {
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	v := build()
	runtime.GC()
	runtime.ReadMemStats(&b)
	runtime.KeepAlive(v)
	if b.HeapAlloc < a.HeapAlloc {
		return 0
	}
	return b.HeapAlloc - a.HeapAlloc
}

// The room Parse reserves comes from a bounded look at the text, not from every
// colon in it: a text that is refused early, or has few nodes and many colons in
// its scalars, costs about its own size, and a text that is dense at the start and
// sparse after that does not keep slots it never fills.
func TestParseReservationIsBoundedByWhatTheParseReaches(t *testing.T) {
	colons := strings.Repeat("a:b", 3<<20) // one plain scalar of colons, 9 MB
	early := append([]byte("a: b: c\n"), []byte(strings.Repeat("x: y\n", 4<<20))...)
	var skew strings.Builder
	skew.WriteString("{")
	for skew.Len() < 1<<20 {
		skew.WriteString("a,")
	}
	skew.WriteString("}\n")
	skew.WriteString(strings.Repeat("# comment text\n", 1<<18))
	for _, c := range []struct {
		name      string
		data      []byte
		total     float64 // limit on bytes allocated, times the input
		kept      bool    // check the heap the Document keeps against its own tree; false: parse must fail
		untrusted bool
	}{
		{"scalar of colons", []byte("k: " + colons + "\n"), 2.2, true, false},
		{"early syntax error", early, 1.6, false, true},
		{"dense start, then comments", []byte(skew.String()), 100, true, false},
	} {
		var opts []Option
		if c.untrusted {
			opts = append(opts, Untrusted())
		}
		var err error
		n := totalAllocated(func() { _, err = Parse(c.data, opts...) })
		if r := float64(n) / float64(len(c.data)); r > c.total {
			t.Errorf("%s: Parse allocated %.1fx the input; want at most %.1fx", c.name, r, c.total)
		}
		if c.kept != (err == nil) {
			t.Errorf("%s: error %v", c.name, err)
		}
		if c.kept {
			var d *Document
			k := keptHeap(func() any { d, _ = Parse(c.data, opts...); return d })
			tree := uint64(28*len(d.s.tree.Nodes) + 16*len(d.s.tree.Props) + len(d.s.src))
			if k > tree+tree/3+64<<10 {
				t.Errorf("%s: the Document keeps %d bytes; its tree and text are %d", c.name, k, tree)
			}
		}
	}
}
