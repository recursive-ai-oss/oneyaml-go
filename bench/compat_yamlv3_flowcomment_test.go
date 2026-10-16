package bench

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	yv3 "go.yaml.in/yaml/v3"
	compat "oneyaml.dev/go/compat/yamlv3"
)

// Comments inside flow collections: the shim writes them where yaml.v3 does.
// Where the shim's reader refuses the text yaml.v3 writes, or reads it as other values, the shim must still
// write text that reads back with the same values.

var flowCommentCases = []string{
	"[a,\n# c\nb]\n",
	"[a, # l\nb]\n",
	"[a,\nb # l\n]\n",
	"{a: 1,\n# h\nb: 2}\n",
	"{a: 1, # l\nb: 2}\n",
	"k: [a,\n  # h\n  [b,\n  # h2\n  c]]\n",
	"# top\n[a, # l\n b]\n# foot\n",
	"- [a,\n  # c\n  b]\n- x\n",
	"{a: [1,\n# c\n2], # x\nb: 3}\n",
	"k: {a: 1, # l\n  b: [2, # m\n   3]}\nz: 1\n",
	"[[a, # l\n b], # m\n [c]]\n",
}

func writeBoth(t *testing.T, in string) (yb, cb []byte, yn *yv3.Node, ok bool) {
	t.Helper()
	yn = &yv3.Node{}
	if yv3.Unmarshal([]byte(in), yn) != nil {
		return nil, nil, nil, false
	}
	yb, yerr := yv3.Marshal(yn)
	cb, cerr := compat.Marshal(toCompat(yn, map[*yv3.Node]*compat.Node{}))
	if yerr != nil || cerr != nil {
		t.Fatalf("%q: v3 err=%v me err=%v", in, yerr, cerr)
	}
	return yb, cb, yn, true
}

func TestCompatV3FlowCommentsWritten(t *testing.T) {
	for _, in := range flowCommentCases {
		yb, cb, _, ok := writeBoth(t, in)
		if !ok {
			t.Fatalf("%q does not parse", in)
		}
		if string(yb) != string(cb) {
			t.Errorf("%q\nyaml.v3:\n%s\nshim:\n%s", in, yb, cb)
		}
		// And what the shim wrote reads back to the tree it was given.
		var a yv3.Node
		var b compat.Node
		if err := compat.Unmarshal(cb, &b); err != nil {
			t.Errorf("%q: output does not parse: %v\n%s", in, err, cb)
			continue
		}
		_ = yv3.Unmarshal([]byte(in), &a)
		if x, y := dumpV3(&a, nodeFields{comments: true}), dumpCompat(&b, nodeFields{comments: true}); x != y {
			t.Errorf("%q: reads back differently\n%s\n%s", in, x, y)
		}
	}
}

// flowCommentInput builds a random flow collection with comments between its
// tokens, in a root, a mapping value or a sequence item.
func flowCommentInput(r *rand.Rand) string {
	seps := []string{"", " ", " ", "\n  ", "\n  ", " # c\n  ", "\n  # h\n  ", "\n  # h\n  # h2\n  ", " # l\n  # f\n  "}
	sep := func() string { return seps[r.Intn(len(seps))] }
	scalars := []string{"a", "1", "'q'", `"d"`, "x y", "true", "~"}
	var gen func(d int) string
	gen = func(d int) string {
		c := r.Intn(6)
		if d >= 3 && c >= 2 {
			c = 0
		}
		switch c {
		case 2, 3:
			var b strings.Builder
			b.WriteString("[" + sep())
			n := r.Intn(4)
			for i := 0; i < n; i++ {
				if i > 0 {
					b.WriteString("," + sep())
				}
				b.WriteString(gen(d + 1))
				b.WriteString(sep())
			}
			b.WriteString("]")
			return b.String()
		case 4, 5:
			var b strings.Builder
			b.WriteString("{" + sep())
			n := r.Intn(4)
			for i := 0; i < n; i++ {
				if i > 0 {
					b.WriteString("," + sep())
				}
				fmt.Fprintf(&b, "k%d:%s", i, sep())
				if b.Len() > 0 && !strings.HasSuffix(b.String(), " ") && !strings.HasSuffix(b.String(), "\n  ") {
					b.WriteString(" ")
				}
				b.WriteString(gen(d + 1))
				b.WriteString(sep())
			}
			b.WriteString("}")
			return b.String()
		}
		return scalars[r.Intn(len(scalars))]
	}
	body := gen(0)
	switch r.Intn(4) {
	case 0:
		return "k: " + body + "\nz: 1\n"
	case 1:
		return "- " + body + "\n- z\n"
	case 2:
		return "# top\n" + body + "\n# foot\n"
	}
	return body + "\n"
}

