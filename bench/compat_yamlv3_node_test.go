package bench

import (
	"fmt"
	"strings"
	"testing"

	yv3 "go.yaml.in/yaml/v3"
	compat "oneyaml.dev/go/compat/yamlv3"
)

// This file compares the Node trees of yaml.v3 and the shim.

// nodeDump renders the fields of a yaml.v3 node, one line per node, with the
// parts selected by flags. Aliases print the line of the node they point to.
type nodeFields struct {
	pos, comments bool
}

func dumpV3(n *yv3.Node, f nodeFields) string {
	var b strings.Builder
	var walk func(n *yv3.Node, depth int)
	walk = func(n *yv3.Node, depth int) {
		if depth > 200 {
			return
		}
		fmt.Fprintf(&b, "%s%d/%d %q %q anchor=%q", strings.Repeat(" ", depth), n.Kind, n.Style, n.Tag, n.Value, n.Anchor)
		if n.Alias != nil {
			fmt.Fprintf(&b, " alias=%d:%d", n.Alias.Line, n.Alias.Column)
		}
		if f.pos {
			fmt.Fprintf(&b, " @%d:%d", n.Line, n.Column)
		}
		if f.comments {
			fmt.Fprintf(&b, " H=%q L=%q F=%q", n.HeadComment, n.LineComment, n.FootComment)
		}
		b.WriteByte('\n')
		for _, c := range n.Content {
			walk(c, depth+1)
		}
	}
	walk(n, 0)
	return b.String()
}

func dumpCompat(n *compat.Node, f nodeFields) string {
	var b strings.Builder
	var walk func(n *compat.Node, depth int)
	walk = func(n *compat.Node, depth int) {
		if depth > 200 {
			return
		}
		fmt.Fprintf(&b, "%s%d/%d %q %q anchor=%q", strings.Repeat(" ", depth), n.Kind, n.Style, n.Tag, n.Value, n.Anchor)
		if n.Alias != nil {
			fmt.Fprintf(&b, " alias=%d:%d", n.Alias.Line, n.Alias.Column)
		}
		if f.pos {
			fmt.Fprintf(&b, " @%d:%d", n.Line, n.Column)
		}
		if f.comments {
			fmt.Fprintf(&b, " H=%q L=%q F=%q", n.HeadComment, n.LineComment, n.FootComment)
		}
		b.WriteByte('\n')
		for _, c := range n.Content {
			walk(c, depth+1)
		}
	}
	walk(n, 0)
	return b.String()
}

func firstLineDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) && i < len(bl); i++ {
		if al[i] != bl[i] {
			return fmt.Sprintf("line %d:\n    v3: %s\n    me: %s", i+1, al[i], bl[i])
		}
	}
	return fmt.Sprintf("length %d vs %d lines", len(al), len(bl))
}

// nodeDifference explains a Node difference by the difference in the plain
// values of the same input, which the value tests list.
func nodeDifference(it corpusItem, yerr, cerr error) string {
	if r := readDifference(it, nil, nil, yerr, cerr); r != "" && (yerr != nil || cerr != nil) {
		return r
	}
	var ya, ca any
	yerr2 := yv3.Unmarshal(it.data, &ya)
	cerr2 := compat.Unmarshal(it.data, &ca)
	if r := readDifference(it, &ya, &ca, yerr2, cerr2); r != "" {
		return r
	}
	return nodeExceptions[strings.TrimPrefix(it.name, "suites/")]
}

// nodeExceptions are inputs where the trees differ although the values do not.
var nodeExceptions = map[string]string{
	"yaml-test-suite/data-2022-01-17/PW8X/in.yaml":                            "position of an empty node that has an anchor",
	"own/cases/schema/empty-key-is-null/input.yaml":                           "position of an empty mapping key",
	"own/cases/parse/tag-suffix-utf-8-percent-escape-is-kept/input.yaml":      "the shim keeps a non-ASCII percent escape in a tag as written; yaml.v3 decodes it",
	"own/cases/parse/top-level-literal-with-indentation-indicator/input.yaml": "yaml.v3 drops the space that belongs to a top-level block scalar with an indentation indicator (the kit expects it)",
}

func testNodeFields(t *testing.T, name string, f nodeFields) {
	tally := newTally(name)
	for _, it := range compatCorpus(t) {
		var yn yv3.Node
		var cn compat.Node
		yerr := yv3.Unmarshal(it.data, &yn)
		cerr := compat.Unmarshal(it.data, &cn)
		switch {
		case yerr != nil && cerr != nil:
			tally.bothErr++
		case yerr == nil && cerr == nil:
			a, b := dumpV3(&yn, f), dumpCompat(&cn, f)
			if a == b {
				tally.same++
			} else if r := nodeDifference(it, nil, nil); r != "" {
				tally.note(r, it.name)
			} else if f.comments && commentReadGaps[gapName(it.name)] {
				tally.note("GAP: comment placement", it.name)
			} else {
				tally.unexplained = append(tally.unexplained, fmt.Sprintf("%s: %s", it.name, firstLineDiff(a, b)))
			}
		default:
			if r := nodeDifference(it, yerr, cerr); r != "" {
				tally.note(r, it.name)
			} else {
				tally.unexplained = append(tally.unexplained, fmt.Sprintf("%s: v3 err=%v me err=%v", it.name, yerr, cerr))
			}
		}
	}
	tally.report(t)
}

