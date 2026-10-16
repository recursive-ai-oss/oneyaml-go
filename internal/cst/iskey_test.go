package cst

import (
	"fmt"
	"strings"
	"testing"
)

// IsKey counts back from the end for the last children and forward from the start
// for the rest; both must give the parity of the position.
func TestIsKeyAtEveryPosition(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 8, 9, 10, 40} {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			switch i % 3 {
			case 0:
				fmt.Fprintf(&sb, "k%d: v\n", i)
			case 1:
				fmt.Fprintf(&sb, "k%d:\n  a: 1\n  b: [x, y]\n", i)
			default:
				fmt.Fprintf(&sb, "k%d:\n  - 1\n  - {p: q}\n", i)
			}
		}
		var tr Tree
		if err := Build(&tr, []byte(sb.String()), -1); err != nil {
			t.Fatal(err)
		}
		for par := int32(1); int(par) < len(tr.Nodes); par++ {
			if tr.Nodes[par].Kind != Mapping {
				continue
			}
			i := 0
			for c := tr.First(par); c != 0; c = tr.Nodes[c].Next {
				if got, want := tr.IsKey(c), i%2 == 0; got != want {
					t.Fatalf("n=%d mapping %d child %d of %d: IsKey %v", n, par, i, tr.Count(par), got)
				}
				i++
			}
		}
	}
}
