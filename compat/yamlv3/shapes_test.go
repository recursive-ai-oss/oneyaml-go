package yaml_test

import (
	"strings"
	"testing"

	yaml "oneyaml.dev/go/compat/yamlv3"
)

// The comment of an item with no value goes to the first scalar of the
// collection that follows it, and an item left empty at the end of the text
// hands its comments to the document: both as yaml.v3 reads them.
func TestCommentsOfEmptyItems(t *testing.T) {
	for _, c := range []struct{ in, head, foot string }{
		// head is the head comment of the first key (or scalar) found; foot is the
		// document's foot comment.
		{"- # c\n  a: 1\n", "# c", ""},
		{"- # c\n\n  # d\n  a: 1\n", "# c\n\n# d", ""},
		{"- x\n- # c\n  - y\n", "# c", ""},
		{"- # c\n", "", "# c"},
		{"- # c\n\n", "", "# c\n"},
		{"- # c\n# d\n", "", "# c\n# d"},
		{"- # c\n\n# d\n", "", "# c\n\n# d"},
		{"a:\n  - # c\n", "", "# c"},
		{"- x\n-\n  # c\n", "", "# c"},
		{"--- # c\n\n", "", "# c\n"},
	} {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(c.in), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.FootComment != c.foot {
			t.Errorf("%q: document foot %q, want %q", c.in, doc.FootComment, c.foot)
		}
		if got := firstHead(&doc); got != c.head {
			t.Errorf("%q: first head comment %q, want %q", c.in, got, c.head)
		}
	}
}

// firstHead returns the first head comment in document order.
func firstHead(n *yaml.Node) string {
	for _, c := range n.Content {
		if c.HeadComment != "" {
			return c.HeadComment
		}
		if h := firstHead(c); h != "" {
			return h
		}
	}
	return ""
}

// A folded scalar that starts with a line break gets a blank line between its
// text lines, and one more at its end, as libyaml writes it.
func TestFoldedScalarStartingWithBreak(t *testing.T) {
	n := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "k"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.FoldedStyle, Value: "\na b\nc\n"},
	}}
	out, err := yaml.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	const want = "k: >4\n\n    a b\n\n    c\n\n"
	if string(out) != want {
		t.Errorf("wrote %q, want %q", out, want)
	}
}

// A folded scalar must read back as the value that was written, whatever it
// starts with and whatever follows its line breaks: a blank line goes only where
// folding would otherwise turn a break into a space.
func TestFoldedScalarReadsBack(t *testing.T) {
	for _, v := range []string{
		"\na\n\n", "\na\n b", "\na\n\tb", "aaaa\n a", "\na\n\n\nb\n", "a\n\n b\n", "\n a\nb\n", "a\n\n", " a\nb\n\n",
	} {
		n := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "k"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.FoldedStyle, Value: v},
		}}
		out, err := yaml.Marshal(n)
		if err != nil {
			t.Fatal(err)
		}
		var back map[string]string
		if err := yaml.Unmarshal(out, &back); err != nil || back["k"] != v {
			t.Errorf("%q: wrote %q, read back %q (%v)", v, out, back["k"], err)
		}
	}
}

// Comments next to an item with no value are kept where yaml.v3 puts them.
func TestDashCommentPlacement(t *testing.T) {
	var doc yaml.Node
	// The comment goes to the flow mapping, the first node of the item.
	if err := yaml.Unmarshal([]byte("- # c\n  - {a: 1}\n"), &doc); err != nil {
		t.Fatal(err)
	}
	if got := firstHead(&doc); got != "# c" {
		t.Errorf("flow mapping head %q, want %q", got, "# c")
	}
	out, err := yaml.Marshal(&doc)
	if err != nil || string(out) != "- # c\n  - {a: 1}\n" {
		t.Errorf("wrote %q (%v)", out, err)
	}
	// The foot of the item before the dash line stays with that item.
	doc = yaml.Node{}
	if err := yaml.Unmarshal([]byte("- v\n# f\n\n- # c\n\n  # d\n# e\n  k0: v\n"), &doc); err != nil {
		t.Fatal(err)
	}
	if foot := doc.Content[0].Content[0].FootComment; foot != "# f" {
		t.Errorf("foot of the first item %q, want %q", foot, "# f")
	}
	// The line of the document start stays on the item; the dash line goes on to its first key.
	doc = yaml.Node{}
	if err := yaml.Unmarshal([]byte("--- # a\n- # c\n  k: v\n"), &doc); err != nil {
		t.Fatal(err)
	}
	if h := doc.Content[0].Content[0]; h.HeadComment != "# a" || h.Content[0].HeadComment != "# c" {
		t.Errorf("item head %q, first key head %q, want %q and %q", h.HeadComment, h.Content[0].HeadComment, "# a", "# c")
	}
}

