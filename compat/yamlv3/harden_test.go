package yaml_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	oneyaml "oneyaml.dev/go"
	yaml "oneyaml.dev/go/compat/yamlv3"
)

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func mapping(c ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: c}
}

func sequence(c ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: c}
}

// Writing a comment must never let its text become YAML: every line of it is a
// comment line of its own, and the first stays on the line of the node.
func TestLineCommentsCannotInjectYAML(t *testing.T) {
	line := func(v *yaml.Node, k *yaml.Node) *yaml.Node { return mapping(k, v) }
	cases := []struct {
		name string
		node *yaml.Node
		want string
	}{
		{"value", line(withLine(scalar("v"), "c\nevil: 1"), scalar("k")), "k: v # c\n\n# evil: 1\n"},
		{"key", line(scalar("v"), withLine(scalar("k"), "c\nevil: 1")), "k: v # c\n\n# evil: 1\n"},
		{"block scalar", line(withStyle(withLine(scalar("a\nb\n"), "c\nevil: 1"), yaml.LiteralStyle), scalar("k")),
			"k: | # c\n    a\n    b\n# evil: 1\n"},
		{"flow sequence", withLine(&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle, Content: []*yaml.Node{scalar("i")}}, "a\nb"),
			"[i] # a\n\n# b\n"},
		{"nested", mapping(scalar("o"), mapping(scalar("k"), withLine(scalar("v"), "c1\nc2\n#c3\n\nc5"), scalar("z"), scalar("w"))),
			"o:\n    k: v # c1\n    # c2\n    #c3\n\n    # c5\n    z: w\n"},
		{"line separator", line(withLine(scalar("v"), "c\u2028evil: 1"), scalar("k")), "k: v # c\n\n# evil: 1\n"},
		{"carriage return", line(withLine(scalar("v"), "c\revil: 1"), scalar("k")), "k: v # c\n\n# evil: 1\n"},
		{"head comment", withHead(scalar("v"), "h\u0085evil: 1"), "# h\n# evil: 1\nv\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := yaml.Marshal(c.node)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
			// Nothing but the comment grew: reading the output back finds no key
			// that the node did not hold.
			var back yaml.Node
			if err := yaml.Unmarshal(got, &back); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(fmt.Sprint(nodeText(&back)), "evil") {
				t.Fatalf("a comment became content: %q", got)
			}
		})
	}
}

func withLine(n *yaml.Node, c string) *yaml.Node { n.LineComment = c; return n }
func withHead(n *yaml.Node, c string) *yaml.Node { n.HeadComment = c; return n }
func withStyle(n *yaml.Node, s yaml.Style) *yaml.Node {
	n.Style = s
	return n
}

