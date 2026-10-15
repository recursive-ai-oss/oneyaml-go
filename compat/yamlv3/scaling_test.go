package yaml

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

type itemNode struct{ s string }

func (t *itemNode) UnmarshalYAML(n *Node) error { t.s = n.Value; return nil }

type itemOld struct{ s string }

func (t *itemOld) UnmarshalYAML(unmarshal func(interface{}) error) error { return unmarshal(&t.s) }

// A type that reads its own node is called once per item, and each call must cost
// in proportion to the item, not to the document: doubling the document may
// double the work, never quadruple it. The work is counted in bytes walked, which
// does not depend on the speed of the machine.
func TestHookCallsScaleLinearly(t *testing.T) {
	for _, layout := range []string{"seq", "comments", "map"} {
		t.Run(layout, func(t *testing.T) {
			var steps [2]int64
			for i, n := range []int{4000, 8000} {
				text := hookDoc(layout, n)
				var counter atomic.Int64
				stepCount = &counter
				var err error
				switch layout {
				case "map":
					var x map[string]itemNode
					err = Unmarshal(text, &x)
				case "comments":
					var x []itemOld
					err = Unmarshal(text, &x)
				default:
					var x []itemNode
					err = Unmarshal(text, &x)
				}
				stepCount = nil
				if err != nil {
					t.Fatal(err)
				}
				steps[i] = counter.Load()
			}
			t.Logf("%s: %d steps for 4000 items, %d for 8000", layout, steps[0], steps[1])
			if steps[1] > 3*steps[0] {
				t.Fatalf("work grew %d -> %d when the document doubled", steps[0], steps[1])
			}
		})
	}
}

func hookDoc(layout string, n int) []byte {
	var b strings.Builder
	for i := 0; i < n; i++ {
		switch layout {
		case "map":
			fmt.Fprintf(&b, "k%d: item%d # c\n", i, i)
		case "comments":
			fmt.Fprintf(&b, "# about %d\n- item%d\n", i, i)
		default:
			fmt.Fprintf(&b, "- item%d\n", i)
		}
	}
	return []byte(b.String())
}

// Going back in the text, as a call that builds the node an alias points to does,
// does not walk the text from the top each time.
func TestLineCursorGoingBack(t *testing.T) {
	src := []byte(strings.Repeat("line of text here\n", 5000))
	var counter atomic.Int64
	stepCount = &counter
	defer func() { stepCount = nil }()
	c := newLineCursor(src)
	c.marks = &markSet{} // as a hook that is called many times has
	end := len(src) - 1
	c.pos(end)
	counter.Store(0)
	for i := 0; i < 1000; i++ {
		// Each question is a little before the last one.
		off := end - 80*i - 1
		wantLine := 1
		for _, b := range src[:off] {
			if b == '\n' {
				wantLine++
			}
		}
		if line, _ := c.pos(off); line != wantLine {
			t.Fatalf("pos(%d) line %d, want %d", off, line, wantLine)
		}
	}
	if got := counter.Load(); got > 1000*2*markStride+int64(len(src))*2 {
		t.Fatalf("1000 backward questions walked %d bytes", got)
	}
}
