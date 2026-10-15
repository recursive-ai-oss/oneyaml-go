package bench

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	yv3 "go.yaml.in/yaml/v3"
	compat "oneyaml.dev/go/compat/yamlv3"
)

// This file holds the shim against yaml.v3 on what a hostile or odd Node, and a
// few text corner cases, do: every case is run through both libraries and the
// results must agree.

func v3s(v string) *yv3.Node       { return &yv3.Node{Kind: yv3.ScalarNode, Tag: "!!str", Value: v} }
func mes(v string) *compat.Node    { return &compat.Node{Kind: compat.ScalarNode, Tag: "!!str", Value: v} }
func v3m(c ...*yv3.Node) *yv3.Node { return &yv3.Node{Kind: yv3.MappingNode, Tag: "!!map", Content: c} }
func mem(c ...*compat.Node) *compat.Node {
	return &compat.Node{Kind: compat.MappingNode, Tag: "!!map", Content: c}
}

func TestCompatV3MarshalOddNodes(t *testing.T) {
	lines := []struct {
		name         string
		keyLine, val string
		valLine      string
		style        yv3.Style
	}{
		{"value line comment", "", "v", "c\nevil: 1", 0},
		{"key line comment", "c\nevil: 1", "v", "", 0},
		{"two lines", "", "v", "c1\nc2", 0},
		{"hash lines", "", "v", "#c1\n#c2", 0},
		{"blank line inside", "", "v", "c1\n\nc3", 0},
		{"trailing break", "", "v", "c1\n", 0},
		{"block scalar", "", "a\nb\n", "c1\nevil: 2", yv3.LiteralStyle},
		{"folded scalar", "", "a b\n", "c1\nc2\n\nc3", yv3.FoldedStyle},
	}
	for _, l := range lines {
		for _, depth := range []int{0, 1, 2} {
			mk3 := func() *yv3.Node {
				k, v := v3s("k"), v3s(l.val)
				k.LineComment, v.LineComment, v.Style = l.keyLine, l.valLine, l.style
				n := v3m(k, v)
				for i := 0; i < depth; i++ {
					n = v3m(v3s("o"), n)
				}
				return n
			}
			mkMe := func() *compat.Node {
				k, v := mes("k"), mes(l.val)
				k.LineComment, v.LineComment, v.Style = l.keyLine, l.valLine, compat.Style(l.style)
				n := mem(k, v)
				for i := 0; i < depth; i++ {
					n = mem(mes("o"), n)
				}
				return n
			}
			want, werr := yv3.Marshal(mk3())
			got, gerr := compat.Marshal(mkMe())
			if l.style != 0 && strings.Contains(strings.TrimRight(l.valLine, "\n"), "\n") {
				// yaml.v3 writes the further lines of the comment of a block scalar
				// between its header and its text, where they become part of the text.
				// The shim writes them after the scalar, so the text keeps its value.
				var back interface{}
				if gerr != nil || compat.Unmarshal(got, &back) != nil || !strings.Contains(fmt.Sprint(back), strings.TrimSpace(l.val)) {
					t.Errorf("%s depth %d: %q does not read back as %q (%v)", l.name, depth, got, l.val, gerr)
				}
				continue
			}
			if (werr != nil) != (gerr != nil) || string(want) != string(got) {
				t.Errorf("%s depth %d:\n yaml.v3 %q %v\n shim    %q %v", l.name, depth, want, werr, got, gerr)
			}
		}
	}
	// A comment on a sequence item and on a flow collection.
	{
		w := &yv3.Node{Kind: yv3.SequenceNode, Tag: "!!seq", Style: yv3.FlowStyle, LineComment: "a\nb", Content: []*yv3.Node{{Kind: yv3.ScalarNode, Value: "i"}}}
		g := &compat.Node{Kind: compat.SequenceNode, Tag: "!!seq", Style: compat.FlowStyle, LineComment: "a\nb", Content: []*compat.Node{{Kind: compat.ScalarNode, Value: "i"}}}
		want, _ := yv3.Marshal(w)
		got, _ := compat.Marshal(g)
		if string(want) != string(got) {
			t.Errorf("flow: %q vs %q", want, got)
		}
	}
	// Names of anchors and aliases.
	for _, name := range []string{"a\nevil: 1\nx", "a b", "a]", "a:b", "é1", "ok_1-A", ""} {
		w := v3s("v")
		w.Anchor = name
		g := mes("v")
		g.Anchor = name
		_, werr := yv3.Marshal(v3m(v3s("k"), w))
		_, gerr := compat.Marshal(mem(mes("k"), g))
		if fmt.Sprint(werr) != fmt.Sprint(gerr) {
			t.Errorf("anchor %q: %v vs %v", name, werr, gerr)
		}
		_, werr = yv3.Marshal(&yv3.Node{Kind: yv3.SequenceNode, Tag: "!!seq", Content: []*yv3.Node{{Kind: yv3.AliasNode, Value: name}}})
		_, gerr = compat.Marshal(&compat.Node{Kind: compat.SequenceNode, Tag: "!!seq", Content: []*compat.Node{{Kind: compat.AliasNode, Value: name}}})
		if fmt.Sprint(werr) != fmt.Sprint(gerr) {
			t.Errorf("alias %q: %v vs %v", name, werr, gerr)
		}
	}
}