// nodeText collects the scalar values of a tree, which a comment must not reach.
func nodeText(n *yaml.Node) []string {
	var out []string
	var walk func(*yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode {
			out = append(out, n.Value)
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(n)
	return out
}

// Names of anchors and aliases are checked as the writer of yaml.v3 checks them.
func TestAnchorAndAliasNamesAreChecked(t *testing.T) {
	for _, name := range []string{"a\nevil: 1\nx", "a b", "a]", "a:b", "é1", "a,b", "a#"} {
		n := scalar("v")
		n.Anchor = name
		_, err := yaml.Marshal(mapping(scalar("k"), n))
		if err == nil || err.Error() != "yaml: anchor value must contain alphanumerical characters only" {
			t.Errorf("anchor %q: error %v", name, err)
		}
		al := &yaml.Node{Kind: yaml.AliasNode, Value: name}
		_, err = yaml.Marshal(sequence(al))
		if err == nil || err.Error() != "yaml: alias value must contain alphanumerical characters only" {
			t.Errorf("alias %q: error %v", name, err)
		}
	}
	_, err := yaml.Marshal(sequence(&yaml.Node{Kind: yaml.AliasNode}))
	if err == nil || err.Error() != "yaml: alias value must not be empty" {
		t.Errorf("empty alias: %v", err)
	}
	ok := scalar("v")
	ok.Anchor = "a_1-B"
	out, err := yaml.Marshal(sequence(ok, &yaml.Node{Kind: yaml.AliasNode, Value: "a_1-B", Alias: ok}))
	if err != nil || string(out) != "- &a_1-B v\n- *a_1-B\n" {
		t.Errorf("valid names: %q %v", out, err)
	}
}

// A tree with a hole in it is an error to write or decode, never a panic.
func TestNilNodesInContent(t *testing.T) {
	trees := map[string]func() *yaml.Node{
		"seq":     func() *yaml.Node { return sequence(nil) },
		"map":     func() *yaml.Node { return mapping(scalar("a"), nil) },
		"nil key": func() *yaml.Node { return mapping(nil, scalar("a")) },
		"doc":     func() *yaml.Node { return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{nil}} },
		"nested":  func() *yaml.Node { return sequence(scalar("x"), mapping(scalar("k"), sequence(nil))) },
	}
	for name, mk := range trees {
		if _, err := yaml.Marshal(mk()); err == nil {
			t.Errorf("%s: Marshal: no error", name)
		}
		var v interface{}
		if err := mk().Decode(&v); err == nil {
			t.Errorf("%s: Decode: no error", name)
		}
	}
	var nilNode *yaml.Node
	if err := nilNode.Decode(new(interface{})); err == nil {
		t.Error("Decode on a nil node: no error")
	}
}

// A node that contains itself has no end: writing or decoding it fails at once,
// and a tree that shares nodes so much that it would write billions of lines is
// refused.
func TestCyclesAndSharedNodes(t *testing.T) {
	cyc := func() *yaml.Node {
		m := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		m.Content = []*yaml.Node{m}
		return m
	}
	wide := func() *yaml.Node {
		m := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		m.Content = []*yaml.Node{m, m}
		return m
	}
	for name, mk := range map[string]func() *yaml.Node{"one": cyc, "two": wide} {
		done := make(chan error, 2)
		go func() { _, err := yaml.Marshal(mk()); done <- err }()
		go func() { var v interface{}; done <- mk().Decode(&v) }()
		for i := 0; i < 2; i++ {
			if err := <-done; err == nil {
				t.Errorf("%s: no error", name)
			}
		}
	}
	// 40 levels of a node listed twice under its parent: 2^40 nodes.
	node := scalar("x")
	for i := 0; i < 40; i++ {
		node = sequence(node, node)
	}
	var v interface{}
	err := node.Decode(&v)
	var le *oneyaml.LimitError
	if !errors.As(err, &le) {
		t.Errorf("shared tree decode: %v", err)
	}
	if _, err := yaml.Marshal(node); err == nil {
		t.Error("shared tree marshal: no error")
	}
	// A chain deeper than the depth limit is a limit error, not a stack.
	deep := scalar("x")
	for i := 0; i < 20000; i++ {
		deep = sequence(deep)
	}
	if err := deep.Decode(&v); !errors.As(err, &le) || le.Limit != "depth" {
		t.Errorf("deep tree: %v", err)
	}
}

// The comments of a document stop where the document does.
func TestCommentsStayInTheirDocument(t *testing.T) {
	for in, want := range map[string]string{
		"a: 1\n---\nb: 2\n# tail\n":        "a: 1\n",
		"a: 1\n---\nb: 2\n\n# tail\n":      "a: 1\n",
		"a: 1\n# foot\n---\nb: 2\n":        "a: 1\n\n# foot\n",
		"a: 1\n\n# foot\n---\nb: 2\n":      "a: 1\n\n# foot\n",
		"- x\n...\n# after end\n":          "- x\n",
		"# head\n\n---\na: 1\n---\nb: 2\n": "# head\n\na: 1\n",
	} {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(in), &n); err != nil {
			t.Fatal(err)
		}
		out, err := yaml.Marshal(&n)
		if err != nil || string(out) != want {
			t.Errorf("%q: got %q (%v), want %q", in, out, err, want)
		}
	}
	// The second document of a stream has none of the first one's head comment.
	dec := yaml.NewDecoder(strings.NewReader("# head\n\n---\na: 1\n---\nb: 2\n"))
	var first, second yaml.Node
	if err := dec.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(&second); err != nil {
		t.Fatal(err)
	}
	if first.HeadComment != "# head" || second.HeadComment != "" {
		t.Errorf("head comments: %q, %q", first.HeadComment, second.HeadComment)
	}
}

// KnownFields applies to each Decode, whenever it was set.
func TestDecoderKnownFieldsPerDecode(t *testing.T) {
	type T struct{ A int }
	dec := yaml.NewDecoder(strings.NewReader("a: 1\nb: 2\n---\na: 1\nb: 2\n---\na: 1\nb: 2\n---\na: 1\n"))
	var v T
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("known fields off: %v", err)
	}
	dec.KnownFields(true)
	err := dec.Decode(&v)
	var te *yaml.TypeError
	if !errors.As(err, &te) || !strings.Contains(te.Error(), "field b not found in type yaml_test.T") {
		t.Fatalf("known fields on: %v", err)
	}
	dec.KnownFields(false)
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("known fields off again: %v", err)
	}
	dec.KnownFields(true)
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("a document with no unknown field: %v", err)
	}
}

