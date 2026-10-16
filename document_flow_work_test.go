package oneyaml

import (
	"fmt"
	"strings"
	"testing"
)

// Editing a flow mapping with many keys reads what the edit touches, not the
// whole mapping: no edit parses the whole stream, and the text re-scanned stays
// a tiny part of the document.
func TestEditFlowMappingWithManyKeys(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("{")
	for i := 0; i < 140000; i++ {
		fmt.Fprintf(&sb, "k%d: %d,\n", i, i)
	}
	sb.WriteString("}\n")
	doc, err := Parse([]byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if err := doc.Set(fmt.Sprintf("/k%d", 70000+i), 7); err != nil {
			t.Fatal(err)
		}
		if err := doc.Remove(fmt.Sprintf("/k%d", 100000+i)); err != nil {
			t.Fatal(err)
		}
	}
	w := doc.s.ed.work
	if w.wholes != 0 || w.bytes > sb.Len()/100 {
		t.Errorf("100 edits parsed the whole stream %d times and scanned %d of %d bytes", w.wholes, w.bytes, sb.Len())
	}
	if w.inline+w.regions != 100 {
		t.Errorf("%d inline and %d region edits, want 100 in all", w.inline, w.regions)
	}
}
