package edit

import (
	"fmt"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/cst"
)

// A merge search reads each source mapping once, however it is reached, so the
// work counted stays within a small multiple of the document's size.
func TestMergedValueWorkIsBounded(t *testing.T) {
	shapes := map[string]string{}
	var b strings.Builder
	b.WriteString("a: &a\n")
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "  k%d: %d\n", i, i)
	}
	b.WriteString("  <<: *a\n")
	shapes["self"] = b.String()
	b.Reset()
	for l := 0; l < 20; l++ {
		fmt.Fprintf(&b, "l%d: &l%d\n", l, l)
		for i := 0; i < 200; i++ {
			fmt.Fprintf(&b, "  k%d_%d: 1\n", l, i)
		}
		if l > 0 {
			fmt.Fprintf(&b, "  <<: [*l%d, *l%d]\n", l-1, l-1)
		}
	}
	shapes["chain"] = b.String()
	for name, text := range shapes {
		src := []byte(text)
		var tree cst.Tree
		if err := cst.Build(&tree, src, -1); err != nil {
			t.Fatal(err)
		}
		c := NewCtx(&tree, src, 0)
		// The mapping to search: the last top-level value.
		var m int32
		for k := tree.First(c.Root()); k != 0; k = tree.Nodes[tree.Nodes[k].Next].Next {
			m = tree.Nodes[k].Next
		}
		c.Merge = true
		if _, err := c.MergedValue(m, "missing"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if limit := 3 * len(tree.Nodes); int(c.MergeWork) > limit {
			t.Errorf("%s: read %d entries for a document of %d nodes", name, c.MergeWork, len(tree.Nodes))
		}
		if c.MergeWork == 0 {
			t.Errorf("%s: nothing counted", name)
		}
	}
}
