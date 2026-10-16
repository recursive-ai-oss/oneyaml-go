// Package cst builds a flat, lossless tree from parse events.
//
// Nodes live in one slice and refer to each other by index, so building a tree
// allocates a few slices and nothing per node. Every node keeps byte offsets into
// the source. Text between nodes (indicators, spaces, comments, line breaks) is
// not stored: it is still in the source, and the edit code reads it from there
// when it needs it. Index 0 is never a node, so the zero value of a link means
// "none".
package cst

import (
	"bytes"
	"io"

	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/scan"
)

// Kind is the kind of a node.
type Kind uint8

// Node kinds.
const (
	Scalar Kind = iota + 1
	Mapping
	Sequence
	Alias
)

// Node is one node of the tree. Links are indexes into Tree.Nodes; 0 means none.
//
// Start and End cover the node's own text, without its anchor and tag:
//   - scalars: the whole presentation (quotes, block header and content lines;
//     for a block scalar this includes trailing blank lines);
//   - aliases: "*name";
//   - flow collections: from "[" or "{" to just after the closing bracket;
//   - block collections: from the first indicator or key to the end of the last
//     descendant's text.
//
// An empty scalar has Start == End and its offset is not meaningful.
// The children of a mapping alternate key, value. The first child of a collection
// with children is the node right after it (see Tree.First), so a node needs no
// link to it; Next links the later children.
type Node struct {
	Kind  Kind
	Style scan.Style // scalars
	Flow  bool       // flow collection
	Pair  bool       // the single-pair mapping of "[a: b]"

	Start, End int32
	Parent     int32
	Next       int32 // next sibling
	Prop       int32 // index into Tree.Props, 0 if the node has no anchor or tag
	// Indent is the indentation of the content lines of a block scalar and the
	// number of children of a collection, which Count then answers without a walk.
	Indent int32
}

// Prop holds the anchor and tag of a node. Offsets index the source; the anchor
// name excludes "&", the tag is as written.
type Prop struct {
	AnchorStart, AnchorEnd int32
	TagStart, TagEnd       int32
}

// HasAnchor reports whether the property set has an anchor.
func (p *Prop) HasAnchor() bool { return p.AnchorEnd > p.AnchorStart }

// HasTag reports whether the property set has a tag.
func (p *Prop) HasTag() bool { return p.TagEnd > p.TagStart }

// Doc describes one document of the stream.
type Doc struct {
	Root int32 // the root node
	// Start and End are the spans of the start and end markers when they are
	// present (ExplicitStart, ExplicitEnd); otherwise they are empty positions.
	StartMarker, EndMarker     [2]int32
	ExplicitStart, ExplicitEnd bool
	VersionMajor, VersionMinor uint8
	_                          uint8
}

// Tree is the parsed stream.
type Tree struct {
	Nodes []Node
	Props []Prop
	Docs  []Doc

	// lagFrom and lagDelta note offsets that are stale; see ShiftAfterNode.
	lagFrom, lagDelta int32
}

// Build parses src and returns its tree. maxDepth is passed to the parser.
func Build(t *Tree, src []byte, maxDepth int) error {
	var b Builder
	b.ResetWhole(t, src)
	p := parse.New(src, maxDepth)
	for {
		ev, err := p.Next()
		if err == io.EOF {
			b.Finish()
			return nil
		}
		if err != nil {
			return err
		}
		b.Event(&ev)
	}
}

// Builder builds a Tree from parse events. Build drives one from a parser; the
// decoder drives one from recorded events to give a hook the subtree it asks for.
type Builder struct {
	t     *Tree
	src   []byte
	stack []builderOpen
	cur   int32 // document under construction
	sized bool  // ResetWhole was called: the text is a whole stream
}

type builderOpen struct{ node, last int32 }

