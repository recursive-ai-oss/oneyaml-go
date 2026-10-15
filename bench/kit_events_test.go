package bench

// Helpers shared by the compat differentials: the kit's expected events for a
// case, and the same notation produced from a Document through the public API.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	yv3 "go.yaml.in/yaml/v3"

	oneyaml "oneyaml.dev/go"
)

// kitEvents returns the expected event lines of a kit case directory, for a case
// whose operation is "events" on the core profile and whose input is the file
// named.
func kitEvents(dir, name string) ([]string, bool) {
	if dir == "" {
		return nil, false
	}
	if _, err := os.Stat(filepath.Join(dir, "in.json")); err == nil {
		return nil, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, "case.json"))
	if err != nil {
		return nil, false
	}
	var c struct {
		Input     string `json:"input"`
		Operation struct {
			Type    string `json:"type"`
			Profile string `json:"profile"`
		} `json:"operation"`
		Expect struct {
			Kind string `json:"kind"`
			File string `json:"file"`
		} `json:"expect"`
	}
	if json.Unmarshal(raw, &c) != nil || c.Operation.Type != "events" || c.Expect.Kind != "events" ||
		(c.Operation.Profile != "" && c.Operation.Profile != "core") {
		return nil, false
	}
	in := c.Input
	if in == "" {
		in = "input.yaml"
	}
	if filepath.Base(name) != in {
		return nil, false
	}
	ev, err := os.ReadFile(filepath.Join(dir, c.Expect.File))
	if err != nil {
		return nil, false
	}
	return strings.Split(strings.TrimRight(string(ev), "\n"), "\n"), true
}

// eventNotation writes the stream of d in the test suite's event notation, with
// the public Document and Node API alone (the core package's tests do the same).
func eventNotation(d *oneyaml.Document) []string {
	lines := []string{"+STR"}
	for i := 0; i < d.Len(); i++ {
		v := d.Doc(i)
		l := "+DOC"
		if v.ExplicitStart() {
			l += " ---"
		}
		lines = append(lines, l)
		lines = appendEventNode(lines, v.Root())
		l = "-DOC"
		if v.ExplicitEnd() {
			l += " ..."
		}
		lines = append(lines, l)
	}
	return append(lines, "-STR")
}

func eventProps(n oneyaml.Node) string {
	s := ""
	if a := n.Anchor(); a != "" {
		s += " &" + a
	}
	if t, ok := n.ExplicitTag(); ok {
		s += " <" + t + ">"
	}
	return s
}

func appendEventNode(lines []string, n oneyaml.Node) []string {
	switch n.Kind() {
	case oneyaml.AliasNode:
		return append(lines, "=ALI *"+n.AliasName())
	case oneyaml.ScalarNode:
		mark := map[oneyaml.NodeStyle]string{oneyaml.PlainStyle: ":", oneyaml.SingleQuotedStyle: "'",
			oneyaml.DoubleQuotedStyle: "\"", oneyaml.LiteralStyle: "|", oneyaml.FoldedStyle: ">"}[n.Style()]
		return append(lines, "=VAL"+eventProps(n)+" "+mark+strings.NewReplacer("\\", `\\`, "\x00", `\0`, "\b", `\b`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(n.Value()))
	case oneyaml.SequenceNode:
		l := "+SEQ"
		if n.Style() == oneyaml.FlowStyle {
			l += " []"
		}
		lines = append(lines, l+eventProps(n))
		for item := range n.Items() {
			lines = appendEventNode(lines, item)
		}
		return append(lines, "-SEQ")
	case oneyaml.MappingNode:
		l := "+MAP"
		if n.Style() == oneyaml.FlowStyle {
			l += " {}"
		}
		lines = append(lines, l+eventProps(n))
		for k, v := range n.Entries() {
			lines = appendEventNode(lines, k)
			lines = appendEventNode(lines, v)
		}
		return append(lines, "-MAP")
	}
	return append(lines, "?invalid")
}

// ---- trees from event streams ----

// evNode is a node of the tree that an event stream describes. The differentials
// build the structure the kit expects from the expected events, and let the
// library under test read that structure, to see what it makes of the value when
// the text is not in its way.
type evNode struct {
	kind   byte // 'M' mapping, 'S' sequence, 'V' scalar, 'A' alias
	anchor string
	tag    string
	style  byte // scalar: ':' plain, '\'' '"' '|' '>'
	flow   bool
	text   string // scalar text, or the name of an alias
	kids   []*evNode
}

// parseEventTree reads the root of the first document from event lines.
func parseEventTree(lines []string) (*evNode, bool) {
	var root *evNode
	var stack []*evNode
	started := false
	add := func(n *evNode) bool {
		if len(stack) == 0 {
			if root != nil {
				return false
			}
			root = n
			return true
		}
		p := stack[len(stack)-1]
		p.kids = append(p.kids, n)
		return true
	}
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "+DOC"):
			started = true
		case strings.HasPrefix(l, "-DOC"):
			return root, root != nil && len(stack) == 0
		case !started:
		case strings.HasPrefix(l, "+MAP"), strings.HasPrefix(l, "+SEQ"):
			n := &evNode{kind: 'M'}
			if l[1] == 'S' {
				n.kind = 'S'
			}
			for _, f := range strings.Fields(l[4:]) {
				switch {
				case f == "{}" || f == "[]":
					n.flow = true
				case f[0] == '&':
					n.anchor = f[1:]
				case f[0] == '<':
					n.tag = strings.TrimSuffix(f[1:], ">")
				default:
					return nil, false
				}
			}
			if !add(n) {
				return nil, false
			}
			stack = append(stack, n)
		case l == "-MAP" || l == "-SEQ":
			if len(stack) == 0 {
				return nil, false
			}
			stack = stack[:len(stack)-1]
		case strings.HasPrefix(l, "=ALI *"):
			if !add(&evNode{kind: 'A', text: l[6:]}) {
				return nil, false
			}
		case strings.HasPrefix(l, "=VAL"):
			n := &evNode{kind: 'V'}
			s := l[4:]
			for {
				s = strings.TrimLeft(s, " ")
				if s != "" && s[0] == '&' {
					i := strings.IndexByte(s, ' ')
					if i < 0 {
						return nil, false
					}
					n.anchor, s = s[1:i], s[i:]
				} else if s != "" && s[0] == '<' {
					i := strings.IndexByte(s, '>')
					if i < 0 {
						return nil, false
					}
					n.tag, s = s[1:i], s[i+1:]
				} else {
					break
				}
			}
			if s == "" {
				return nil, false
			}
			n.style, n.text = s[0], unescapeEvent(s[1:])
			if !add(n) {
				return nil, false
			}
		}
	}
	return nil, false
}

