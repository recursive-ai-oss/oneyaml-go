package yaml

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"

	"oneyaml.dev/go/internal/bind"
	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/emit"
	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/scan"
)

var (
	nodeType            = reflect.TypeFor[Node]()
	unmarshalerType     = reflect.TypeFor[Unmarshaler]()
	obsoleteUnmarshType = reflect.TypeFor[obsoleteUnmarshaler]()
)

func init() {
	emit.SetV3Node(
		func(t reflect.Type) bool { return t == nodeType || t == reflect.PointerTo(nodeType) },
		func(v reflect.Value, step int, compact bool) ([]byte, error) {
			n, ok := nodeOf(v.Interface())
			if !ok {
				return []byte("null\n"), nil
			}
			return renderNode(n, step, compact)
		})
	bind.SetV3Hook(
		func(pt reflect.Type) bool {
			return pt.Implements(unmarshalerType) || pt.Implements(obsoleteUnmarshType) || pt == reflect.PointerTo(nodeType) || foreignHook(pt)
		},
		func(t reflect.Type) bool { return t == nodeType },
		callHook,
	)
}

// callHook hands the node of a value to the type that reads its own node: a
// *Node takes it whole, an Unmarshaler is passed it, and the older function
// style gets a function that decodes it into whatever it is given.
func callHook(inst any, ref bind.NodeRef) error {
	switch x := inst.(type) {
	case *Node:
		n, err := buildNode(ref, ref.Root, nil)
		if err != nil {
			return err
		}
		*x = *n
		return nil
	case Unmarshaler:
		// Every node handed to the method remembers the call it belongs to, so a
		// Node.Decode inside the method is charged to that call's alias budget.
		n, err := buildNode(ref, false, &hookCall{cfg: ref.Cfg, scope: ref.Scope})
		if err != nil {
			return err
		}
		return softened(x.UnmarshalYAML(n))
	case obsoleteUnmarshaler:
		n, err := buildNode(ref, false, nil)
		if err != nil {
			return err
		}
		return softened(x.UnmarshalYAML(func(v interface{}) error {
			cfg := ref.Cfg
			if ref.Scope.Spend() == nil {
				// The method kept this function past its call: a later decode
				// starts its own budget.
				cfg.Spend = nil
			}
			return n.decode(v, cfg)
		}))
	}
	return foreignHookError(inst)
}

// foreignHook reports whether pt has an UnmarshalYAML method that the adapter
// cannot call: typically one that takes the Node of another YAML package, such
// as gopkg.in/yaml.v3, which is a different type from this package's Node. The
// answer is cached with the type's decoding plan, so the lookup runs once.
func foreignHook(pt reflect.Type) bool {
	_, ok := pt.MethodByName("UnmarshalYAML")
	return ok
}

// foreignHookError names the type and the method that cannot be called. yaml.v3
// itself skips a method it does not recognise and fills the value by its
// fields; the adapter refuses instead, because the skipped method usually
// validates or converts the value and a silent skip would drop that.
func foreignHookError(inst any) error {
	t := reflect.TypeOf(inst)
	m, ok := t.MethodByName("UnmarshalYAML")
	if !ok {
		return errors.New("yaml: unexpected unmarshaler")
	}
	name := t.Elem().String()
	var ins []string
	for i := 1; i < m.Type.NumIn(); i++ {
		ins = append(ins, m.Type.In(i).String())
	}
	sig := "UnmarshalYAML(" + strings.Join(ins, ", ") + ")"
	why := "the signature is neither UnmarshalYAML(*yaml.Node) error nor UnmarshalYAML(func(interface{}) error) error with this package's Node"
	if m.Type.NumIn() == 2 {
		if p := m.Type.In(1); p.Kind() == reflect.Pointer && p.Elem().Name() == "Node" && p.Elem().PkgPath() != nodeType.PkgPath() {
			why = "the parameter is " + p.Elem().PkgPath() + ".Node, a different type from this package's Node"
		}
	}
	return fmt.Errorf("yaml: cannot decode into %s: its method %s is not one this package can call (%s); the adapter can't call a hook written for another YAML package, and does not fill the value field by field behind its back (yaml.v3 would skip the method silently). Give the type a hook with this package's signature, or read it through a wrapper type of your own", name, sig, why)
}

// softened marks a *TypeError from a user's hook so the decoder collects its
// messages instead of stopping.
func softened(err error) error {
	if te, ok := err.(*TypeError); ok {
		return softTypeError{te}
	}
	return err
}