// Reset starts a new tree in t over src, reusing t's slices.
func (b *Builder) Reset(t *Tree, src []byte) {
	*t = Tree{Nodes: t.Nodes[:0], Props: t.Props[:0], Docs: t.Docs[:0]}
	t.Nodes = append(t.Nodes, Node{}) // index 0: none
	t.Props = append(t.Props, Prop{})
	b.t, b.src, b.stack, b.cur, b.sized = t, src, b.stack[:0], 0, false
}

// ResetWhole is Reset for a parse of a whole stream: it also reserves room for the
// tree of the start of the text, so that building it does not grow the slices step
// by step. Every step of growth leaves the old slice behind, and a tree built that
// way allocates several times its own size.
//
// Only what the parse is likely to reach is reserved: the estimate reads at most
// the first reserveSpan bytes, and the reservation is at most maxReserve nodes, so
// that a huge text, or one that is refused early, costs a bounded amount. Past the
// reservation the slice grows by the density of nodes seen so far (see grow). The
// estimate counts colons, flow brackets and "- " and can be several times too high
// on text that is mostly comment or prose; Finish gives back a large excess.
func (b *Builder) ResetWhole(t *Tree, src []byte) {
	var n, p int
	if len(src) < tinySource {
		// Counting costs more than it saves on a text this short.
		n, p = len(src)/6+4, 1
	} else {
		span := min(len(src), reserveSpan)
		n, p = estimateSizes(src[:span])
		n += n/10 + 16
		// Dense flow text has a node for every 4 or 5 bytes, and nothing real has
		// more; punctuation that is text, not syntax, must not make a large
		// reservation.
		n = min(n, span/4+16, maxReserve)
		p = min(p+2, n)
	}
	if cap(t.Nodes) < n {
		t.Nodes = make([]Node, 0, n)
	}
	if cap(t.Props) < p {
		t.Props = make([]Prop, 0, p)
	}
	b.Reset(t, src)
	b.sized = true
}

// Finish ends a successful parse started with ResetWhole. If the reservation was
// more than twice too high it copies the nodes into a slice that fits, so that a document does
// not keep the excess for as long as it lives.
func (b *Builder) Finish() {
	t := b.t
	if !b.sized || cap(t.Nodes)-len(t.Nodes) < 4096/28 || cap(t.Nodes) < 2*len(t.Nodes) {
		return
	}
	t.Nodes = append(make([]Node, 0, len(t.Nodes)), t.Nodes...)
}

// reserveSpan is how much of the text ResetWhole reads to size the tree, and
// maxReserve the most nodes it reserves: 4M nodes are 112 MB, and a 32-bit build
// must be able to find that.
const (
	reserveSpan = 1 << 20
	maxReserve  = 1 << 22
)

// estimateSizes estimates the nodes and the properties (anchors and tags) of src
// from the number of colons, flow brackets and "- " in it, and of ampersands and
// exclamation marks. A colon is only counted when it is not likely to be in a
// comment: three quarters of the number of "#" are taken off. A longer text is
// sampled in four windows, because counting all of it would cost more than the
// growth it saves is worth on texts with few nodes. Document markers ("---") are
// not counted as dashes, so a stream of markers does not make a large reservation.
func estimateSizes(src []byte) (nodes, props int) {
	count := func(b []byte) (int, int) {
		colons := max(0, bytes.Count(b, colon)-bytes.Count(b, hashMark)*3/4)
		return 2*colons + bytes.Count(b, flowOpenSeq) + bytes.Count(b, flowOpenMap) + bytes.Count(b, dashSpace),
			bytes.Count(b, anchorMark) + bytes.Count(b, tagMark)
	}
	if len(src) <= 4*sampleWindow+sampleWindow/2 {
		return count(src)
	}
	for i := 0; i < 4; i++ {
		at := (len(src) - sampleWindow) / 3 * i
		n, p := count(src[at : at+sampleWindow])
		nodes += n
		props += p
	}
	scale := func(x int) int { return int(int64(x) * int64(len(src)) / (4 * sampleWindow)) }
	return scale(nodes), scale(props)
}

