package oneyaml

import (
	"bytes"
	"testing"
)

// A Node an Unmarshaler keeps from any read goes on reading its own subtree,
// whatever is read afterwards, on every route. The kept Node of each read is
// taken by the hook of that read, so two reads never share a variable.

type keepHook struct {
	n *Node
	s string
}

func (k *keepHook) UnmarshalYAML(n Node) error {
	if k.n != nil {
		*k.n = n
	}
	return n.Decode(&k.s)
}

func readKept(t *testing.T, name string, n Node, want string) {
	t.Helper()
	var s string
	var out any
	if err := n.Decode(&s); err != nil || s != want {
		t.Errorf("%s: Decode gave %q, %v; want %q", name, s, err, want)
	}
	if err := n.Decode(&out); err != nil || out != want {
		t.Errorf("%s: Decode into any gave %v, %v; want %q", name, out, err, want)
	}
	if err := n.Err(); err != nil || n.Value() != want || !n.IsValid() {
		t.Errorf("%s: Err %v Value %q valid %v; want %q", name, err, n.Value(), n.IsValid(), want)
	}
}

func TestNodeKeptFromAnyRouteKeepsReadingItsOwnText(t *testing.T) {
	var fromUnmarshal, fromDecoder, fromDocument Node
	var a struct{ X keepHook }
	a.X.n = &fromUnmarshal
	if err := Unmarshal([]byte("x: first\n"), &a); err != nil {
		t.Fatal(err)
	}
	var b struct{ X keepHook }
	b.X.n = &fromDecoder
	dec := NewDecoder(bytes.NewReader([]byte("x: second\n---\nx: other\n---\nx: other\n---\nx: other\n")))
	if err := dec.Decode(&b); err != nil {
		t.Fatal(err)
	}
	doc, err := Parse([]byte("x: third\n"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct{ X keepHook }
	c.X.n = &fromDocument
	if err := doc.Decode(&c); err != nil {
		t.Fatal(err)
	}
	// Later reads of every kind, each with a hook that keeps its own Node.
	for i := 0; i < 3; i++ {
		var n Node
		var d struct{ X keepHook }
		d.X.n = &n
		if err := Unmarshal([]byte("x: later\n"), &d); err != nil {
			t.Fatal(err)
		}
		var e struct{ X keepHook }
		e.X.n = &n
		if err := dec.Decode(&e); err != nil {
			t.Fatal(err)
		}
	}
	readKept(t, "Unmarshal", fromUnmarshal, "first")
	readKept(t, "Decoder", fromDecoder, "second")
	readKept(t, "Document.Decode", fromDocument, "third")
}

func TestNodeKeptFromADocumentReadGoesStaleWhenTheDocumentIsEdited(t *testing.T) {
	var kept Node
	doc, err := Parse([]byte("x: third\n"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct{ X keepHook }
	c.X.n = &kept
	if err := doc.Decode(&c); err != nil {
		t.Fatal(err)
	}
	if err := doc.Set("/x", "edited"); err != nil {
		t.Fatal(err)
	}
	if !kept.Stale() || kept.Err() == nil {
		t.Errorf("kept Node after an edit: Stale %v Err %v", kept.Stale(), kept.Err())
	}
}
