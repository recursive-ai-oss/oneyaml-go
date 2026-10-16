package oneyaml

import (
	"iter"
	"oneyaml.dev/go/internal/bind"
	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/pointer"
	"oneyaml.dev/go/internal/resolve"
	"oneyaml.dev/go/internal/scan"
	"strconv"
)

// NodeKind is the kind of a Node.
type NodeKind uint8

// Node kinds. The zero value marks an invalid Node.
const (
	InvalidNode NodeKind = iota
	ScalarNode
	MappingNode
	SequenceNode
	AliasNode
)

// String returns the kind name: "scalar", "mapping", "sequence", "alias" or "invalid".
func (k NodeKind) String() string {
	switch k {
	case ScalarNode:
		return "scalar"
	case MappingNode:
		return "mapping"
	case SequenceNode:
		return "sequence"
	case AliasNode:
		return "alias"
	}
	return "invalid"
}

// NodeStyle is how a node is written in the source.
type NodeStyle uint8

// Node styles. Plain, SingleQuoted, DoubleQuoted, Literal and Folded describe
// scalars; BlockStyle and FlowStyle describe collections.
const (
	PlainStyle NodeStyle = iota
	SingleQuotedStyle
	DoubleQuotedStyle
	LiteralStyle
	FoldedStyle
	BlockStyle
	FlowStyle
)

// A Node is a read-only view of one node of a Document. It is a small value and
// is cheap to copy. A Node becomes stale when the Document is edited: Stale then
// reports true, methods that return an error report ErrStaleNode, and the others
// return zero values ("", 0, false, an invalid Node). Zero values are easy to
// write back by mistake, so to edit while reading, collect the pointers first
// (see Pointer) and edit afterwards, or read the nodes again after each edit.
type Node struct {
	s   *docStream
	i   int32
	doc int32
	gen uint32
	// sc is set on a Node handed to an Unmarshaler: a decode made from it spends
	// the alias budget of the call that made it, for as long as the call runs.
	sc *bind.Scope
}

func (d *Document) node(i int32) Node {
	return Node{s: d.s, i: i, doc: int32(d.idx), gen: d.s.epoch()}
}

func (n Node) nd() *cst.Node {
	if n.s == nil || (n.gen != n.s.gen && n.gen != n.s.epoch()) || n.i <= 0 || int(n.i) >= len(n.s.tree.Nodes) {
		return nil
	}
	return &n.s.tree.Nodes[n.i]
}

func (n Node) child(i int32) Node { return Node{s: n.s, i: i, doc: n.doc, gen: n.gen, sc: n.sc} }

func (n Node) ctx() *edit.Ctx {
	c := edit.NewCtx(&n.s.tree, n.s.src, int(n.doc))
	n.s.attach(c)
	return c
}

// IsValid reports whether n refers to a node of its document. It is false for the
// zero Node and for a stale Node.
func (n Node) IsValid() bool { return n.nd() != nil }

// Stale reports whether n came from a Document that has been edited since n was
// read. A stale Node is not valid: its methods return zero values (or
// ErrStaleNode where they return an error). The zero Node is not stale, only
// invalid.
func (n Node) Stale() bool {
	return n.s != nil && n.gen != n.s.gen && n.gen != n.s.epoch()
}

// errInvalid is the error for a Node that is not usable: ErrStaleNode when the
// document was edited since the Node was read, ErrNotFound for the zero Node.
func (n Node) errInvalid(op string) error {
	if n.Stale() {
		return &EditError{Op: op, Kind: ErrStaleNode}
	}
	return &EditError{Op: op, Kind: ErrNotFound}
}