// sampleWindow is the size of each window estimateSizes reads from a long text.
const sampleWindow = 16 << 10

// tinySource is the length below which ResetWhole reserves a fixed small amount.
const tinySource = 256

var (
	colon       = []byte(":")
	flowOpenSeq = []byte("[")
	flowOpenMap = []byte("{")
	dashSpace   = []byte("- ")
	hashMark    = []byte("#")
	anchorMark  = []byte("&")
	tagMark     = []byte("!")
)

// grow makes room for more nodes when the slice is full. The new size follows the
// density of nodes seen so far, projected over the whole text with a little to
// spare, so that the last step lands near the final size instead of leaving a chain
// of slices that are each a fraction too small. A step is at most twice the nodes
// built, so a text that is dense at the start and sparse after that cannot make a
// reservation far beyond its use. off is the offset of the node being added.
func (b *Builder) grow(off int32) {
	t := b.t
	next := cap(t.Nodes) + cap(t.Nodes)/4 + 8
	if off > 0 && len(b.src) > 0 {
		proj := int(int64(len(t.Nodes))*int64(len(b.src))/int64(off)) * 21 / 20
		proj = min(proj, 2*len(t.Nodes), len(b.src)+16)
		next = max(next, proj)
	}
	t.Nodes = append(make([]Node, 0, next), t.Nodes...)
}

func (b *Builder) add(n Node) int32 {
	t := b.t
	idx := int32(len(t.Nodes))
	if b.sized && len(t.Nodes) == cap(t.Nodes) {
		b.grow(n.Start)
	}
	if len(b.stack) > 0 {
		top := &b.stack[len(b.stack)-1]
		if n.Kind == Scalar && n.Start == n.End {
			n.Start = t.emptyAt(b.src, top.node, top.last, &n)
			n.End = n.Start
		}
		n.Parent = top.node
		t.Nodes[top.node].Indent++
		if top.last != 0 {
			// The first child needs no link: it follows its collection.
			t.Nodes[top.last].Next = idx
		}
		top.last = idx
	} else {
		t.Docs[b.cur].Root = idx
	}
	t.Nodes = append(t.Nodes, n)
	return idx
}

func (b *Builder) props(ev *parse.Event) int32 {
	if !ev.HasAnchor() && !ev.HasTag() {
		return 0
	}
	b.t.Props = append(b.t.Props, Prop{int32(ev.AnchorStart), int32(ev.AnchorEnd), int32(ev.TagStart), int32(ev.TagEnd)})
	return int32(len(b.t.Props) - 1)
}

// Event adds one event to the tree.
func (b *Builder) Event(ev *parse.Event) {
	t := b.t
	switch ev.Kind {
	case parse.DocumentStart:
		b.cur = int32(len(t.Docs))
		t.Docs = append(t.Docs, Doc{
			StartMarker: [2]int32{int32(ev.Start), int32(ev.End)}, ExplicitStart: ev.Explicit,
			VersionMajor: uint8(ev.VersionMajor), VersionMinor: uint8(ev.VersionMinor),
		})
	case parse.DocumentEnd:
		d := &t.Docs[b.cur]
		d.EndMarker = [2]int32{int32(ev.Start), int32(ev.End)}
		d.ExplicitEnd = ev.Explicit
	case parse.Scalar:
		b.add(Node{Kind: Scalar, Style: ev.Style, Start: int32(ev.Start), End: int32(ev.End),
			Prop: b.props(ev), Indent: int32(ev.Indent)})
	case parse.Alias:
		b.add(Node{Kind: Alias, Start: int32(ev.Start), End: int32(ev.End)})
	case parse.MappingStart, parse.SequenceStart:
		k := Mapping
		if ev.Kind == parse.SequenceStart {
			k = Sequence
		}
		idx := b.add(Node{Kind: k, Flow: ev.Flow, Pair: ev.Pair, Start: int32(ev.Start), End: int32(ev.End), Prop: b.props(ev)})
		b.stack = append(b.stack, builderOpen{node: idx})
	case parse.MappingEnd, parse.SequenceEnd:
		top := b.stack[len(b.stack)-1]
		b.stack = b.stack[:len(b.stack)-1]
		n := &t.Nodes[top.node]
		switch {
		case n.Flow:
			n.End = int32(ev.End)
		case top.last != 0:
			n.End = t.Nodes[top.last].End
			if fs := t.FullStart(top.node + 1); fs < n.Start {
				n.Start = fs
			}
		default:
			n.End = n.Start
		}
	}
}

