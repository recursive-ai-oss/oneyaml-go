package yaml_test

import (
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