// Pointer returns the RFC 6901 pointer of the node within its own document,
// ready for Document.Set, Get or Remove. It is how to edit while walking: collect
// the pointers of the nodes you want, finish the loop, then edit (an edit makes
// every Node read earlier stale). A pointer is an address: removing or inserting
// a sequence item changes what the later indices address, and a rename or a
// removal changes what the pointers below it address. To remove several items
// by collected pointers, remove them from the end of the sequence first.
//
// Pointer returns an error matching ErrStaleNode when the Document has been
// edited since the node was read, and one matching ErrNotFound for the zero Node.
// It returns an error matching ErrNoPointer, and never a guess, when no pointer
// addresses the node: a mapping key (simple or complex), anything inside a
// complex key, or a node whose pointer would lead to a different node (the key
// occurs twice in its mapping). An alias node has the pointer of the alias
// itself; Target gives the node it refers to, whose pointer is the place of the
// anchor, not a path through the alias.
func (n Node) Pointer() (string, error) {
	toks, err := n.pointerToks()
	if err != nil {
		return "", err
	}
	return pointer.Join(toks), nil
}

// pointerToks is Pointer as reference tokens.
func (n Node) pointerToks() ([]string, error) {
	toks, _, err := n.path(false)
	return toks, err
}

// path is Pointer as reference tokens. With withSeq, seq tells for each token
// whether it indexes a sequence.
func (n Node) path(withSeq bool) (toks []string, seq []bool, _ error) {
	nd := n.nd()
	if nd == nil {
		return nil, nil, n.errInvalid("pointer")
	}
	t := &n.s.tree
	c := n.ctx()
	fail := &EditError{Op: "pointer", Kind: ErrNoPointer}
	for cur := n.i; t.Nodes[cur].Parent != 0; {
		par := t.Nodes[cur].Parent
		pos := 0
		for ch := t.First(par); ch != cur; ch = t.Nodes[ch].Next {
			if ch == 0 {
				return nil, nil, fail
			}
			pos++
		}
		switch t.Nodes[par].Kind {
		case cst.Sequence:
			toks = append(toks, strconv.Itoa(pos))
			if withSeq {
				seq = append(seq, true)
			}
		case cst.Mapping:
			if pos%2 == 0 {
				return nil, nil, fail // a key
			}
			k := t.Prev(cur)
			if t.Nodes[k].Kind != cst.Scalar {
				return nil, nil, fail // a complex key has no token
			}
			toks = append(toks, c.KeyText(k))
			if withSeq {
				seq = append(seq, false)
			}
		default:
			return nil, nil, fail
		}
		cur = par
	}
	for i, j := 0, len(toks)-1; i < j; i, j = i+1, j-1 {
		toks[i], toks[j] = toks[j], toks[i]
		if withSeq {
			seq[i], seq[j] = seq[j], seq[i]
		}
	}
	// Resolve the pointer again: it must lead back to this node.
	loc, err := c.Resolve(toks, false)
	if err != nil || loc.Node != n.i {
		return nil, nil, fail
	}
	return toks, seq, nil
}

// Kind returns the kind of the node.
func (n Node) Kind() NodeKind {
	nd := n.nd()
	if nd == nil {
		return InvalidNode
	}
	switch nd.Kind {
	case cst.Scalar:
		return ScalarNode
	case cst.Mapping:
		return MappingNode
	case cst.Sequence:
		return SequenceNode
	}
	return AliasNode
}

// Style returns how the node is written.
func (n Node) Style() NodeStyle {
	nd := n.nd()
	if nd == nil {
		return PlainStyle
	}
	switch nd.Kind {
	case cst.Scalar:
		switch nd.Style {
		case scan.SingleQuoted:
			return SingleQuotedStyle
		case scan.DoubleQuoted:
			return DoubleQuotedStyle
		case scan.Literal:
			return LiteralStyle
		case scan.Folded:
			return FoldedStyle
		}
		return PlainStyle
	case cst.Alias:
		return PlainStyle
	}
	if nd.Flow {
		return FlowStyle
	}
	return BlockStyle
}

// Anchor returns the anchor name of the node without "&", or "".
func (n Node) Anchor() string {
	if n.nd() == nil {
		return ""
	}
	if p := n.s.tree.PropOf(n.i); p != nil && p.HasAnchor() {
		return string(n.s.src[p.AnchorStart:p.AnchorEnd])
	}
	return ""
}

// AliasName returns the anchor an alias node refers to, or "".
func (n Node) AliasName() string {
	nd := n.nd()
	if nd == nil || nd.Kind != cst.Alias {
		return ""
	}
	return string(n.s.src[nd.Start+1 : nd.End])
}