// The alias budget is shared by every decode that one call starts, so a type
// that decodes its own node over and over cannot expand a small document without
// end. The older function style and the node style are both counted.
type bombOld struct{}

var bombOldCalls, bombNodeCalls atomic.Int64

func (o *bombOld) UnmarshalYAML(unmarshal func(interface{}) error) error {
	bombOldCalls.Add(1)
	var xs []bombOld
	if err := unmarshal(&xs); err != nil {
		var le *oneyaml.LimitError
		if errors.As(err, &le) {
			return err
		}
		var s string
		return unmarshal(&s)
	}
	return nil
}

type bombNode struct{}

func (o *bombNode) UnmarshalYAML(n *yaml.Node) error {
	bombNodeCalls.Add(1)
	if n.Kind == yaml.SequenceNode || n.Kind == yaml.AliasNode {
		var xs []bombNode
		return n.Decode(&xs)
	}
	return nil
}

func bombText(levels int) []byte {
	var b strings.Builder
	b.WriteString("- &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i < levels; i++ {
		fmt.Fprintf(&b, "- &a%d [", i)
		for j := 0; j < 10; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "*a%d", i-1)
		}
		b.WriteString("]\n")
	}
	return []byte(b.String())
}

func TestAliasBudgetIsSharedByNestedDecodes(t *testing.T) {
	// Seven levels expand past the million-node budget in a second or so, which
	// keeps the test in the short tier; nine take longer and are the shape the full run uses.
	levels := 7
	if !testing.Short() {
		levels = 9
	}
	in := bombText(levels)
	// The budget is a million nodes, so the call count is bounded by it; before
	// the budget was shared it grew without a bound.
	// Each style counts its own calls, so the two run side by side.
	for name, c := range map[string]struct {
		run   func() error
		calls *atomic.Int64
	}{
		"function style": {func() error { var x []bombOld; return yaml.Unmarshal(in, &x) }, &bombOldCalls},
		"node style":     {func() error { var x []bombNode; return yaml.Unmarshal(in, &x) }, &bombNodeCalls},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := c.run()
			var le *oneyaml.LimitError
			if !errors.As(err, &le) {
				t.Errorf("error %v, want a limit error", err)
			}
			// The call count is the bound.
			if n := c.calls.Load(); n > 1_100_000 {
				t.Errorf("%d calls", n)
			}
		})
	}
}

type bombNodeSafe struct{}

func (o *bombNodeSafe) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.SequenceNode || n.Kind == yaml.AliasNode {
		var xs []bombNodeSafe
		return n.Decode(&xs)
	}
	return nil
}

// Decodes running on different goroutines each have a budget of their own: one
// that runs out must not make another fail, and one that stays within its
// budget must not pay for the other.
func TestAliasBudgetIsPerCallAcrossGoroutines(t *testing.T) {
	big, small := bombText(7), bombText(4)
	var wg sync.WaitGroup
	var bigErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		var x []bombNodeSafe
		bigErr = yaml.Unmarshal(big, &x)
	}()
	smallErrs := make([]error, 30)
	go func() {
		defer wg.Done()
		// Thirty runs of about 110k nodes each would add up to more than the
		// budget if the runs, or the other goroutine, shared one.
		for i := range smallErrs {
			var x []bombNodeSafe
			smallErrs[i] = yaml.Unmarshal(small, &x)
		}
	}()
	wg.Wait()
	var le *oneyaml.LimitError
	if !errors.As(bigErr, &le) {
		t.Errorf("the bomb: error %v, want a limit error", bigErr)
	}
	for i, err := range smallErrs {
		if err != nil {
			t.Fatalf("run %d of the small input: %v", i, err)
		}
	}
}

// A float with more digits than the limit allows is refused when it would be
// converted, as in the primary package, and read as a string when the target is
// a string. (yaml.v3 reads an integer too wide for 64 bits as a string, so it
// never builds a big integer and has nothing to limit there.)
func TestNumberDigitsLimit(t *testing.T) {
	in := []byte("0." + strings.Repeat("3", 200_000))
	var v interface{}
	err := yaml.Unmarshal(in, &v)
	var le *oneyaml.LimitError
	if !errors.As(err, &le) || le.Limit != "number digits" {
		t.Fatalf("any: %v", err)
	}
	var f float64
	if err := yaml.Unmarshal(in, &f); !errors.As(err, &le) {
		t.Fatalf("float: %v", err)
	}
	var s string
	if err := yaml.Unmarshal(in, &s); err != nil || len(s) != len(in) {
		t.Fatalf("string: %d, %v", len(s), err)
	}
}