// lineCursor maps byte offsets to line and column by walking the text. Offsets
// are mostly asked for in increasing order, so it keeps its place instead of an
// index of every line start, which is what costs memory on a large document;
// asking for an earlier offset starts the walk again from the top.
type lineCursor struct {
	src            []byte
	off, line, col int
	// marks remember the line and column at every markStride-th offset the cursor
	// has passed, once a question about an earlier offset has shown they are
	// needed: the next such question then walks from the nearest mark, not from
	// the top, so a hook called many times for one document stays linear.
	marks *markSet
}

type markSet struct{ m []cursorMark }

type cursorMark struct {
	ok             bool
	off, line, col int
}

const markStride = 4096

// stepCount, when set (by tests), receives the number of bytes the cursors walk
// and the bytes searched for comments, which makes cost visible without timing.
var stepCount *atomic.Int64

func newLineCursor(src []byte) *lineCursor {
	c := &lineCursor{src: src}
	c.reset()
	return c
}

func (c *lineCursor) reset() {
	c.off, c.line, c.col = 0, 1, 1
	if len(c.src) >= 3 && c.src[0] == 0xEF && c.src[1] == 0xBB && c.src[2] == 0xBF {
		// A byte order mark is not a column.
		c.off = 3
	}
}

// pos returns the 1-based line and column (in characters) of offset off.
func (c *lineCursor) pos(off int) (line, col int) {
	if off > len(c.src) {
		off = len(c.src)
	}
	if off < c.off {
		c.rewind(off)
	}
	if stepCount != nil {
		stepCount.Add(int64(off - c.off))
	}
	for c.off < off {
		if c.marks != nil && c.off%markStride == 0 {
			c.mark()
		}
		b := c.src[c.off]
		c.off++
		switch {
		case b == '\n':
			c.line++
			c.col = 1
		case b == '\r':
			if c.off < len(c.src) && c.src[c.off] == '\n' && c.off < off {
				c.off++
			}
			c.line++
			c.col = 1
		case b&0xC0 != 0x80:
			c.col++
		}
	}
	return c.line, c.col
}

// rewind moves the cursor to a place at or before off.
func (c *lineCursor) rewind(off int) {
	if c.marks == nil {
		// A single build asks for earlier places rarely and in a short text: start
		// again from the top.
		c.reset()
		return
	}
	for k := min(off/markStride, len(c.marks.m)-1); k >= 0; k-- {
		if m := c.marks.m[k]; m.ok && m.off <= off {
			c.off, c.line, c.col = m.off, m.line, m.col
			return
		}
	}
	c.reset()
}

// mark records the state at the current offset, a multiple of markStride.
func (c *lineCursor) mark() {
	k := c.off / markStride
	for len(c.marks.m) <= k {
		c.marks.m = append(c.marks.m, cursorMark{})
	}
	c.marks.m[k] = cursorMark{true, c.off, c.line, c.col}
}

// buildShared is what the builders of one document share: the cursors that map
// offsets to positions and the facts about the text that every call would
// otherwise find out again, which is what made a type that reads its own node
// cost time in proportion to the size of the whole document for each call.
type buildShared struct {
	li        lineCursor
	ahead     *lineCursor
	hashKnown bool
	hasHash   bool
	// spans are the multi-line scalars seen so far, in text order unless a call
	// went back to an earlier part of the document (unsorted).
	unsorted bool
	spans    [][2]int32
}

func sharedFor(ref bind.NodeRef) *buildShared {
	if ref.Cache != nil {
		if sh, ok := (*ref.Cache).(*buildShared); ok && len(sh.li.src) == len(ref.Src) && (len(ref.Src) == 0 || &sh.li.src[0] == &ref.Src[0]) {
			return sh
		}
	}
	sh := &buildShared{li: lineCursor{src: ref.Src}}
	sh.li.reset()
	if !ref.Root {
		// A type that reads its own node is called again and again for one text, so
		// its cursors keep marks to come back to.
		sh.li.marks = &markSet{}
	}
	if ref.Cache != nil {
		*ref.Cache = sh
	}
	return sh
}

type builder struct {
	ref  bind.NodeRef
	sh   *buildShared
	li   *lineCursor
	slab []Node
	// hook, when set, is recorded on every node built.
	hook *hookCall
	// kids is one allocation for the Content of every collection: each node but
	// the root is the child of exactly one collection.
	kids    []*Node
	scratch []byte
	anchors map[string]*Node
	cs      commentState
}