// Len returns the number of nodes, not counting the placeholder.
func (t *Tree) Len() int { return len(t.Nodes) - 1 }

// PropOf returns the properties of node n, or nil.
func (t *Tree) PropOf(n int32) *Prop {
	if p := t.Nodes[n].Prop; p != 0 {
		return &t.Props[p]
	}
	return nil
}

// FullStart returns the offset where the node's text begins including any anchor
// and tag.
func (t *Tree) FullStart(n int32) int32 {
	s := t.Nodes[n].Start
	if p := t.PropOf(n); p != nil {
		if p.HasAnchor() && p.AnchorStart-1 < s {
			s = p.AnchorStart - 1
		}
		if p.HasTag() && p.TagStart < s {
			s = p.TagStart
		}
	}
	return s
}

// First returns the first child of collection n, or 0 when n is a scalar, an alias
// or an empty collection. Nodes are stored in document order, so the first child
// is the node that follows its collection.
func (t *Tree) First(n int32) int32 {
	if nd := &t.Nodes[n]; (nd.Kind == Mapping || nd.Kind == Sequence) && nd.Indent > 0 {
		return n + 1
	}
	return 0
}

// Children appends the children of n to dst.
func (t *Tree) Children(dst []int32, n int32) []int32 {
	for c := t.First(n); c != 0; c = t.Nodes[c].Next {
		dst = append(dst, c)
	}
	return dst
}

// Count returns the number of children of n (keys and values both count).
func (t *Tree) Count(n int32) int {
	if nd := &t.Nodes[n]; nd.Kind == Mapping || nd.Kind == Sequence {
		return int(nd.Indent)
	}
	return 0
}

// Last returns the last child of n, or 0. Nodes are stored in document order, so
// the subtree of n is the run of indexes up to the node before n's next sibling
// (or before the next sibling of the nearest ancestor that has one); the last
// child is found from the last node of that run by going up, and the cost is the
// depth, not the number of children.
func (t *Tree) Last(n int32) int32 {
	if t.First(n) == 0 {
		return 0
	}
	y := t.SubtreeEnd(n)
	for y != 0 && t.Nodes[y].Parent != n {
		y = t.Nodes[y].Parent
	}
	return y
}

// Prev returns the sibling before n, or 0: the node before n in document order is
// in that sibling's subtree, and going up from it reaches the sibling.
func (t *Tree) Prev(n int32) int32 {
	par := t.Nodes[n].Parent
	if par == 0 || t.First(par) == n {
		return 0
	}
	x := n - 1
	for x != par && x > 0 && t.Nodes[x].Parent != par {
		x = t.Nodes[x].Parent
	}
	if x == par || x <= 0 {
		return 0
	}
	return x
}

// IsKey reports whether n is a key of a mapping.
func (t *Tree) IsKey(n int32) bool {
	par := t.Nodes[n].Parent
	if par == 0 || t.Nodes[par].Kind != Mapping {
		return false
	}
	// Near the end, count back from the last child: an edit at the end of a large
	// mapping must not read every key before it.
	if cnt := t.Count(par); cnt > isKeyBack {
		l := t.Last(par)
		for j := cnt - 1; l != 0 && j >= cnt-isKeyBack; j-- {
			if l == n {
				return j%2 == 0
			}
			l = t.Prev(l)
		}
	}
	i := 0
	for c := t.First(par); c != n; c = t.Nodes[c].Next {
		i++
	}
	return i%2 == 0
}

