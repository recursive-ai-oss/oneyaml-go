package oneyaml

import (
	"fmt"
	"slices"
	"testing"

	"oneyaml.dev/go/internal/cst"
)

// spliceCheck turns the comparison off for tests that count allocations.
var spliceCheck = true

func noSpliceCheck(t testing.TB) {
	spliceCheck = false
	t.Cleanup(func() { spliceCheck = true })
}

// Every test in the package runs with the region splice checked against a parse of
// the whole text: the tree an edit leaves must be the tree a fresh parse gives, node
// for node.
func init() {
	regionMinSrc = 0
	spliceHook = func(s *docStream, r *regionDebug) {
		// A full parse after every edit of a megabyte document would make the
		// timing tests measure the check, not the edit.
		if !spliceCheck || len(s.src) > 256<<10 {
			return
		}
		var want cst.Tree
		if err := cst.Build(&want, s.src, -1); err != nil {
			panic(fmt.Sprintf("region edit left text that does not parse: %v\n%q", err, s.src))
		}
		got := &s.tree
		if slices.Equal(got.Nodes, want.Nodes) && slices.Equal(got.Props, want.Props) && slices.Equal(got.Docs, want.Docs) {
			return
		}
		msg := fmt.Sprintf("region edit: tree differs from a parse of the text (nodes %d/%d, props %d/%d, docs %d/%d)",
			len(got.Nodes), len(want.Nodes), len(got.Props), len(want.Props), len(got.Docs), len(want.Docs))
		for i := 0; i < min(len(got.Nodes), len(want.Nodes)); i++ {
			if got.Nodes[i] != want.Nodes[i] {
				msg += fmt.Sprintf("\nnode %d: got %+v\n        want %+v", i, got.Nodes[i], want.Nodes[i])
				break
			}
		}
		for i := 0; i < min(len(got.Props), len(want.Props)); i++ {
			if got.Props[i] != want.Props[i] {
				msg += fmt.Sprintf("\nprop %d: got %+v want %+v", i, got.Props[i], want.Props[i])
				break
			}
		}
		for i := 0; i < min(len(got.Docs), len(want.Docs)); i++ {
			if got.Docs[i] != want.Docs[i] {
				msg += fmt.Sprintf("\ndoc %d: got %+v want %+v", i, got.Docs[i], want.Docs[i])
			}
		}
		panic(msg + fmt.Sprintf("\ntext %q\nregion container %d [%d,%d) pad %d new %q repls %v", s.src, r.cn, r.S, r.E, r.pad, r.text, r.repls))
	}
}