// buildNode converts the subtree a hook was given into Nodes. With doc set the
// result is a document node holding the root.
func buildNode(ref bind.NodeRef, doc bool, hook *hookCall) (*Node, error) {
	sh := sharedFor(ref)
	b := &builder{ref: ref, sh: sh, li: &sh.li, hook: hook}
	b.initComments()
	if err := b.predefine(); err != nil {
		return nil, err
	}
	b.slab = make([]Node, 0, len(ref.Tree.Nodes)-1)
	b.kids = make([]*Node, 0, len(ref.Tree.Nodes)-1)
	root, err := b.build(ref.Tree, ref.Node, ref.Tags)
	if err != nil {
		return nil, err
	}
	if !doc {
		return root, nil
	}
	d := &Node{Kind: DocumentNode, Content: []*Node{root}}
	d.Line, d.Column = b.li.pos(directivesStart(ref.Src, ref.DocStart))
	b.documentComments(d)
	return d, nil
}

// predefine builds the anchored nodes outside the subtree that its aliases point
// to, so the aliases can refer to them.
func (b *builder) predefine() error {
	for _, evs := range b.ref.Predefs {
		var tree cst.Tree
		var cb cst.Builder
		cb.Reset(&tree, b.ref.Src)
		cb.Event(&parse.Event{Kind: parse.DocumentStart})
		for i := range evs {
			e := evs[i]
			cb.Event(&e)
		}
		cb.Event(&parse.Event{Kind: parse.DocumentEnd})
		if len(tree.Docs) == 0 || tree.Docs[0].Root == 0 {
			continue
		}
		if _, err := b.build(&tree, tree.Docs[0].Root, nil); err != nil {
			return err
		}
	}
	return nil
}

// aheadCursor returns the cursor that places empty nodes; most texts have none.
func (b *builder) aheadCursor() *lineCursor {
	sh := b.sh
	if sh.ahead == nil {
		sh.ahead = newLineCursor(sh.li.src)
		if sh.li.marks != nil {
			sh.ahead.marks = &markSet{}
		}
	}
	return sh.ahead
}

func (b *builder) pos(t *cst.Tree, c int32) (int, int) {
	return b.li.pos(int(t.FullStart(c)))
}

type buildFrame struct {
	c      int32
	parent *Node
	// prev is the preceding sibling in a mapping, to place an empty value.
	prev int32
}

// build converts the tree below c, in document order so that an alias finds the
// node it names. Resolved tags are tags[Prop] when tags is set, else the tag as
// written.
func (b *builder) build(t *cst.Tree, c int32, tags []string) (*Node, error) {
	var root *Node
	// The first levels of nesting fit in this array, which stays on the stack.
	var frames [24]buildFrame
	stack := append(frames[:0], buildFrame{c: c})
	var kidsArr [32]int32
	kids := kidsArr[:0]
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n, err := b.one(t, f.c, tags)
		if err != nil {
			return nil, err
		}
		if f.prev != 0 {
			b.placeEmpty(t, f.c, f.prev, n)
		}
		r := roleRoot
		if f.parent == nil {
			root = n
		} else {
			switch {
			case f.parent.Kind == SequenceNode:
				r = roleItem
			case len(f.parent.Content)%2 == 0:
				r = roleKey
			default:
				r = roleValue
			}
			f.parent.Content = append(f.parent.Content, n)
		}
		if t == ref0(b) {
			b.commentsFor(t, f.c, n, r)
		}
		nd := &t.Nodes[f.c]
		if nd.Kind == cst.Mapping || nd.Kind == cst.Sequence {
			kids = t.Children(kids[:0], f.c)
			for i := len(kids) - 1; i >= 0; i-- {
				fr := buildFrame{c: kids[i], parent: n}
				if nd.Kind == cst.Mapping && i%2 == 1 {
					fr.prev = kids[i-1]
				}
				stack = append(stack, fr)
			}
		}
	}
	return root, nil
}