// isKeyBack is how many children from the end IsKey counts back over before it
// counts forward from the first.
const isKeyBack = 8

// emptyAt returns the offset to give an empty scalar that is about to become a
// child of parent after prev. The parser reports the position of whatever token
// follows, which is useless for editing; the useful anchor is the indicator the
// empty node belongs to (the ":" of a mapping value, the "-" of a block sequence
// item), or the end of its anchor and tag.
func (t *Tree) emptyAt(src []byte, parent, prev int32, n *Node) int32 {
	if n.Prop != 0 {
		p := &t.Props[n.Prop]
		return max(p.AnchorEnd, p.TagEnd)
	}
	par := &t.Nodes[parent]
	switch {
	case par.Kind == Mapping && !par.Flow && par.Indent%2 == 0:
		// An empty key. The token that follows is the next entry's, usually on a
		// later line, so the key is placed at its own indicator: just after the "?",
		// or on the ":" when there is no "?". The indicator is the first token after
		// the entry before it, or the start of the mapping.
		from := int(par.Start)
		if prev != 0 {
			from = int(t.Nodes[prev].End)
		}
		i := SkipTrivia(src, from)
		if i < len(src) {
			switch src[i] {
			case '?':
				return int32(i + 1)
			case ':':
				return int32(i)
			}
		}
		return n.Start
	case par.Kind == Mapping && prev != 0 && par.Indent%2 == 1:
		// prev is the last child and Indent counts the children linked so far, so
		// an odd count means prev is a key; no walk over the siblings is needed.
		// An empty value: after the colon if there is one.
		pk := &t.Nodes[prev]
		if pk.Kind == Scalar && pk.Start == pk.End && pk.Prop == 0 && pk.Start > 0 && int(pk.Start) <= len(src) {
			// The key is empty and sits at its own indicator. A ":" on the key's own
			// token is its value's colon; after a "?" the value's colon must stand
			// in the "?" column, or it belongs to the next entry.
			if int(pk.Start) < len(src) && src[pk.Start] == ':' {
				return pk.Start + 1
			}
			if src[pk.Start-1] == '?' {
				i := SkipTrivia(src, int(pk.Start))
				if i < len(src) && src[i] == ':' && blankAfter(src, i) && colOf(src, i) == colOf(src, int(pk.Start)-1) {
					return int32(i + 1)
				}
				return pk.End
			}
		}
		i := SkipTrivia(src, int(t.Nodes[prev].End))
		if i < len(src) && src[i] == ':' && (par.Flow || blankAfter(src, i) && t.colonIsValueOf(src, par, prev, i)) {
			return int32(i + 1)
		}
		return t.Nodes[prev].End
	case par.Kind == Sequence && !par.Flow:
		from := par.Start
		if prev != 0 {
			from = t.Nodes[prev].End
		}
		i := SkipTrivia(src, int(from))
		if i < len(src) && src[i] == '-' {
			return int32(i + 1)
		}
	}
	return n.Start
}

// colonIsValueOf reports whether the colon at offset i, found after key node k of
// the block mapping par, is the one that introduces k's value. When the entry
// begins with "?" (wherever its key starts), the value's colon must stand in that
// column. Otherwise the key is implicit and its colon is on the key's line. Any
// other colon belongs to an entry of an outer mapping.
func (t *Tree) colonIsValueOf(src []byte, par *Node, k int32, i int) bool {
	// The entry's own indicator is the first token after the entry before it, or
	// after the start of the mapping.
	from := int(par.Start)
	if p := t.Prev(k); p != 0 {
		from = int(t.Nodes[p].End)
	}
	if q := SkipTrivia(src, from); q < len(src) && q < i && src[q] == '?' && blankAfter(src, q) {
		return colOf(src, i) == colOf(src, q)
	}
	for j := int(t.Nodes[k].End); j < i && j < len(src); j++ {
		if src[j] == '\n' || src[j] == '\r' {
			return false
		}
	}
	return true
}

