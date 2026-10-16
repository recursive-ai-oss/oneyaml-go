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
