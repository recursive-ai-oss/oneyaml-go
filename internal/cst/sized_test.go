package cst

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/parse"
)

// plainBuild builds the tree without reserving room first, as the decoder's hook
// builder does.
func plainBuild(t *testing.T, src []byte) Tree {
	var tr Tree
	var b Builder
	b.Reset(&tr, src)
	p := parse.New(src, -1)
	for {
		ev, err := p.Next()
		if err == io.EOF {
			return tr
		}
		if err != nil {
			t.Fatal(err)
		}
		b.Event(&ev)
	}
}

// Reserving room from an estimate must not change the tree, however far off the
// estimate is: texts with a node for almost every byte, with none of the
// characters the estimate counts, and long enough to be sampled.
func TestSizedBuildMatchesPlainBuild(t *testing.T) {
	var flow, text, block, anchors strings.Builder
	flow.WriteString("[")
	for i := 0; i < 30000; i++ {
		fmt.Fprintf(&flow, "x%d,", i)
	}
	flow.WriteString("]\n")
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&text, "plain scalar %d\n---\n", i)
	}
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&block, "k%d: v\n", i)
	}
	for i := 0; i < 8000; i++ {
		fmt.Fprintf(&anchors, "- &a%d !!str x\n", i)
	}
	for name, src := range map[string]string{
		"flow sequence":    flow.String(),
		"many documents":   text.String(),
		"block mapping":    block.String(),
		"anchors and tags": anchors.String(),
		"short":            "a: [1, 2]\n",
		"empty":            "",
	} {
		var got Tree
		if err := Build(&got, []byte(src), -1); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := plainBuild(t, []byte(src))
		if !reflect.DeepEqual(got.Nodes, want.Nodes) || !reflect.DeepEqual(got.Props, want.Props) || !reflect.DeepEqual(got.Docs, want.Docs) {
			t.Errorf("%s: the tree built with reserved room differs from the plain one", name)
		}
	}
}