// ColonIsValueOf reports whether the colon at offset i, found after key node k,
// introduces k's value rather than an entry of an outer mapping.
func (t *Tree) ColonIsValueOf(src []byte, k int32, i int) bool {
	par := &t.Nodes[t.Nodes[k].Parent]
	return par.Flow || t.colonIsValueOf(src, par, k, i)
}

// blankAfter reports whether the byte after offset i ends a line or is white
// space. In block context a colon only introduces a value then; in "::" the
// first colon starts a plain scalar.
func blankAfter(src []byte, i int) bool {
	if i+1 >= len(src) {
		return true
	}
	switch src[i+1] {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// colOf returns the number of bytes between the start of the line holding i and i.
func colOf(src []byte, i int) int {
	j := i
	for j > 0 && src[j-1] != '\n' && src[j-1] != '\r' {
		j--
	}
	return i - j
}

// SkipTrivia returns the offset of the first byte at or after i that is not white
// space, a line break or part of a comment.
func SkipTrivia(src []byte, i int) int {
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\n', '\r':
			i++
		case '#':
			for i < len(src) && src[i] != '\n' && src[i] != '\r' {
				i++
			}
		default:
			return i
		}
	}
	return i
}

// ShiftAfter adds delta to every offset that is at or after from.
func (t *Tree) ShiftAfter(from, delta int32) {
	if delta == 0 {
		return
	}
	if ShiftHook != nil {
		ShiftHook(len(t.Nodes))
	}
	for i := range t.Nodes {
		nd := &t.Nodes[i]
		if nd.Start >= from {
			nd.Start += delta
		}
		if nd.End >= from {
			nd.End += delta
		}
	}
	for i := range t.Props {
		p := &t.Props[i]
		if p.AnchorStart >= from {
			p.AnchorStart += delta
			p.AnchorEnd += delta
		}
		if p.TagStart >= from {
			p.TagStart += delta
			p.TagEnd += delta
		}
	}
	for i := range t.Docs {
		d := &t.Docs[i]
		for j := range d.StartMarker {
			if d.StartMarker[j] >= from {
				d.StartMarker[j] += delta
			}
			if d.EndMarker[j] >= from {
				d.EndMarker[j] += delta
			}
		}
	}
}

// Lagging offsets.
//
// A run of scalar edits through a wide mapping would shift every later offset once
// per edit, which is quadratic. Instead ShiftAfterNode shifts the nodes close to the
// edit at once and notes the shift of the rest as a debt: nodes at or after
// lagFrom are stale by lagDelta. Nodes come in text order, so "the rest" is a
// suffix of Nodes. Whoever is about to read an offset of a node past lagFrom calls
// Settle (everything) or SettleTo (that node and the ones before it) first. A
// Tree that is not being edited has lagFrom == 0 and costs the readers one test.

// lagWindow is how many nodes after an edited node are shifted at once. A loop
// that edits entry after entry then finds the next entry settled, and the work of
// an edit does not depend on the size of the tree.
const lagWindow = 64

// LagPoison, when not zero, makes a tree that starts to lag store its stale offsets
// wrong by this much, so that a test finds any read that did not settle first. It
// is for tests only.
var LagPoison int32

// ShiftHook, when set, is told how many nodes each shift of offsets moves. Tests use
// it to show that the work of an edit does not grow with the size of the tree.
var ShiftHook func(nodes int)

// Lagging reports whether some offsets are stale.
func (t *Tree) Lagging() bool { return t.lagFrom != 0 }

// Frontier is the index from which nodes may be stale: every node before it has
// true offsets.
func (t *Tree) Frontier() int32 {
	if t.lagFrom != 0 {
		return t.lagFrom
	}
	return int32(len(t.Nodes))
}

// Settle brings every offset up to date.
func (t *Tree) Settle() {
	if t.lagFrom != 0 {
		t.settle()
	}
}