func TestCompatV3NodeStructure(t *testing.T) { testNodeFields(t, "node-structure", nodeFields{}) }

func TestCompatV3NodePositions(t *testing.T) {
	testNodeFields(t, "node-positions", nodeFields{pos: true})
}

func TestCompatV3NodeComments(t *testing.T) {
	testNodeFields(t, "node-comments", nodeFields{comments: true})
}

// toCompat copies a yaml.v3 tree into the shim's Node type, keeping aliases
// pointing at the copies.
func toCompat(n *yv3.Node, seen map[*yv3.Node]*compat.Node) *compat.Node {
	if c, ok := seen[n]; ok {
		return c
	}
	c := &compat.Node{Kind: compat.Kind(n.Kind), Style: compat.Style(n.Style), Tag: n.Tag, Value: n.Value, Anchor: n.Anchor,
		HeadComment: n.HeadComment, LineComment: n.LineComment, FootComment: n.FootComment, Line: n.Line, Column: n.Column}
	seen[n] = c
	if n.Alias != nil {
		c.Alias = toCompat(n.Alias, seen)
	}
	for _, ch := range n.Content {
		c.Content = append(c.Content, toCompat(ch, seen))
	}
	return c
}

// TestCompatV3NodeWrite writes the trees yaml.v3 read from the corpus with both
// libraries.
func TestCompatV3NodeWrite(t *testing.T) {
	tally := newTally("node-write")
	for _, it := range compatCorpus(t) {
		var yn yv3.Node
		if yv3.Unmarshal(it.data, &yn) != nil {
			continue
		}
		cn := toCompat(&yn, map[*yv3.Node]*compat.Node{})
		yb, yerr := yv3.Marshal(&yn)
		cb, cerr := compat.Marshal(cn)
		switch {
		case yerr != nil && cerr != nil:
			tally.bothErr++
		case yerr == nil && cerr == nil && string(yb) == string(cb):
			tally.same++
		default:
			if r := writeDifference(&yn, yb, cb, yerr, cerr); r != "" {
				tally.note(r, it.name)
			} else if commentWriteGaps[gapName(it.name)] {
				tally.note("GAP: comment layout", it.name)
			} else if commentInFlow(&yn, false) {
				tally.note("GAP: comments inside flow collections are not written", it.name)
			} else {
				tally.unexplained = append(tally.unexplained, fmt.Sprintf("%s: v3 err=%v me err=%v\n%s%s", it.name, yerr, cerr, firstDiff(yb, cb), fullIf(yb, cb)))
			}
		}
	}
	tally.report(t)
}

// commentInFlow reports whether any node inside a flow collection has a comment.
func commentInFlow(n *yv3.Node, inFlow bool) bool {
	inFlow = inFlow || n.Style&yv3.FlowStyle != 0
	if inFlow && (n.HeadComment != "" || n.LineComment != "" || n.FootComment != "") {
		return true
	}
	for _, c := range n.Content {
		if commentInFlow(c, inFlow) {
			return true
		}
	}
	return false
}

// Known gaps: inputs where the shim places a comment on a different node than
// yaml.v3 does (reading), or lays one out differently (writing). Each is a real
// difference to close, not an intended one, so the list is a ratchet: a new input
// that differs fails the test, and so does a name here that has started to match.
var commentReadGaps = map[string]bool{
	"2XXW/in.yaml": true,
	"5TYM/in.yaml": true,
	"5WE3/in.yaml": true,
	"735Y/in.yaml": true,
	"7BMT/in.yaml": true,
	"7TMG/in.yaml": true,
	"C4HZ/in.yaml": true,
	"J7PZ/in.yaml": true,
	"JHB9/in.yaml": true,
	"P94K/in.yaml": true,
	"RZP5/in.yaml": true,
	"U3XV/in.yaml": true,
	"W42U/in.yaml": true,
	"X8DW/in.yaml": true,
	"XW4D/in.yaml": true,
	// A comment after "?" with no key text: yaml.v3 puts it on the mapping.
	"edit/set-value-of-explicit-null-key-at-end-of-large-mapping/input.yaml": true,
	"parse/encoding-utf-16be-with-bom/input.yaml":                            true,
	"parse/encoding-utf-16le-with-bom/input.yaml":                            true,
	"parse/encoding-utf-8-with-bom/input.yaml":                               true,
	"parse/encoding-utf-8-without-bom/input.yaml":                            true,
	"roundtrip/multiple-documents-with-markers/input.yaml":                   true,
	"helm-ingress-nginx":                                                     true,
	"prometheus-config":                                                      true,
}

var commentWriteGaps = map[string]bool{
	"7T8X/in.yaml":       true,
	"P94K/in.yaml":       true,
	"Q9WF/in.yaml":       true,
	"RZP5/in.yaml":       true,
	"X8DW/in.yaml":       true,
	"XW4D/in.yaml":       true,
	"helm-ingress-nginx": true,
}

func gapName(name string) string {
	name = strings.TrimPrefix(name, "suites/yaml-test-suite/data-2022-01-17/")
	return strings.TrimPrefix(name, "suites/own/cases/")
}