// Target returns the node an alias refers to; for any other node it returns n.
func (n Node) Target() Node {
	nd := n.nd()
	if nd == nil || nd.Kind != cst.Alias {
		return n
	}
	c := n.ctx()
	t := c.Follow(n.i)
	if t != n.i {
		return n.child(t)
	}
	return Node{}
}

// Value returns the text of a scalar node: quotes removed, escapes expanded, line
// folding and block chomping applied. For other nodes it is "".
func (n Node) Value() string {
	nd := n.nd()
	if nd == nil || nd.Kind != cst.Scalar {
		return ""
	}
	return string(edit.DecodeScalar(nil, &n.s.tree, n.s.src, n.i))
}

// ExplicitTag returns the tag written on the node, resolved: the handle is
// expanded by the %TAG directives of the node's document, "!<uri>" gives the uri,
// and ASCII percent escapes are decoded exactly as the parser does (non-ASCII
// escapes stay as written). The non-specific tag "!" is returned as "!". ok is
// false when no tag was written, which Tag cannot tell apart from a tag the Core
// schema supplies.
func (n Node) ExplicitTag() (tag string, ok bool) {
	nd := n.nd()
	if nd == nil || nd.Kind == cst.Alias {
		return "", false
	}
	p := n.s.tree.PropOf(n.i)
	if p == nil || !p.HasTag() {
		return "", false
	}
	if n.s.dec.propTags != nil {
		return n.s.dec.propTags[nd.Prop], true
	}
	if b, err := n.s.appendTag(nil, int(n.doc), int(p.TagStart), int(p.TagEnd)); err == nil {
		return string(b), true
	}
	return string(n.s.src[p.TagStart:p.TagEnd]), true
}

// TagAsWritten returns the tag exactly as it appears in the text, such as
// "!!str", "!e!foo" or "!<tag:x,2000:y>", or "" when the node has no tag.
func (n Node) TagAsWritten() string {
	if n.nd() == nil {
		return ""
	}
	if p := n.s.tree.PropOf(n.i); p != nil && p.HasTag() {
		return string(n.s.src[p.TagStart:p.TagEnd])
	}
	return ""
}

// Tag returns the resolved tag of the node: the explicit tag with its handle
// expanded by the %TAG directives of the node's document, or the tag the Core
// schema gives the node ("tag:yaml.org,2002:str", ...). The non-specific tag "!"
// gives a scalar the string tag and a collection its default tag, as the
// specification says (section 6.9.1); the decoder reads the node the same way.
// An alias has no tag of its own and returns "".
func (n Node) Tag() string {
	nd := n.nd()
	if nd == nil || nd.Kind == cst.Alias {
		return ""
	}
	const core = "tag:yaml.org,2002:"
	nonSpecific := false
	if t, ok := n.ExplicitTag(); ok {
		if t != "!" {
			return t
		}
		nonSpecific = true
	}
	switch nd.Kind {
	case cst.Mapping:
		return core + "map"
	case cst.Sequence:
		return core + "seq"
	}
	if nonSpecific || nd.Style != scan.Plain {
		return core + "str"
	}
	switch resolve.Core(edit.DecodeScalar(nil, &n.s.tree, n.s.src, n.i)).Kind {
	case resolve.Null:
		return core + "null"
	case resolve.Bool:
		return core + "bool"
	case resolve.Int:
		return core + "int"
	case resolve.Float:
		return core + "float"
	}
	return core + "str"
}

// Len returns the number of items of a sequence or entries of a mapping.
func (n Node) Len() int {
	nd := n.nd()
	if nd == nil {
		return 0
	}
	switch nd.Kind {
	case cst.Sequence:
		return n.s.tree.Count(n.i)
	case cst.Mapping:
		return n.s.tree.Count(n.i) / 2
	}
	return 0
}

