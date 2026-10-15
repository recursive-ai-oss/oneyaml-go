package cst

import (
	"testing"
	"unsafe"
)

// An empty key is placed at its own indicator, not at the next token, which is on
// a later line and would make the entry look as long as the lines up to it.
func TestEmptyKeyIsAtItsIndicator(t *testing.T) {
	for _, tc := range []struct {
		src  string
		node int32 // the key, by node index
		at   int32
	}{
		{"?\nk: 1\n", 2, 1},
		{"?\n?\nk: 1\n", 4, 3},
		{"a: 1\n?\nb: 2\n", 4, 6},
		{"a: 1\n  # c\n?  # d\nb: 2\n", 4, 12},
		{"x:\n  ?\n  k: 1\n", 4, 6},
		{": v\nk: 1\n", 2, 0},
		{"a: 1\n: v\n", 4, 5},
		{" ?\n k: 1\n", 2, 2},
	} {
		var tr Tree
		if err := Build(&tr, []byte(tc.src), -1); err != nil {
			t.Fatalf("%q: %v", tc.src, err)
		}
		n := tr.Nodes[tc.node]
		if n.Kind != Scalar || n.Start != n.End || n.Start != tc.at {
			t.Errorf("%q: node %d is %+v, want an empty scalar at %d", tc.src, tc.node, n, tc.at)
		}
	}
}

// The empty value of an empty explicit key takes its colon from the "?" column; a
// colon further down belongs to the next entry, and the value stays by its key.
func TestEmptyValueColonBelongsToItsKey(t *testing.T) {
	for _, src := range []string{
		"0:\n 000:\n    ?\n:\n - x",
		"a:\n  ?\nb: 1\n:\n",
		"x: 1\n?\n:\n",
	} {
		var tr Tree
		if err := Build(&tr, []byte(src), -1); err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		found := false
		for i := int32(1); int(i) < len(tr.Nodes); i++ {
			k := tr.Nodes[i]
			if k.Kind != Scalar || k.Start != k.End || k.Next == 0 || !tr.IsKey(i) || k.Start == 0 || src[k.Start-1] != '?' {
				continue
			}
			v := tr.Nodes[k.Next]
			found = true
			if v.Kind == Scalar && v.Start == v.End && src[v.Start-1] == ':' && colOf([]byte(src), int(v.Start)-1) != colOf([]byte(src), int(k.Start)-1) {
				t.Errorf("%q: the value of the empty key at %d is placed after a colon at %d in another column", src, k.Start, v.Start-1)
			}
		}
		if !found {
			t.Errorf("%q: no empty explicit key found", src)
		}
	}
}

// The empty value that ends a nested explicit-key mapping stays inside it; a colon
// in a shallower column starts an entry of the outer mapping.
func TestEmptyValueDoesNotTakeOuterColon(t *testing.T) {
	src := "0:\n : ? 0\n:"
	var tr Tree
	if err := Build(&tr, []byte(src), -1); err != nil {
		t.Fatal(err)
	}
	nested := tr.Nodes[3]
	if nested.Kind != Mapping || nested.End > 9 {
		t.Fatalf("nested mapping is %+v, want it to end by offset 9", nested)
	}
}

// Children of a collection follow each other in the source: an empty value is
// never placed after a colon that starts the next entry's plain scalar, as in
// "::", where the first colon is part of the key.
func TestChildrenAreInSourceOrder(t *testing.T) {
	for _, src := range []string{
		"?\n::", "?\n:", "? \n::", "?\n: :", "? a\n::", "x:\n  ?\n  ::\n",
		"a: 1\n?\n::\n", "{?\n::}",
	} {
		var tr Tree
		if err := Build(&tr, []byte(src), -1); err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		for i := int32(1); int(i) < len(tr.Nodes); i++ {
			n := tr.Nodes[i]
			if n.Start > n.End {
				t.Errorf("%q: node %d runs backwards: %+v", src, i, n)
			}
			if n.Next != 0 {
				if nx := tr.Nodes[n.Next]; nx.Start < n.End {
					t.Errorf("%q: node %d [%d,%d) is followed by node %d [%d,%d)", src, i, n.Start, n.End, n.Next, nx.Start, nx.End)
				}
			}
		}
	}
}

