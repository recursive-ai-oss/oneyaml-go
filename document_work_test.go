package oneyaml

import (
	"fmt"
	"strings"
	"testing"
)

// wideDoc is a flat mapping of n entries of mixed scalars.
func wideDoc(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		switch i % 4 {
		case 0:
			fmt.Fprintf(&b, "key%d: %d\n", i, i)
		case 1:
			fmt.Fprintf(&b, "key%d: plain text %d\n", i, i)
		case 2:
			fmt.Fprintf(&b, "# about key%d\nkey%d: \"quoted: %d\"\n", i, i, i)
		default:
			fmt.Fprintf(&b, "key%d: null\n", i)
		}
	}
	return b.String()
}

// An edit parses the entries it touches, not the stream: the text scanned and the
// nodes built for an edit do not grow with the size of the document.
func TestEditWorkIsBoundedByTheRegion(t *testing.T) {
	var perEdit [2]editWork
	for si, entries := range []int{200, 20000} {
		d, err := Parse([]byte(wideDoc(entries)))
		if err != nil {
			t.Fatal(err)
		}
		s := d.s
		ops := []struct {
			name string
			do   func() error
		}{
			{"set multi-line", func() error { return d.Set("/key5", "two\nlines") }},
			{"set back", func() error { return d.Set("/key5", 7) }},
			{"add", func() error { return d.Set("/zz-new", map[string]any{"a": 1, "b": []int{1, 2}}) }},
			{"remove", func() error { return d.Remove("/key9") }},
			{"remove last", func() error { return d.Remove("/zz-new") }},
		}
		for _, op := range ops {
			before := s.es().work
			if err := op.do(); err != nil {
				t.Fatalf("%d entries: %s: %v", entries, op.name, err)
			}
			w := editWork{
				bytes: s.es().work.bytes - before.bytes, nodes: s.es().work.nodes - before.nodes,
				regions: s.es().work.regions - before.regions + s.es().work.inline - before.inline, wholes: s.es().work.wholes - before.wholes,
			}
			t.Logf("%6d entries, %-14s scanned %5d bytes, built %4d nodes, region edits %d, whole parses %d",
				entries, op.name, w.bytes, w.nodes, w.regions, w.wholes)
			if w.wholes != 0 || w.regions != 1 {
				t.Errorf("%d entries, %s: parsed the stream (%d whole, %d region)", entries, op.name, w.wholes, w.regions)
			}
			if w.bytes > 1024 || w.nodes > 64 {
				t.Errorf("%d entries, %s: scanned %d bytes and built %d nodes for one entry", entries, op.name, w.bytes, w.nodes)
			}
			perEdit[si].bytes += w.bytes
			perEdit[si].nodes += w.nodes
		}
	}
	// 100 times more entries must not mean more work for the same edits.
	if perEdit[1].bytes > 2*perEdit[0].bytes+1024 || perEdit[1].nodes > 2*perEdit[0].nodes+64 {
		t.Errorf("work grew with the document: %d bytes / %d nodes for 200 entries, %d / %d for 20000",
			perEdit[0].bytes, perEdit[0].nodes, perEdit[1].bytes, perEdit[1].nodes)
	}
}