func unescapeEvent(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case '0':
			b.WriteByte(0)
		case 'b':
			b.WriteByte('\b')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// shape lists the structure of a tree without styles, tags or anchors: the kind
// of each node, the text of each scalar and the name of each alias.
func (n *evNode) shape(out []string) []string {
	switch n.kind {
	case 'A':
		return append(out, "ALI "+n.text)
	case 'V':
		return append(out, "VAL "+n.text)
	}
	open := "+MAP"
	if n.kind == 'S' {
		open = "+SEQ"
	}
	out = append(out, open)
	for _, k := range n.kids {
		out = k.shape(out)
	}
	return append(out, "-"+open[1:])
}

// yamlv3Shape is shape for a tree that yaml.v3 read.
func yamlv3Shape(n *yv3.Node, out []string) []string {
	switch n.Kind {
	case yv3.DocumentNode:
		for _, c := range n.Content {
			out = yamlv3Shape(c, out)
		}
		return out
	case yv3.AliasNode:
		return append(out, "ALI "+n.Value)
	case yv3.ScalarNode:
		return append(out, "VAL "+n.Value)
	}
	open := "+MAP"
	if n.Kind == yv3.SequenceNode {
		open = "+SEQ"
	}
	out = append(out, open)
	for _, c := range n.Content {
		out = yamlv3Shape(c, out)
	}
	return append(out, "-"+open[1:])
}

// yamlv3Tree builds the yaml.v3 node tree of n, so that yaml.v3 can decode it.
func (n *evNode) yamlv3Tree(anchors map[string]*yv3.Node) *yv3.Node {
	out := &yv3.Node{Anchor: n.anchor}
	if n.tag != "" {
		out.Tag, out.Style = n.tag, yv3.TaggedStyle
	}
	switch n.kind {
	case 'A':
		out.Kind, out.Value, out.Alias = yv3.AliasNode, n.text, anchors[n.text]
		return out
	case 'V':
		out.Kind, out.Value = yv3.ScalarNode, n.text
		switch n.style {
		case '\'':
			out.Style |= yv3.SingleQuotedStyle
		case '"':
			out.Style |= yv3.DoubleQuotedStyle
		case '|':
			out.Style |= yv3.LiteralStyle
		case '>':
			out.Style |= yv3.FoldedStyle
		}
	case 'S':
		out.Kind = yv3.SequenceNode
	default:
		out.Kind = yv3.MappingNode
	}
	if n.flow {
		out.Style |= yv3.FlowStyle
	}
	if n.anchor != "" {
		anchors[n.anchor] = out
	}
	for _, k := range n.kids {
		out.Content = append(out.Content, k.yamlv3Tree(anchors))
	}
	return out
}

// flowYAML writes the tree as flow-style YAML text that reads as the same
// structure. A scalar that was not plain is double-quoted (a JSON string is a
// YAML double-quoted string); a plain one stays plain when its text cannot be
// mistaken for syntax, and is quoted otherwise, which only matters for texts that
// would resolve to a string anyway.
func (n *evNode) flowYAML(b *strings.Builder) bool {
	props := func() {
		if n.anchor != "" {
			b.WriteString("&" + n.anchor + " ")
		}
		if n.tag != "" {
			b.WriteString("!<" + n.tag + "> ")
		}
	}
	switch n.kind {
	case 'A':
		b.WriteString("*" + n.text)
		return true
	case 'V':
		props()
		switch {
		case n.style == ':' && n.text == "":
			b.WriteString("~")
		case n.style == ':' && plainSafe(n.text):
			b.WriteString(n.text)
		default:
			q, _ := json.Marshal(n.text)
			b.Write(q)
		}
		return true
	}
	props()
	open, shut := "[", "]"
	if n.kind == 'M' {
		open, shut = "{", "}"
		if len(n.kids)%2 != 0 {
			return false
		}
	}
	b.WriteString(open)
	for i, k := range n.kids {
		if i > 0 {
			if n.kind == 'M' && i%2 == 1 {
				b.WriteString(": ")
			} else {
				b.WriteString(", ")
			}
		}
		if n.kind == 'M' && i%2 == 0 && k.kind != 'V' && k.kind != 'A' {
			return false
		}
		if !k.flowYAML(b) {
			return false
		}
	}
	b.WriteString(shut)
	return true
}

func plainSafe(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}
	if strings.ContainsAny(s[:1], "-?:,[]{}#&*!|>'\"%@`") {
		return false
	}
	for _, r := range s {
		if r < ' ' || r == 0x7f || strings.ContainsRune(",[]{}#:", r) {
			return false
		}
	}
	return !strings.HasPrefix(s, "---") && !strings.HasPrefix(s, "...")
}