// The empty value of an entry sits right after the entry's own colon. For an entry
// that begins with "?" the colon is in the "?" column even when the key starts on
// a later line; for an implicit key, including one nested in a "?" key, it is on
// the key's line.
func TestEmptyValueAfterItsOwnColon(t *testing.T) {
	for _, tc := range []struct {
		src string
		key string // text of the key whose value is empty
		at  int32  // offset just after that value's colon
	}{
		{"?\n  a\n:", "a", 7},
		{"? # c\n  a\n:", "a", 11},
		{"?\n  !!str a\n:", "a", 13},
		{"?\n  - a\n:\nb: 1\n", "", 0},
		{"? a:", "a", 4},
		{"? a :", "a", 5},
		{"- ? a:", "a", 6},
	} {
		var tr Tree
		if err := Build(&tr, []byte(tc.src), -1); err != nil {
			t.Fatalf("%q: %v", tc.src, err)
		}
		if tc.key == "" {
			// The key is a sequence; its entry's value is the empty node after it.
			for i := int32(1); int(i) < len(tr.Nodes); i++ {
				n := tr.Nodes[i]
				if n.Kind == Sequence && tr.IsKey(i) {
					v := tr.Nodes[n.Next]
					if v.Start != v.End || tc.src[v.Start-1] != ':' {
						t.Errorf("%q: empty value at %d..%d is not right after a colon", tc.src, v.Start, v.End)
					}
				}
			}
			continue
		}
		found := false
		for i := int32(1); int(i) < len(tr.Nodes); i++ {
			k := tr.Nodes[i]
			if k.Kind != Scalar || k.Next == 0 || !tr.IsKey(i) || tc.src[k.Start:k.End] != tc.key {
				continue
			}
			v := tr.Nodes[k.Next]
			found = true
			if v.Start != v.End || v.Start != tc.at {
				t.Errorf("%q: the empty value of %q is at %d..%d, want %d", tc.src, tc.key, v.Start, v.End, tc.at)
			}
		}
		if !found {
			t.Errorf("%q: key %q not found", tc.src, tc.key)
		}
	}
}

// A colon only marks a value when blank follows it. "?x" is a plain key that
// starts with a question mark, not an explicit-key indicator, and "::" starts a
// plain scalar; neither may move an empty value away from its own colon.
func TestColonNeedsBlankAfterIt(t *testing.T) {
	for _, tc := range []struct {
		src  string
		node int32
		at   int32
	}{
		{"?x:", 3, 3},
		{"?x :\nb: 1", 3, 4},
		{"- ?x:", 4, 5},
		{"x:\n  ?x:\n  b: 1", 5, 8},
		{"? a\n::\n", 3, 3},
		{"? a\n::\n", 5, 6},
	} {
		var tr Tree
		if err := Build(&tr, []byte(tc.src), -1); err != nil {
			t.Fatalf("%q: %v", tc.src, err)
		}
		n := tr.Nodes[tc.node]
		if n.Kind != Scalar || n.Start != n.End || n.Start != tc.at {
			t.Errorf("%q: node %d is %+v, want an empty scalar at %d", tc.src, tc.node, n, tc.at)
		}
	}
}

// A node is 28 bytes. Dense flow text has a node for every few bytes of input, so
// the size of the node decides whether a parsed document stays within a small
// multiple of its text.
func TestNodeSize(t *testing.T) {
	if s := unsafe.Sizeof(Node{}); s > 28 {
		t.Errorf("Node is %d bytes, want at most 28", s)
	}
}