// one converts a single tree node, leaving its children to the caller.
func (b *builder) one(t *cst.Tree, c int32, tags []string) (*Node, error) {
	nd := &t.Nodes[c]
	var n *Node
	if len(b.slab) < cap(b.slab) {
		b.slab = b.slab[:len(b.slab)+1]
		n = &b.slab[len(b.slab)-1]
	} else {
		n = &Node{}
	}
	n.hook = b.hook
	n.Line, n.Column = b.pos(t, c)
	var tag string
	if p := t.PropOf(c); p != nil {
		if p.HasAnchor() {
			n.Anchor = string(b.ref.Src[p.AnchorStart:p.AnchorEnd])
		}
		if p.HasTag() {
			if tags != nil && int(nd.Prop) < len(tags) && tags[nd.Prop] != "" {
				tag = tags[nd.Prop]
			} else {
				tag = string(b.ref.Src[p.TagStart:p.TagEnd])
			}
		}
	}
	var defaultTag string
	switch nd.Kind {
	case cst.Alias:
		n.Kind = AliasNode
		n.Value = string(b.ref.Src[nd.Start+1 : nd.End])
		n.Alias = b.anchors[n.Value]
		if n.Alias == nil {
			return nil, fmt.Errorf("yaml: unknown anchor '%s' referenced", n.Value)
		}
		return n, nil
	case cst.Scalar:
		n.Kind = ScalarNode
		if raw := b.ref.Src[nd.Start:nd.End]; nd.Style == scan.Plain && bytes.IndexByte(raw, '\n') < 0 && bytes.IndexByte(raw, '\r') < 0 {
			n.Value = string(raw)
		} else {
			ev := parse.Event{Kind: parse.Scalar, Style: nd.Style, Start: int(nd.Start), End: int(nd.End), Indent: int(nd.Indent)}
			out, err := parse.AppendScalar(b.scratch[:0], b.ref.Src, &ev)
			if err != nil {
				return nil, err
			}
			b.scratch = out
			n.Value = string(out)
		}
		switch nd.Style {
		case scan.SingleQuoted:
			n.Style = SingleQuotedStyle
		case scan.DoubleQuoted:
			n.Style = DoubleQuotedStyle
		case scan.Literal:
			n.Style = LiteralStyle
		case scan.Folded:
			n.Style = FoldedStyle
		}
		if n.Style == 0 {
			if n.Value == "<<" {
				defaultTag = mergeTag
			}
		} else {
			defaultTag = strTag
		}
	case cst.Sequence:
		n.Kind, defaultTag = SequenceNode, seqTag
		n.Content = b.content(t.Count(c))
		if nd.Flow || nd.Pair {
			n.Style |= FlowStyle
		}
	case cst.Mapping:
		n.Kind, defaultTag = MappingNode, mapTag
		n.Content = b.content(t.Count(c))
		if nd.Flow || nd.Pair {
			n.Style |= FlowStyle
		}
	}
	switch {
	case tag != "" && tag != "!":
		n.Tag = shortTag(tag)
		n.Style |= TaggedStyle
	case defaultTag != "":
		n.Tag = defaultTag
	case n.Kind == ScalarNode:
		n.Tag = resolveTag(n.Value)
	}
	if n.Anchor != "" {
		if b.anchors == nil {
			b.anchors = map[string]*Node{}
		}
		b.anchors[n.Anchor] = n
	}
	return n, nil
}

// content returns an empty Content slice with room for n nodes, cut from the
// shared block when it has room. The capacity is clipped, so a caller that
// appends to the slice later copies it instead of writing into a neighbour.
func (b *builder) content(n int) []*Node {
	if n == 0 {
		return []*Node{}
	}
	if len(b.kids)+n <= cap(b.kids) {
		l := len(b.kids)
		b.kids = b.kids[:l+n]
		return b.kids[l : l : l+n]
	}
	return make([]*Node, 0, n)
}

// directivesStart returns where a document that starts at off really begins: at
// its first directive, if it has any. The parser reports the "---" marker.
func directivesStart(src []byte, off int) int {
	first := off
	end := off
	for end > 0 {
		// The line before the one that starts at end.
		start := end - 1
		for start > 0 && src[start-1] != '\n' {
			start--
		}
		line := src[start : end-1]
		switch {
		case len(line) > 0 && line[0] == '%':
			first = start
		case len(bytes.TrimSpace(line)) == 0 || bytes.HasPrefix(bytes.TrimLeft(line, " \t"), []byte("#")):
		default:
			return first
		}
		end = start
	}
	return first
}

// placeEmpty gives an empty mapping value the position libyaml gives it. After a
// colon that is the colon's end, which the tree already says. A key with no
// colon at all ("? key") gets the start of whatever token comes next.
func (b *builder) placeEmpty(t *cst.Tree, c, key int32, n *Node) {
	nd := &t.Nodes[c]
	if nd.Kind != cst.Scalar || nd.Start != nd.End || nd.Prop != 0 {
		return
	}
	i := cst.SkipTrivia(b.ref.Src, int(t.Nodes[key].End))
	if i < len(b.ref.Src) && b.ref.Src[i] == ':' {
		if !t.Nodes[t.Nodes[c].Parent].Flow {
			return
		}
		// In a flow mapping an empty value sits at the next token.
		i = cst.SkipTrivia(b.ref.Src, i+1)
	}
	n.Line, n.Column = b.aheadCursor().pos(i)
	if src := b.ref.Src; i >= len(src) && len(src) > 0 && src[len(src)-1] != '\n' && src[len(src)-1] != '\r' {
		// libyaml reads a text that does not end in a line break as if it did, so
		// a token at the very end sits at the start of the following line.
		n.Line, n.Column = n.Line+1, 1
	}
}

// ref0 returns the main tree of the builder (the predefined trees are built
// without comments).
func ref0(b *builder) *cst.Tree { return b.ref.Tree }