// The first document of a stream, read into a Node and written again, keeps
// what yaml.v3 keeps of the comments around it and nothing of the later documents.
func TestCompatV3FirstDocumentComments(t *testing.T) {
	for _, in := range []string{
		"a: 1\n---\nb: 2\n# tail\n",
		"a: 1\n---\nb: 2\n\n# tail\n",
		"a: 1\n# foot\n---\nb: 2\n",
		"a: 1\n\n# foot\n---\nb: 2\n",
		"a: 1\n# f\n\n# g\n---\nb: 1\n",
		"a: 1\n---\n# h2\nb: 2\n",
		"a: 1\n# foot\n",
		"a: 1\n\n# foot\n",
	} {
		var w yv3.Node
		var g compat.Node
		if err := yv3.Unmarshal([]byte(in), &w); err != nil {
			t.Fatal(err)
		}
		if err := compat.Unmarshal([]byte(in), &g); err != nil {
			t.Fatal(err)
		}
		want, _ := yv3.Marshal(&w)
		got, _ := compat.Marshal(&g)
		if string(want) != string(got) {
			t.Errorf("%q:\n yaml.v3 %q\n shim    %q", in, want, got)
		}
	}
}

func TestCompatV3DotNumbers(t *testing.T) {
	for _, in := range []string{"x: ._5\n", "x: .5_0\n", "x: .5_\n", "x: .5__0\n", "x: ._\n", "x: .1_0e1_0\n", "x: .5e_3\n",
		"x: +._5\n", "x: -.5_0\n", "x: 1_0.5\n", "x: 0.5_0\n", "x: ._5e1\n", "x: .5_5_5\n"} {
		var w, g any
		werr := yv3.Unmarshal([]byte(in), &w)
		gerr := compat.Unmarshal([]byte(in), &g)
		if fmt.Sprint(werr) != fmt.Sprint(gerr) || !compatSame(w, g) {
			t.Errorf("%q: yaml.v3 %#v %v, shim %#v %v", in, w, werr, g, gerr)
		}
	}
}

func TestCompatV3KnownFieldsPerDecode(t *testing.T) {
	type T struct{ A int }
	const stream = "a: 1\nb: 2\n---\na: 1\nb: 2\n---\na: 1\nb: 2\n---\na: 1\n"
	wd := yv3.NewDecoder(strings.NewReader(stream))
	gd := compat.NewDecoder(strings.NewReader(stream))
	for i, known := range []bool{false, true, false, true, false} {
		wd.KnownFields(known)
		gd.KnownFields(known)
		var w, g T
		werr, gerr := wd.Decode(&w), gd.Decode(&g)
		if errors.Is(werr, io.EOF) != errors.Is(gerr, io.EOF) || (werr == nil) != (gerr == nil) || w != g {
			t.Fatalf("decode %d known=%v: yaml.v3 %v %+v, shim %v %+v", i, known, werr, w, gerr, g)
		}
		if werr != nil && !errors.Is(werr, io.EOF) && !strings.Contains(fmt.Sprint(gerr), "field b not found") {
			t.Fatalf("decode %d: %v vs %v", i, werr, gerr)
		}
	}
}

// A head comment on the first entry of a collection that starts on the line of
// its parent's dash is written after the dash, not before it, so the collection
// stays the parent's item.
func TestCompatV3HeadCommentOnInlineEntry(t *testing.T) {
	for _, h := range []string{"h", "h1\nh2", "#h1\nh2"} {
		w := func(v string, head string) *yv3.Node {
			return &yv3.Node{Kind: yv3.ScalarNode, Tag: "!!str", Value: v, HeadComment: head}
		}
		g := func(v string, head string) *compat.Node {
			return &compat.Node{Kind: compat.ScalarNode, Tag: "!!str", Value: v, HeadComment: head}
		}
		wn := []*yv3.Node{
			{Kind: yv3.SequenceNode, Tag: "!!seq", Content: []*yv3.Node{
				{Kind: yv3.SequenceNode, Tag: "!!seq", Content: []*yv3.Node{w("a", h), w("b", "")}}}},
			{Kind: yv3.SequenceNode, Tag: "!!seq", Content: []*yv3.Node{
				{Kind: yv3.MappingNode, Tag: "!!map", Content: []*yv3.Node{w("k", h), w("v", ""), w("k2", ""), w("v2", "")}}}},
		}
		gn := []*compat.Node{
			{Kind: compat.SequenceNode, Tag: "!!seq", Content: []*compat.Node{
				{Kind: compat.SequenceNode, Tag: "!!seq", Content: []*compat.Node{g("a", h), g("b", "")}}}},
			{Kind: compat.SequenceNode, Tag: "!!seq", Content: []*compat.Node{
				{Kind: compat.MappingNode, Tag: "!!map", Content: []*compat.Node{g("k", h), g("v", ""), g("k2", ""), g("v2", "")}}}},
		}
		for i := range wn {
			want, _ := yv3.Marshal(wn[i])
			got, _ := compat.Marshal(gn[i])
			if string(want) != string(got) {
				t.Errorf("case %d head %q:\n yaml.v3 %q\n shim    %q", i, h, want, got)
			}
		}
	}
}