func TestCompatV3FlowCommentsGenerated(t *testing.T) {
	r := rand.New(rand.NewSource(79))
	n := 4000
	if testing.Short() {
		n = 600
	}
	same, unsound, parsed, comments, bad := 0, 0, 0, 0, 0
	var examples []string
	for i := 0; i < n; i++ {
		in := flowCommentInput(r)
		yb, cb, yn, ok := writeBoth(t, in)
		if !ok {
			continue
		}
		parsed++
		want := dumpV3(yn, nodeFields{comments: true})
		var back compat.Node
		yerr := compat.Unmarshal(yb, &back)
		sound := yerr == nil && dumpCompat(&back, nodeFields{}) == dumpV3(yn, nodeFields{})
		if string(yb) == string(cb) {
			same++
			continue
		}
		if sound {
			bad++
			if len(examples) < 6 {
				examples = append(examples, fmt.Sprintf("%q\nyaml.v3:\n%s\nshim:\n%s", in, yb, cb))
			}
			continue
		}
		// The reader refuses yaml.v3's text or reads it as other values: the shim must still write valid text
		// with the same values.
		unsound++
		var mine compat.Node
		if err := compat.Unmarshal(cb, &mine); err != nil {
			bad++
			if len(examples) < 6 {
				examples = append(examples, fmt.Sprintf("%q: shim output does not parse: %v\nyaml.v3:\n%s\nshim:\n%s", in, err, yb, cb))
			}
			continue
		}
		if dumpCompat(&mine, nodeFields{}) != dumpV3(yn, nodeFields{}) {
			bad++
			if len(examples) < 6 {
				examples = append(examples, fmt.Sprintf("%q: shim output reads back with other values\nyaml.v3:\n%s\nshim:\n%s", in, yb, cb))
			}
			continue
		}
		if dumpCompat(&mine, nodeFields{comments: true}) == want {
			comments++
		}
	}
	t.Logf("inputs=%d same=%d v3-unsound=%d (shim reads back whole=%d) unexplained=%d", parsed, same, unsound, comments, bad)
	for _, e := range examples {
		t.Errorf("%s", e)
	}
}

