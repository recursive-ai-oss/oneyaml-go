package oneyaml

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNodeIterationAndIndexing(t *testing.T) {
	d, _ := Parse([]byte("- a\n- b\n- c\n"))
	r := d.Root()
	var viaItems, viaIndex []string
	for it := range r.Items() {
		viaItems = append(viaItems, it.Value())
	}
	// Mixed access: forwards, backwards, repeated, out of range.
	for _, i := range []int{0, 1, 2, 1, 0, 2, 2, 3, -1} {
		if n := r.Item(i); n.IsValid() {
			viaIndex = append(viaIndex, n.Value())
		} else {
			viaIndex = append(viaIndex, "-")
		}
	}
	if got := strings.Join(viaItems, ""); got != "abc" {
		t.Errorf("Items = %q", got)
	}
	if got := strings.Join(viaIndex, ""); got != "abcbacc--" {
		t.Errorf("Item = %q", got)
	}
	// Breaking out of the loop stops it.
	n := 0
	for range r.Items() {
		n++
		break
	}
	if n != 1 {
		t.Errorf("break: %d", n)
	}
	// An edit invalidates the cached position.
	d, _ = Parse([]byte("k1: 1\nk2: 2\nk3: 3\n"))
	m := d.Root()
	if k, v := m.Entry(2); k.Value() != "k3" || v.Value() != "3" {
		t.Fatalf("entry 2 = %s %s", k.Value(), v.Value())
	}
	if err := d.Remove("/k1"); err != nil {
		t.Fatal(err)
	}
	m = d.Root()
	if k, _ := m.Entry(1); k.Value() != "k3" {
		t.Errorf("after edit entry 1 = %s", k.Value())
	}
	if k, _ := m.Entry(0); k.Value() != "k2" {
		t.Errorf("after edit entry 0 = %s", k.Value())
	}
	// Non-collections yield nothing.
	d, _ = Parse([]byte("x"))
	for range d.Root().Items() {
		t.Error("scalar yielded an item")
	}
	for range d.Root().Entries() {
		t.Error("scalar yielded an entry")
	}
}

func TestSequentialItemAccessIsLinear(t *testing.T) {
	var sb strings.Builder
	const n = 100000
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "- %d\n", i)
	}
	d, _ := Parse([]byte(sb.String()))
	r := d.Root()
	// Walking from the start for every call takes about n*n/2 = 5e9 link steps,
	// many seconds; with the cursor it is n steps.
	start := time.Now()
	for i := 0; i < n; i++ {
		if r.Item(i).Source() != strconv.Itoa(i) {
			t.Fatal("wrong item")
		}
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("%d sequential Item calls took %v", n, el)
	}
}

func TestTagAccessors(t *testing.T) {
	d, err := Parse([]byte("- !<tag:x,2000:y> a\n- !e!z b\n- !!str c\n- d\n- e\n- ! f\n- !a%21 g\n"))
	if err == nil {
		// !e! is not declared, so the parser must refuse the second item.
		t.Fatalf("undeclared handle accepted: %v", d)
	}
	d, err = Parse([]byte("%TAG !e! tag:example.com,2000:\n---\n- !<tag:x,2000:y> a\n- !e!z%21 b\n- !!str c\n- d\n- ! f\n- !a%21 g\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		written, tag string
		ok           bool
	}{
		{"!<tag:x,2000:y>", "tag:x,2000:y", true},
		{"!e!z%21", "tag:example.com,2000:z!", true},
		{"!!str", "tag:yaml.org,2002:str", true},
		{"", "", false},
		{"!", "!", true},
		{"!a%21", "!a!", true},
	}
	i := 0
	for it := range d.Root().Items() {
		tag, ok := it.ExplicitTag()
		w := want[i]
		if tag != w.tag || ok != w.ok || it.TagAsWritten() != w.written {
			t.Errorf("item %d: ExplicitTag %q %v, written %q; want %+v", i, tag, ok, it.TagAsWritten(), w)
		}
		i++
	}
}

// An empty node has a position at the indicator it belongs to and an empty span;
// the value is the empty string.
func TestEmptyNodePositions(t *testing.T) {
	d, _ := Parse([]byte("a:\nb: &x\nc: !!str\n- not\n"[:len("a:\nb: &x\nc: !!str\n")]))
	r := d.Root()
	for k, v := range r.Entries() {
		s, e := v.Span()
		if s != e || v.Value() != "" {
			t.Errorf("%s: span %d-%d value %q", k.Value(), s, e, v.Value())
		}
		line, col := v.Position()
		if line != 1+map[string]int{"a": 0, "b": 1, "c": 2}[k.Value()] || col < 2 {
			t.Errorf("%s: position %d:%d", k.Value(), line, col)
		}
	}
	if _, ok := mustLookup(r, "c").ExplicitTag(); !ok {
		t.Error("empty tagged value lost its tag")
	}
	if mustLookup(r, "b").Anchor() != "x" {
		t.Error("empty anchored value lost its anchor")
	}
}

func mustLookup(n Node, k string) Node { v, _ := n.Lookup(k); return v }