// Lazy keeps the node it was given and decodes it later, the deferred decoding
// that yaml.v3 programs use.
type lazyNode struct{ n yaml.Node }

func (l *lazyNode) UnmarshalYAML(n *yaml.Node) error { l.n = *n; return nil }

type lazyFunc struct{ f func(interface{}) error }

func (l *lazyFunc) UnmarshalYAML(f func(interface{}) error) error { l.f = f; return nil }

// A node (or function) kept past its call starts a budget of its own for each
// later decode: decoding it again and again must never add up to a limit, and
// must not touch the pooled state of other calls.
func TestKeptNodeDecodesWithFreshBudget(t *testing.T) {
	doc := []byte("a: &a [" + strings.TrimSuffix(strings.Repeat("x, ", 100), ", ") + "]\nb: *a\n")
	var ln lazyNode
	if err := yaml.Unmarshal(doc, &ln); err != nil {
		t.Fatal(err)
	}
	var lf lazyFunc
	if err := yaml.Unmarshal(doc, &lf); err != nil {
		t.Fatal(err)
	}
	// Each decode expands 100 nodes; 12,000 of them would pass the million-node
	// budget if the count carried over.
	for i := 0; i < 12000; i++ {
		var v interface{}
		if err := ln.n.Decode(&v); err != nil {
			t.Fatalf("node, decode %d: %v", i, err)
		}
		if err := lf.f(&v); err != nil {
			t.Fatalf("function, decode %d: %v", i, err)
		}
	}
}

// Decoding a kept node on one goroutine while others unmarshal must not race
// on shared decoder state (run under -race).
func TestKeptNodeDecodeDoesNotRace(t *testing.T) {
	doc := []byte("a: &a [" + strings.TrimSuffix(strings.Repeat("x, ", 100), ", ") + "]\nb: *a\n")
	var ln lazyNode
	if err := yaml.Unmarshal(doc, &ln); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			var v interface{}
			_ = ln.n.Decode(&v)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			var m map[string]interface{}
			_ = yaml.Unmarshal(doc, &m)
			var l lazyNode
			_ = yaml.Unmarshal(doc, &l)
		}
	}()
	wg.Wait()
}

// Comments with characters the reader refuses are not written, so what Marshal
// returns always reads back.
func TestCommentsThatCannotBeWrittenAreRefused(t *testing.T) {
	for name, c := range map[string]string{
		"NUL": "a\x00b", "DEL": "a\x7fb", "C1": "a\u0090b", "BOM": "a\ufeffb",
		"invalid UTF-8": "a\xffb", "escape": "a\x1bb", "U+FFFE": "a\ufffeb",
	} {
		for field, n := range map[string]*yaml.Node{
			"head": {Kind: yaml.ScalarNode, Value: "v", HeadComment: c},
			"line": {Kind: yaml.ScalarNode, Value: "v", LineComment: c},
			"foot": {Kind: yaml.ScalarNode, Value: "v", FootComment: c},
		} {
			if out, err := yaml.Marshal(n); err == nil {
				t.Errorf("%s in a %s comment: written as %q", name, field, out)
			}
		}
	}
	// Tab, NEL and ordinary non-ASCII text are fine and read back.
	n := &yaml.Node{Kind: yaml.ScalarNode, Value: "v", LineComment: "tab\there \u00e9 \u4e16"}
	out, err := yaml.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var s string
	if err := yaml.Unmarshal(out, &s); err != nil || s != "v" {
		t.Fatalf("%q: %q, %v", out, s, err)
	}
}

// A tree that shares one large node many times writes far more text than it holds;
// the writer refuses it by bytes as well as by node count.
func TestSharedLargeNodeIsRefusedByBytes(t *testing.T) {
	big := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: strings.Repeat("x", 1<<20)}
	root := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for i := 0; i < 200; i++ {
		root.Content = append(root.Content, big)
	}
	if out, err := yaml.Marshal(root); err == nil {
		t.Fatalf("wrote %d bytes", len(out))
	}
	// The same tree with a few repeats is fine.
	root.Content = root.Content[:3]
	if _, err := yaml.Marshal(root); err != nil {
		t.Fatal(err)
	}
}