// settle is the work of Settle, kept out of line so that the test in front of it is
// small enough to be inlined into the readers.
//
//go:noinline
func (t *Tree) settle() {
	t.shiftNodes(t.lagFrom, int32(len(t.Nodes)), t.lagDelta)
	t.lagFrom, t.lagDelta = 0, 0
}

// SettleTo brings the offsets of node i, and of every node before it, up to date.
func (t *Tree) SettleTo(i int32) {
	if t.lagFrom != 0 && i >= t.lagFrom {
		t.settleTo(i)
	}
}

//go:noinline
func (t *Tree) settleTo(i int32) {
	e := i + 1
	if int(e) >= len(t.Nodes) {
		t.Settle()
		return
	}
	t.shiftNodes(t.lagFrom, e, t.lagDelta)
	t.lagFrom = e
}

// shiftNodes adds delta to the offsets of nodes a to b-1 and of their anchors and
// tags. The properties of a node lie in front of it, so they are in the same range
// as the node.
func (t *Tree) shiftNodes(a, b, delta int32) {
	if delta == 0 {
		return
	}
	if ShiftHook != nil {
		ShiftHook(int(b - a))
	}
	for i := a; i < b; i++ {
		nd := &t.Nodes[i]
		nd.Start += delta
		nd.End += delta
		if nd.Prop != 0 {
			p := &t.Props[nd.Prop]
			if p.AnchorStart != 0 {
				p.AnchorStart += delta
				p.AnchorEnd += delta
			}
			if p.TagStart != 0 {
				p.TagStart += delta
				p.TagEnd += delta
			}
		}
	}
}

// ShiftAfterNode is ShiftAfter for an edit inside the text of node n, whose text
// ended at from before the edit: it gives the same offsets once Settle has run, but
// shifts only the ancestors of n and a window of the nodes after it now.
func (t *Tree) ShiftAfterNode(n, from, delta int32) {
	if delta == 0 {
		return
	}
	// The ancestors are the only nodes before n whose text reaches past it.
	for p := t.Nodes[n].Parent; p != 0; p = t.Nodes[p].Parent {
		nd := &t.Nodes[p]
		if nd.Start >= from {
			nd.Start += delta
		}
		if nd.End >= from {
			nd.End += delta
		}
	}
	size := int32(len(t.Nodes))
	e := n + 1 + lagWindow
	if e > size {
		e = size
	}
	old := t.lagDelta
	if t.lagFrom != 0 {
		// The edit is behind the frontier by more than the stale tail is long:
		// bring the tail up to date and lag from the window on, which costs less
		// than shifting the nodes in between.
		if f := t.lagFrom; f-e > size-f {
			t.shiftNodes(f, size, old)
			t.lagFrom, t.lagDelta, old = 0, 0, 0
		}
	}
	if t.lagFrom != 0 {
		// Nodes up to the window that were stale are brought up to date first.
		if e < t.lagFrom {
			e = t.lagFrom
		}
		t.shiftNodes(t.lagFrom, e, old)
	}
	t.shiftNodes(n+1, e, delta)
	switch {
	case e >= size:
		t.lagFrom, t.lagDelta = 0, 0
	case t.lagFrom == 0:
		// The tail is still true: it becomes the debt.
		t.lagFrom, t.lagDelta = e, delta
		if LagPoison != 0 {
			t.shiftNodes(e, size, -LagPoison)
			t.lagDelta += LagPoison
		}
	default:
		t.lagFrom, t.lagDelta = e, old+delta
		if t.lagDelta == 0 {
			t.lagFrom = 0
		}
	}
	// The markers of the documents are few; they are kept true.
	for i := range t.Docs {
		d := &t.Docs[i]
		for j := range d.StartMarker {
			if d.StartMarker[j] >= from {
				d.StartMarker[j] += delta
			}
			if d.EndMarker[j] >= from {
				d.EndMarker[j] += delta
			}
		}
	}
}
