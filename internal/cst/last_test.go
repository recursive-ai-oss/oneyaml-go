package cst

import (
	"os"
	"path/filepath"
	"testing"
)

// lastWalk and prevWalk are the definitions Last and Prev must agree with.
func lastWalk(t *Tree, n int32) int32 {
	l := int32(0)
	for ch := t.First(n); ch != 0; ch = t.Nodes[ch].Next {
		l = ch
	}
	return l
}

func prevWalk(t *Tree, n int32) int32 {
	par := t.Nodes[n].Parent
	if par == 0 {
		return 0
	}
	p := int32(0)
	for c := t.First(par); c != 0 && c != n; c = t.Nodes[c].Next {
		p = c
	}
	return p
}

func checkTree(t *testing.T, name string, src []byte) {
	var tr Tree
	if err := Build(&tr, src, -1); err != nil {
		return
	}
	for i := 1; i < len(tr.Nodes); i++ {
		n := int32(i)
		if got, want := tr.Last(n), lastWalk(&tr, n); got != want {
			t.Fatalf("%s: Last(%d) = %d, want %d", name, n, got, want)
		}
		if got, want := tr.Prev(n), prevWalk(&tr, n); got != want {
			t.Fatalf("%s: Prev(%d) = %d, want %d", name, n, got, want)
		}
	}
}

// Last and Prev read the node order instead of walking the sibling links; they
// must give what the walk gives.
func TestLastAndPrevAgreeWithTheLinks(t *testing.T) {
	docs := []string{
		"a: 1\nb: [1, 2, {c: d}]\ne:\n  - x\n  - y: z\n    w: [q]\n",
		"- - - a\n    - b\n  - c\n- d\n",
		"? [a, b]\n: {c: d}\n? e\n",
		"[: 0]\n",
		"a: &x 1\nb: *x\nc: !!str\nd:\n",
		"---\na: 1\n...\n---\nb: [1]\n---\n- x\n---\n",
		"[a: b, c, {d: e}]\n",
		"{a: , b: [], c: {}}\n",
		"",
		"x\n",
	}
	for i, d := range docs {
		checkTree(t, "doc"+string(rune('0'+i)), []byte(d))
	}
	// Every input of the suite, when it is at hand.
	if dir := os.Getenv("ONEYAML_YTS_DIR"); dir != "" {
		files, _ := filepath.Glob(filepath.Join(dir, "*", "in.yaml"))
		more, _ := filepath.Glob(filepath.Join(dir, "*", "*", "in.yaml"))
		for _, f := range append(files, more...) {
			b, err := os.ReadFile(f)
			if err == nil {
				checkTree(t, f, b)
			}
		}
	}
}
