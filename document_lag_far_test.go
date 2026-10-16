package oneyaml

import (
	"fmt"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/cst"
)

// A Set far ahead of the nodes that are true, and one far behind them, in a mapping
// that has a key index, give the text and tree an eager shift gives.
func TestLazySetFarAheadAndBehind(t *testing.T) {
	prev := cst.LagPoison
	cst.LagPoison = 1 << 20
	defer func() { cst.LagPoison = prev }()
	var src strings.Builder
	for i := 0; i < 900; i++ {
		fmt.Fprintf(&src, "key%d: %d\n", i, i%10)
	}
	ref, _ := Parse([]byte(src.String()))
	lazy, _ := Parse([]byte(src.String()))
	set := func(ptr string, v any) {
		t.Helper()
		noLag = true
		errR := ref.Set(ptr, v)
		noLag = false
		errL := lazy.Set(ptr, v)
		if errR != nil || errL != nil {
			t.Fatalf("%s: %v %v", ptr, errR, errL)
		}
		if a, b := string(ref.Bytes()), string(lazy.Bytes()); a != b {
			t.Fatalf("%s: lazy text differs from eager text", ptr)
		}
	}
	for i := 0; i < 8; i++ { // builds the key index and starts a lag
		set(fmt.Sprintf("/key%d", i), "a longer value")
	}
	if !lazy.s.tree.Lagging() {
		t.Fatal("no lag after a run of Sets: the test does not test the lazy path")
	}
	set("/key850", "x") // far ahead of the true nodes
	set("/key860", "much longer than before")
	set("/key20", "y") // far behind them
	set("/key700", "z")
	set("/key3", "w")
	lazy.s.tree.Settle()
	if err := sameAsFreshParse(lazy); err != nil {
		t.Fatal(err)
	}
}
