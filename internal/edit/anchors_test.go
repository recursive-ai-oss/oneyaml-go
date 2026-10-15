package edit

import (
	"fmt"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/cst"
)

// Deleting a node that carries anchors reads the rest of the document once,
// however many anchors the deleted text defines.
func TestAnchorsInUseReadsTheTailOnce(t *testing.T) {
	var b strings.Builder
	b.WriteString("first:\n")
	const anchors = 2000
	for i := 0; i < anchors; i++ {
		fmt.Fprintf(&b, "  k%d: &a%d v%d\n", i, i, i)
	}
	const tail = 4000
	for i := 0; i < tail; i++ {
		fmt.Fprintf(&b, "t%d: plain %d\n", i, i)
	}
	src := []byte(b.String())
	var tree cst.Tree
	if err := cst.Build(&tree, src, -1); err != nil {
		t.Fatal(err)
	}
	c := NewCtx(&tree, src, 0)
	val := tree.Nodes[tree.First(c.Root())].Next // the mapping that holds the anchors
	if err := c.anchorsInUse(val, true); err != nil {
		t.Fatal(err)
	}
	if limit := len(tree.Nodes); c.tailVisits == 0 || int(c.tailVisits) > limit {
		t.Errorf("read %d nodes after the deleted text, which has %d nodes in all", c.tailVisits, limit)
	}
	// An alias after the deleted text still refuses it.
	src = append(src, "use: *a1999\n"...)
	tree = cst.Tree{}
	if err := cst.Build(&tree, src, -1); err != nil {
		t.Fatal(err)
	}
	c = NewCtx(&tree, src, 0)
	if err := c.anchorsInUse(tree.Nodes[tree.First(c.Root())].Next, true); err == nil {
		t.Error("an anchor in use was not refused")
	}
}