// childAt returns the k-th child of node p, or 0. The last position found is
// remembered for the current generation of the tree, so a loop over Item(0),
// Item(1), ... (or Entry) walks each sibling link once instead of once per call.
func (s *docStream) childAt(p int32, k int) int32 {
	c, pos := s.tree.First(p), 0
	if cu := &s.dec.cur; cu.parent == p && cu.gen == s.gen && cu.pos <= k {
		c, pos = cu.node, cu.pos
	}
	for ; pos < k && c != 0; pos++ {
		c = s.tree.Nodes[c].Next
	}
	if c != 0 {
		s.dec.cur = childCursor{parent: p, gen: s.gen, pos: pos, node: c}
	}
	return c
}

// Item returns item i of a sequence, or an invalid Node. Looping over i is linear
// in the length; Items is the shorter way to write that loop.
func (n Node) Item(i int) Node {
	nd := n.nd()
	if nd == nil || nd.Kind != cst.Sequence || i < 0 {
		return Node{}
	}
	if c := n.s.childAt(n.i, i); c != 0 {
		return n.child(c)
	}
	return Node{}
}

// Entry returns the key and value of entry i of a mapping. Looping over i is
// linear in the length; Entries is the shorter way to write that loop.
func (n Node) Entry(i int) (key, value Node) {
	nd := n.nd()
	if nd == nil || nd.Kind != cst.Mapping || i < 0 {
		return Node{}, Node{}
	}
	c := n.s.childAt(n.i, 2*i)
	if c == 0 {
		return Node{}, Node{}
	}
	return n.child(c), n.child(n.s.tree.Nodes[c].Next)
}

// Items returns the items of a sequence in order. It yields nothing for any
// other node.
//
// The loop may edit the Document, as a loop over a map may change the map. After
// an edit the nodes already yielded are stale, but the loop goes on: it finds the
// sequence again by the pointer it had before the latest edit and yields the
// item at the next position, as a fresh node. An edit that inserts or removes
// items of a sequence on the way to this one moves the pointer with it. The loop
// ends if the sequence is replaced or removed, is no longer a sequence, or has no
// pointer (see Node.Pointer). Any number of loops over one Document may run and
// edit at once, and a loop that a panic leaves does not affect the others. A
// loop stepped through iter.Pull and never stopped keeps its place, and the edits
// made on that Document afterwards get slower; call the stop function.
//
// Items appended during the loop are yielded, so a body that appends on every
// pass never ends. Items inserted or removed before the current one may make the
// loop skip items or see some twice, as Go leaves open for a map: to add or
// remove while ranging, collect the pointers first (Node.Pointer), finish the
// loop, then edit.
func (n Node) Items() iter.Seq[Node] {
	return func(yield func(Node) bool) {
		nd := n.nd()
		if nd == nil || nd.Kind != cst.Sequence {
			return
		}
		// The loop keeps only the epoch of its nodes as its own: n stays as it was
		// read, so that a step copies no more than it did before loops could edit.
		s, g := n.s, n.gen
		h := s.watch(n, cst.Sequence)
		defer s.unwatch(h)
		pos := 0
		for c := s.tree.First(n.i); c != 0; c = s.tree.Nodes[c].Next {
			pos++
			if !yield(Node{s: s, i: c, doc: n.doc, gen: g, sc: n.sc}) {
				break
			}
			if g != s.gen && g != s.epoch() {
				// Go on from the item that was yielded last: the loop then steps to the next.
				var ok bool
				if g, c, ok = s.resume(h, pos-1); !ok {
					break
				}
			}
		}
	}
}