// A foot comment held by a block collection that is an item is written once.
func TestItemCollectionFootWrittenOnce(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("- k: v\n# c\n\n- # d\n  - v\n"), &doc); err != nil {
		t.Fatal(err)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(out), "# c"); n != 1 {
		t.Errorf("# c written %d times in %q", n, out)
	}
}

// commentLines counts the comment lines the tree holds.
func commentLines(n *yaml.Node) int {
	c := 0
	for _, s := range []string{n.HeadComment, n.LineComment, n.FootComment} {
		for _, l := range strings.Split(s, "\n") {
			if strings.TrimSpace(l) != "" {
				c++
			}
		}
	}
	for _, k := range n.Content {
		c += commentLines(k)
	}
	return c
}

// Comment lines that stand above a dash line belong to one node, however many
// collections start on that line. A line is never given to two nodes, so nothing
// is written twice and the text does not grow when it is read and written again.
func TestDashCommentLinesAreReadOnce(t *testing.T) {
	var doc yaml.Node
	in := "- k: v\n- # a\n  # b\n  - # c\n    k0: v\n"
	if err := yaml.Unmarshal([]byte(in), &doc); err != nil {
		t.Fatal(err)
	}
	m := doc.Content[0].Content[1].Content[0]
	if m.Kind != yaml.MappingNode || m.HeadComment != "# a\n# b" {
		t.Errorf("mapping head comment %q, want %q", m.HeadComment, "# a\n# b")
	}
	if k := m.Content[0]; k.Value != "k0" || k.HeadComment != "# c" {
		t.Errorf("key %q head comment %q, want %q", k.Value, k.HeadComment, "# c")
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(out), "# a"); got != 1 {
		t.Errorf("# a written %d times in %q", got, out)
	}

	// For each input every comment line of the text is read once: the same
	// number of lines comes back, and again after writing and reading.
	for _, c := range []struct {
		in   string
		want int
	}{
		{"  - # c\n    - # c\n      - v # l\n", 3},
		{"- v\n- # c\n\n  - # c\n      k2: v\n", 2},
		{"- v\n- # c\n\n# d\n  - # c\n      text\n", 3},
		{"k1:\n  - # c\n# d\n    - # c\n      - v\n", 3},
		{"a:\n\n  - # c\n# d\n\n    - # c\n      k1: v # l\n", 4},
		{"k0:\n# c\n\n  - # c\n    - # c\n      - v # l\n# c\n# d\n", 6},
		{"- \n# d\n  - # c\n    k0: v\n", 2},
		{"- {a: 1} # l\n- \n# d\n  - # c\n    k0: v\n", 3},
	} {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(c.in), &n); err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if got := commentLines(&n); got != c.want {
			t.Errorf("%q: read %d comment lines, want %d", c.in, got, c.want)
		}
		out, err := yaml.Marshal(&n)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		var back yaml.Node
		if err := yaml.Unmarshal(out, &back); err != nil {
			t.Fatalf("%q: wrote %q: %v", c.in, out, err)
		}
		if got := commentLines(&back); got != c.want {
			t.Errorf("%q: wrote %q, which reads %d comment lines, want %d", c.in, out, got, c.want)
		}
	}
}

// Comments after the last entry of a document go to the entry that closes it.
// When that entry has a foot comment already they are kept with the document or
// added to the entry's, and never dropped.
func TestEndOfDocumentCommentsAreKept(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
	}{
		{"- x\n- \n  k1:\n      k2: [a, b]\n# d\n  k2: v\n# c\n", 2},
		{"k0:\n# c\n\n  - # c\n    - # c\n      - v # l\n# c\n# d\n", 6},
	} {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(c.in), &n); err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if got := commentLines(&n); got != c.want {
			t.Errorf("%q: read %d comment lines, want %d", c.in, got, c.want)
		}
	}
}

// A comment block and a blank line right under a key whose value is a mapping
// are the head of the mapping's first key with what follows, not the foot of
// the key above, as yaml.v3 reads them.
func TestCommentsUnderAKeyWithAMappingValue(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("k:\n  # a\n\n  # b\n  j: v\n"), &doc); err != nil {
		t.Fatal(err)
	}
	m := doc.Content[0]
	if m.Content[0].FootComment != "" {
		t.Errorf("key k has foot comment %q", m.Content[0].FootComment)
	}
	if j := m.Content[1].Content[0]; j.HeadComment != "# a\n\n# b" {
		t.Errorf("key j head comment %q, want %q", j.HeadComment, "# a\n\n# b")
	}
}