// A tag is written in a form that reads back as the same tag: a shorthand needs a
// suffix and cannot hold a flow indicator, so those tags are escaped or written in
// full.
func TestTagsReadBackAsWritten(t *testing.T) {
	for _, tag := range []string{"!!", "![", "!a,b", "!{x}", "tag:yaml.org,2002:", "!local", "tag:example.com,2000:a b", "!!str", "tag:x,y:[z]"} {
		n := &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: "v", Style: yaml.TaggedStyle}
		out, err := yaml.Marshal(n)
		if err != nil {
			t.Fatalf("%q: %v", tag, err)
		}
		var back yaml.Node
		if err := yaml.Unmarshal(out, &back); err != nil {
			t.Errorf("tag %q written as %q does not read back: %v", tag, out, err)
			continue
		}
		got := back.Content[0]
		want := tag
		if want == "tag:yaml.org,2002:" {
			want = "!!" // the same tag, in the short form the reader reports
		}
		if got.Tag != want {
			t.Errorf("tag %q written as %q reads back as %q", tag, out, got.Tag)
		}
	}
}

// Comments next to a keep-chomped block scalar must not add lines to its text:
// every blank line after the text belongs to it.
func TestCommentsDoNotChangeKeepScalar(t *testing.T) {
	for _, in := range []string{"|+\n\n\n", "|+\r\r   ", "|+\n x\n\n"} {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(in), &n); err != nil {
			t.Fatal(err)
		}
		var want string
		if err := n.Decode(&want); err != nil {
			t.Fatal(err)
		}
		n.FootComment = "foot"
		n.Content[0].LineComment = "a\n\nb"
		n.Content[0].FootComment = "scalar foot"
		out, err := yaml.Marshal(&n)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		if err := yaml.Unmarshal(out, &got); err != nil || got != want {
			t.Errorf("%q: wrote %q, reads back %q (%v), want %q", in, out, got, err, want)
		}
	}
}

// Comments next to a keep-chomped folded or literal scalar must not add or cut
// lines of its text, wherever the comments are attached.
func TestCommentsAroundKeepFoldedScalar(t *testing.T) {
	for _, in := range []string{"\n: >+\n ", "0: >+\n\n: 0", "a: >+\n x\n\n\nb: 1\n"} {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(in), &n); err != nil {
			t.Fatal(err)
		}
		plain, err := yaml.Marshal(&n)
		if err != nil {
			t.Fatal(err)
		}
		var want interface{}
		if err := yaml.Unmarshal(plain, &want); err != nil {
			t.Fatal(err)
		}
		for _, c := range [][3]string{
			{"\nh1\n\nh2", " l\n\nl2", "\n\nf1\n\nf2"},
			{"\n \n", " ", "\n"},
			{" ", ">+", "\n: "},
			{"", "\n", ""},
		} {
			var all func(x *yaml.Node)
			all = func(x *yaml.Node) {
				x.HeadComment, x.LineComment, x.FootComment = c[0], c[1], c[2]
				for _, k := range x.Content {
					all(k)
				}
			}
			all(&n)
			out, err := yaml.Marshal(&n)
			if err != nil {
				continue
			}
			var got interface{}
			if err := yaml.Unmarshal(out, &got); err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("%q: wrote %q, reads back %v (%v), want %v", in, out, got, err, want)
			}
		}
	}
}

// A key with a long tag is written as an explicit key, as the reader allows an
// implicit key only so long.
func TestLongTagKeyIsExplicit(t *testing.T) {
	for _, in := range []string{"? !" + strings.Repeat("N", 1500) + "\n: 1\n", "? ? !" + strings.Repeat("P", 1200) + " x\n: 1\n"} {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(in), &n); err != nil {
			t.Fatal(err)
		}
		out, err := yaml.Marshal(&n)
		if err != nil {
			t.Fatal(err)
		}
		var back yaml.Node
		if err := yaml.Unmarshal(out, &back); err != nil {
			t.Errorf("does not read back: %v", err)
		}
	}
}

// A node with no text at the very end of an input that has no final line break
// sits at the start of the next line, where libyaml (which reads such a text as if
// it ended in a line break) puts it.
func TestEmptyNodeAtEndWithoutLineBreak(t *testing.T) {
	for _, c := range []struct {
		in        string
		line, col int
	}{
		{"a: 1\n?", 3, 1},
		{"? a", 2, 1},
		{"a: 1\n? #c", 3, 1},
		{"a: 1\n? ", 3, 1},
		{"a: 1\n?\n", 3, 1},
	} {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(c.in), &n); err != nil {
			t.Fatal(err)
		}
		m := n.Content[0]
		v := m.Content[len(m.Content)-1]
		if v.Line != c.line || v.Column != c.col {
			t.Errorf("%q: value at %d:%d, want %d:%d", c.in, v.Line, v.Column, c.line, c.col)
		}
	}
}