// Entries returns the key and value of each entry of a mapping in order. It
// yields nothing for any other node.
//
// The loop may edit the Document, as a loop over a map may change the map. After
// an edit the nodes already yielded are stale, but the loop goes on: it finds the
// mapping again by the pointer it had before the latest edit and yields the entry
// at the next position, as fresh nodes. An edit that inserts or removes items of
// a sequence on the way to this mapping moves the pointer with it. The loop ends
// if the mapping is replaced or removed, is no longer a mapping, or has no
// pointer (see Node.Pointer). Any number of loops over one Document may run and
// edit at once, and a loop that a panic leaves does not affect the others. A
// loop stepped through iter.Pull and never stopped keeps its place, and the edits
// made on that Document afterwards get slower; call the stop function.
//
// Entries added at the end during the loop are yielded, so a body that adds an
// entry on every pass never ends. Entries added or removed before the current one
// may make the loop skip entries or see some twice, as Go leaves open for a map:
// to add or remove while ranging, collect the pointers first (Node.Pointer),
// finish the loop, then edit.
func (n Node) Entries() iter.Seq2[Node, Node] {
	return func(yield func(Node, Node) bool) {
		nd := n.nd()
		if nd == nil || nd.Kind != cst.Mapping {
			return
		}
		s, g := n.s, n.gen
		h := s.watch(n, cst.Mapping)
		defer s.unwatch(h)
		pos := 0
		// The tree is read afresh each time: an edit, or a patch that was rolled
		// back, can leave it in new slices.
		for k := s.tree.First(n.i); k != 0; {
			v := s.tree.Nodes[k].Next
			if v == 0 {
				break
			}
			pos++
			if !yield(Node{s: s, i: k, doc: n.doc, gen: g, sc: n.sc}, Node{s: s, i: v, doc: n.doc, gen: g, sc: n.sc}) {
				break
			}
			if g != s.gen && g != s.epoch() {
				var ok bool
				if g, k, ok = s.resume(h, 2*(pos-1)); !ok {
					break
				}
				v = s.tree.Nodes[k].Next
			}
			k = s.tree.Nodes[v].Next
		}
	}
}

// Lookup returns the value of the first entry of a mapping whose key has the
// scalar text key. Aliases are not followed.
func (n Node) Lookup(key string) (Node, bool) {
	nd := n.nd()
	if nd == nil || nd.Kind != cst.Mapping {
		return Node{}, false
	}
	c := n.ctx()
	for k := n.s.tree.First(n.i); k != 0; {
		v := n.s.tree.Nodes[k].Next
		if v == 0 {
			break
		}
		if n.s.tree.Nodes[k].Kind == cst.Scalar && c.KeyText(k) == key {
			return n.child(v), true
		}
		k = n.s.tree.Nodes[v].Next
	}
	return Node{}, false
}

// Position returns the 1-based line and column of the start of the node, counted
// in characters.
func (n Node) Position() (line, column int) {
	nd := n.nd()
	if nd == nil {
		return 0, 0
	}
	e := scan.NewError(n.s.src, int(nd.Start), scan.Syntax, "")
	return e.Line, e.Column
}

// Span returns the byte offsets of the node's text in Document.Bytes, without its
// anchor and tag. The offsets refer to the UTF-8 text of the stream.
func (n Node) Span() (start, end int) {
	nd := n.nd()
	if nd == nil {
		return 0, 0
	}
	return int(nd.Start), int(nd.End)
}

// Source returns the node's text exactly as written.
func (n Node) Source() string {
	nd := n.nd()
	if nd == nil {
		return ""
	}
	return string(n.s.src[nd.Start:nd.End])
}

// LineComment returns the comment that follows the node on its line, trimmed: it
// starts with the "#" and has no trailing blanks. It is "" when there is none.
// The comment of a block collection or an empty value is the one after the colon
// or dash that introduces it.
func (n Node) LineComment() string {
	nd := n.nd()
	if nd == nil {
		return ""
	}
	s, e := n.ctx().LineCommentSpan(n.i)
	return edit.TrimComment(string(n.s.src[s:e]))
}

// HeadComment returns the comment lines directly above the entry the node belongs
// to (no blank line between), trimmed as yaml.v3 does: each line without its
// indentation and with its "#", the lines joined by line feeds, no line break at
// the end. It is "" when there is none. Use SetHeadComment to change it.
func (n Node) HeadComment() string {
	nd := n.nd()
	if nd == nil {
		return ""
	}
	s, e := n.ctx().HeadCommentSpan(n.i)
	return edit.TrimComment(string(n.s.src[s:e]))
}

// String returns the source text of the node (see Source), so that printing a Node
// prints its YAML. A stale Node prints "<stale node>" and the zero Node
// "<invalid node>".
func (n Node) String() string {
	if n.nd() == nil {
		if n.Stale() {
			return "<stale node>"
		}
		return "<invalid node>"
	}
	return n.Source()
}