// randomFlowTree builds a tree with comments on arbitrary nodes inside flow
// collections (where the reader would never put them), flow collections inside
// block ones, and complex keys. Block collections carry no comments: their
// layout is not what this test is about. flow says whether the parent is a flow
// collection.
func randomFlowTree(r *rand.Rand, d int, flow bool) *yv3.Node {
	comment := func() string {
		switch r.Intn(8) {
		case 0:
			return "# c"
		case 1:
			return "# a\n# b"
		case 2:
			return "x"
		}
		return ""
	}
	n := &yv3.Node{}
	if flow {
		n.HeadComment, n.LineComment, n.FootComment = comment(), comment(), comment()
	}
	c := r.Intn(7)
	if d >= 3 {
		c = 0
	}
	switch c {
	case 1, 2, 3:
		n.Kind, n.Tag = yv3.SequenceNode, "!!seq"
		if flow || r.Intn(2) == 0 {
			n.Style = yv3.FlowStyle
		}
		for i, k := 0, r.Intn(4); i < k; i++ {
			n.Content = append(n.Content, randomFlowTree(r, d+1, n.Style&yv3.FlowStyle != 0))
		}
	case 4, 5:
		n.Kind, n.Tag = yv3.MappingNode, "!!map"
		if flow || r.Intn(2) == 0 {
			n.Style = yv3.FlowStyle
		}
		for i, k := 0, r.Intn(4); i < k; i++ {
			key := randomFlowTree(r, d+1, n.Style&yv3.FlowStyle != 0)
			if r.Intn(4) != 0 {
				key = &yv3.Node{Kind: yv3.ScalarNode, Tag: "!!str", Value: fmt.Sprintf("k%d", i), HeadComment: key.HeadComment, LineComment: key.LineComment, FootComment: key.FootComment}
			}
			n.Content = append(n.Content, key, randomFlowTree(r, d+1, n.Style&yv3.FlowStyle != 0))
		}
	default:
		n.Kind = yv3.ScalarNode
		n.Tag = "!!str"
		n.Value = []string{"a", "b c", "", "1"}[r.Intn(4)]
		if n.Value == "" || n.Value == "1" {
			n.Style = yv3.DoubleQuotedStyle // plain, they would read as null and a number
		}
	}
	if len(n.Content) == 0 && n.Kind != yv3.ScalarNode {
		n.Style = yv3.FlowStyle // an empty collection is always written in flow style
	}
	return n
}

// TestCompatV3FlowCommentsTrees writes random trees with comments anywhere. The
// shim matches yaml.v3's bytes, except where those do not read back as valid
// YAML with the same values: there the shim must write text that does.
func TestCompatV3FlowCommentsTrees(t *testing.T) {
	r := rand.New(rand.NewSource(7979))
	n := 6000
	if testing.Short() {
		n = 800
	}
	same, differ, bad := 0, 0, 0
	for i := 0; i < n; i++ {
		tree := randomFlowTree(r, 0, false)
		doc := &yv3.Node{Kind: yv3.DocumentNode, Content: []*yv3.Node{tree}}
		yb, yerr := yv3.Marshal(doc)
		cb, cerr := compat.Marshal(toCompat(doc, map[*yv3.Node]*compat.Node{}))
		if (yerr == nil) != (cerr == nil) {
			bad++
			t.Errorf("tree %d: v3 err=%v shim err=%v", i, yerr, cerr)
			continue
		}
		if yerr != nil {
			continue
		}
		if string(yb) == string(cb) {
			same++
			continue
		}
		differ++
		var vy, vm compat.Node
		if compat.Unmarshal(yb, &vy) == nil && dumpCompat(&vy, nodeFields{}) == dumpV3(doc, nodeFields{}) {
			// The reader takes yaml.v3's text as the tree it wrote: the shim must
			// match it.
			bad++
			if bad < 6 {
				t.Errorf("tree %d differs\nyaml.v3:\n%s\nshim:\n%s", i, yb, cb)
			}
			continue
		}
		if err := compat.Unmarshal(cb, &vm); err != nil {
			bad++
			if bad < 6 {
				t.Errorf("tree %d: shim text does not parse: %v\nyaml.v3:\n%s\nshim:\n%s", i, err, yb, cb)
			}
		} else if dumpCompat(&vm, nodeFields{}) != dumpV3(doc, nodeFields{}) {
			bad++
			if bad < 6 {
				t.Errorf("tree %d: shim text reads back with other values (%s)\nyaml.v3:\n%s\nshim:\n%s", i, firstLineDiff(dumpV3(doc, nodeFields{}), dumpCompat(&vm, nodeFields{})), yb, cb)
			}
		}
	}
	t.Logf("trees=%d same=%d yaml.v3-invalid-and-different=%d unexplained=%d", n, same, differ, bad)
}
