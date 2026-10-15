package edit

import (
	"bytes"
	"sort"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/pointer"
	"oneyaml.dev/go/internal/scan"
)

// Ctx is the state an edit is planned against: a tree and the source it was built
// from, and the document of the stream being edited.
type Ctx struct {
	T   *cst.Tree
	Src []byte
	Doc int
	// MaxAliasHops bounds the aliases followed while resolving one pointer; 0
	// means 1<<20.
	MaxAliasHops int

	// Cache, when set, holds indexes the caller keeps between contexts; without
	// one, a context that needs an index makes its own.
	Cache *Cache

	// tailVisits counts the nodes anchorsInUse read after the deleted text, and
	// hintReads the scalars styleHint decoded; tests use them to show that the
	// document is read once and that an add does not read the whole container.
	tailVisits, hintReads int32

	bom     int // length of a leading byte order mark
	scratch []byte
	nl      string // the first line break in the source, "\n" if none; "" until firstNL looks
}

// NewCtx prepares a Ctx for document doc of t.
func NewCtx(t *cst.Tree, src []byte, doc int) *Ctx {
	c := &Ctx{T: t, Src: src, Doc: doc}
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		c.bom = 3
	}
	return c
}

// Root returns the root node of the document.
func (c *Ctx) Root() int32 {
	if c.Doc < 0 || c.Doc >= len(c.T.Docs) {
		return 0
	}
	return c.T.Docs[c.Doc].Root
}

// Loc is the outcome of resolving a pointer.
type Loc struct {
	// Node is the addressed node; 0 when the last token names a missing mapping
	// key or the end of a sequence.
	Node int32
	// Parent is the container of Node, 0 for the root.
	Parent int32
	// Key is the key node when Parent is a mapping and Node exists.
	Key int32
	// Tok is the last reference token, Index its position in a sequence (the
	// length of the sequence when it is "-" or past the end by exactly one).
	Tok   string
	Index int
	// Dash reports that Tok was "-".
	Dash bool
}

// IsRoot reports whether the location is the document root.
func (l Loc) IsRoot() bool { return l.Parent == 0 }

// Resolve resolves toks in the document. With write set, descending through an
// alias is refused; otherwise aliases are followed. The last token may name a
// missing mapping key or the position just past a sequence: then Loc.Node is 0 and
// Loc.Parent is the container. Every other miss is ErrNotFound.
func (c *Ctx) Resolve(toks []string, write bool) (Loc, error) {
	cur := c.Root()
	if cur == 0 {
		return Loc{}, notFound("no such document")
	}
	if len(toks) == 0 {
		return Loc{Node: cur}, nil
	}
	hops := 0
	maxHops := c.MaxAliasHops
	if maxHops <= 0 {
		maxHops = 1 << 20
	}
	for i, tok := range toks {
		last := i == len(toks)-1
		nd := &c.T.Nodes[cur]
		if nd.Kind == cst.Alias {
			if write {
				return Loc{}, wrap(ErrAlias, "below the alias %s", c.Src[nd.Start:nd.End])
			}
			hops++
			if hops > maxHops {
				return Loc{}, ErrNotFound
			}
			t := c.anchorOf(cur)
			if t == 0 {
				return Loc{}, notFound("undefined alias")
			}
			cur = t
			nd = &c.T.Nodes[cur]
		}
		switch nd.Kind {
		case cst.Mapping:
			found, key, ambiguous := c.findKey(cur, tok)
			if ambiguous {
				return Loc{}, wrap(ErrAmbiguous, "key %q occurs more than once", tok)
			}
			if found == 0 {
				if last {
					return Loc{Parent: cur, Tok: tok}, nil
				}
				return Loc{}, notFound("no key %q", tok)
			}
			if last {
				return Loc{Node: found, Parent: cur, Key: key, Tok: tok}, nil
			}
			cur = found
		case cst.Sequence:
			n := c.T.Count(cur)
			if tok == "-" {
				if last {
					return Loc{Parent: cur, Tok: tok, Index: n, Dash: true}, nil
				}
				return Loc{}, notFound("\"-\" does not address an item")
			}
			idx, ok := pointer.Index(tok)
			if !ok {
				return Loc{}, notFound("%q is not an array index", tok)
			}
			if idx > n || (idx == n && !last) {
				return Loc{}, notFound("index %d is past the end", idx)
			}
			if idx == n {
				return Loc{Parent: cur, Tok: tok, Index: idx}, nil
			}
			ch := c.T.First(cur)
			for j := 0; j < idx; j++ {
				ch = c.T.Nodes[ch].Next
			}
			if last {
				return Loc{Node: ch, Parent: cur, Tok: tok, Index: idx}, nil
			}
			cur = ch
		default:
			return Loc{}, notFound("%q is below a scalar", tok)
		}
	}
	return Loc{}, ErrNotFound
}

// Follow returns the node an alias refers to, or n itself if it is not an alias.
func (c *Ctx) Follow(n int32) int32 {
	for hops := 0; c.T.Nodes[n].Kind == cst.Alias && hops < 1000; hops++ {
		t := c.anchorOf(n)
		if t == 0 {
			return n
		}
		n = t
	}
	return n
}

// anchorOf finds the node that alias node a refers to: the nearest earlier node
// of the same document with that anchor name.
func (c *Ctx) anchorOf(a int32) int32 {
	nd := &c.T.Nodes[a]
	name := c.Src[nd.Start+1 : nd.End]
	lo := c.Root()
	// Most aliases are near their anchor: look back a little first.
	stop := max(a-anchorNear, lo-1, 0)
	for i := a - 1; i > stop; i-- {
		if p := c.T.Nodes[i].Prop; p != 0 {
			pr := &c.T.Props[p]
			if pr.HasAnchor() && bytes.Equal(c.Src[pr.AnchorStart:pr.AnchorEnd], name) {
				return i
			}
		}
	}
	if stop < lo {
		return 0
	}
	// Further away: look the name up in an index of the document's anchors, built
	// once, so that n aliases cost n lookups and not n walks back.
	if c.Cache == nil {
		c.Cache = &Cache{}
	}
	if a := c.Cache.Anchors; a == nil || a.Doc != c.Doc || a.Gen != c.Cache.Gen {
		c.Cache.Anchors = c.buildAnchorIndex()
		c.Cache.Anchors.Gen = c.Cache.Gen
	}
	list := c.Cache.Anchors.byName[string(name)]
	// The last definition before a.
	i := sort.Search(len(list), func(i int) bool { return list[i] >= a })
	if i == 0 {
		return 0
	}
	return list[i-1]
}

// anchorNear is how many nodes before an alias anchorOf reads before it uses the
// index.
const anchorNear = 128

// AnchorIndex lists, for each anchor name of one document, the nodes that define
// it in document order. It is valid for the version of the tree named by Gen.
type AnchorIndex struct {
	Doc    int
	Gen    uint32
	byName map[string][]int32
}

func (c *Ctx) buildAnchorIndex() *AnchorIndex {
	ix := &AnchorIndex{Doc: c.Doc, byName: make(map[string][]int32)}
	for i := c.Root(); i < c.DocEnd(); i++ {
		if p := c.T.Nodes[i].Prop; p != 0 && c.T.Props[p].HasAnchor() {
			pr := &c.T.Props[p]
			name := string(c.Src[pr.AnchorStart:pr.AnchorEnd])
			ix.byName[name] = append(ix.byName[name], i)
		}
	}
	return ix
}

// keyIs reports whether the key node k has the scalar text tok. Collections and
// aliases never match.
func (c *Ctx) keyIs(k int32, tok string) bool {
	nd := &c.T.Nodes[k]
	if nd.Kind != cst.Scalar {
		return false
	}
	raw := c.Src[nd.Start:nd.End]
	switch nd.Style {
	case scan.Plain:
		// A plain scalar keeps its first and last character however it is folded;
		// most keys differ there, and the line breaks need not be looked for.
		if len(raw) > 0 && len(tok) > 0 && (raw[0] != tok[0] || raw[len(raw)-1] != tok[len(tok)-1]) {
			return false
		}
		if bytes.IndexByte(raw, '\n') < 0 && bytes.IndexByte(raw, '\r') < 0 {
			return string(raw) == tok
		}
	case scan.SingleQuoted:
		in := raw[1 : len(raw)-1]
		if bytes.IndexByte(in, '\'') < 0 && bytes.IndexByte(in, '\n') < 0 && bytes.IndexByte(in, '\r') < 0 {
			return string(in) == tok
		}
	case scan.DoubleQuoted:
		in := raw[1 : len(raw)-1]
		if bytes.IndexByte(in, '\\') < 0 && bytes.IndexByte(in, '\n') < 0 && bytes.IndexByte(in, '\r') < 0 {
			return string(in) == tok
		}
	}
	c.scratch = DecodeScalar(c.scratch[:0], c.T, c.Src, k)
	return string(c.scratch) == tok
}

// KeyText returns the decoded text of a key node.
func (c *Ctx) KeyText(k int32) string {
	c.scratch = DecodeScalar(c.scratch[:0], c.T, c.Src, k)
	return string(c.scratch)
}

// ---- text helpers ----

func isBreak(b byte) bool { return b == '\n' || b == '\r' }

// lineStart returns the offset where the line containing i begins. On the first
// line a byte order mark is not part of the line.
func (c *Ctx) lineStart(i int) int {
	if i > len(c.Src) {
		i = len(c.Src)
	}
	for i > 0 && !isBreak(c.Src[i-1]) {
		i--
	}
	if i < c.bom {
		return c.bom
	}
	return i
}

// lineEnd returns the offset of the line break that ends the line containing i,
// or len(Src).
func (c *Ctx) lineEnd(i int) int {
	for i < len(c.Src) && !isBreak(c.Src[i]) {
		i++
	}
	return i
}

// afterBreak returns the start of the line after the one containing i, or
// len(Src) if there is none.
func (c *Ctx) afterBreak(i int) int {
	i = c.lineEnd(i)
	if i >= len(c.Src) {
		return i
	}
	if c.Src[i] == '\r' && i+1 < len(c.Src) && c.Src[i+1] == '\n' {
		return i + 2
	}
	return i + 1
}

// col returns the column of offset i (bytes since the line start; the text
// before a node on its line is indentation and ASCII indicators).
func (c *Ctx) col(i int) int { return i - c.lineStart(i) }

// onlyBlankBefore reports whether only spaces separate i from the start of its line.
func (c *Ctx) onlyBlankBefore(i int) bool {
	for j := c.lineStart(i); j < i; j++ {
		if c.Src[j] != ' ' && c.Src[j] != '\t' {
			return false
		}
	}
	return true
}

// firstNLWindow bounds how much of the source firstNL reads.
const firstNLWindow = 1 << 16

// firstNL returns the first line break of the source, "\n" if there is none. It
// is found on first use: most calls never need it, and looking for it up front
// costs a pass over a source with no line breaks on every call.
func (c *Ctx) firstNL() string {
	if c.nl != "" {
		return c.nl
	}
	c.nl = "\n"
	// Only the start is read: a source whose first line is longer than this is
	// one long line (a flow collection), and reading all of it for every edit
	// would make a run of edits quadratic.
	head := c.Src[:min(len(c.Src), firstNLWindow)]
	if i := bytes.IndexAny(head, "\r\n"); i >= 0 && c.Src[i] == '\r' {
		c.nl = "\r"
		if i+1 < len(c.Src) && c.Src[i+1] == '\n' {
			c.nl = "\r\n"
		}
	}
	return c.nl
}

// nlAt returns the line break that ends the line containing i, or the
// document's first break if the line has none.
func (c *Ctx) nlAt(i int) string {
	if i > 0 && i < len(c.Src) && c.Src[i] == '\n' && c.Src[i-1] == '\r' {
		i--
	}
	e := c.lineEnd(i)
	if e >= len(c.Src) {
		return c.firstNL()
	}
	if c.Src[e] == '\r' {
		if e+1 < len(c.Src) && c.Src[e+1] == '\n' {
			return "\r\n"
		}
		return "\r"
	}
	return "\n"
}

// commentOnly reports whether the line starting at ls holds only white space and
// a comment, and is not blank.
func (c *Ctx) commentOnly(ls int) bool {
	i := ls
	for i < len(c.Src) && (c.Src[i] == ' ' || c.Src[i] == '\t') {
		i++
	}
	return i < len(c.Src) && c.Src[i] == '#'
}

func (c *Ctx) blankLine(ls int) bool {
	i := ls
	for i < len(c.Src) && (c.Src[i] == ' ' || c.Src[i] == '\t') {
		i++
	}
	return i >= len(c.Src) || isBreak(c.Src[i])
}

// prevLine returns the start of the line before the one starting at ls, or -1.
func (c *Ctx) prevLine(ls int) int {
	if ls <= c.bom {
		return -1
	}
	i := ls - 1
	if c.Src[i] == '\n' && i > 0 && c.Src[i-1] == '\r' {
		i--
	}
	return c.lineStart(i)
}

// ownedStart returns where the comment lines that sit directly above the line
// starting at ls (no blank line between) begin; ls itself if there are none. The
// lines must start at or after floor, the end of the entry before: a line that
// looks like a comment inside a block scalar belongs to the scalar.
func (c *Ctx) ownedStart(ls, floor int) int {
	for {
		p := c.prevLine(ls)
		if p < 0 || p < floor || !c.commentOnly(p) {
			return ls
		}
		ls = p
	}
}

// ---- positions of indicators ----

// colonEnd returns the offset just after the ":" that follows key k, or the end of
// the key if the entry has no colon.
func (c *Ctx) colonEnd(k int32) int {
	e := int(c.T.Nodes[k].End)
	i := cst.SkipTrivia(c.Src, e)
	if i < len(c.Src) && c.Src[i] == ':' {
		return i + 1
	}
	return e
}

// dashOf returns the offset of the "-" that introduces item n of a block sequence.
func (c *Ctx) dashOf(n int32) int {
	nd := &c.T.Nodes[n]
	seq := &c.T.Nodes[nd.Parent]
	from := int(seq.Start)
	if p := c.T.Prev(n); p != 0 {
		from = int(c.T.Nodes[p].End)
	}
	i := cst.SkipTrivia(c.Src, from)
	if i < len(c.Src) && c.Src[i] == '-' {
		return i
	}
	return int(c.T.FullStart(n))
}

// entryStart returns the offset where the entry whose first node is n begins in
// a block collection: the "-" of a sequence item, the "?" of an explicit key, or
// the start of the key.
func (c *Ctx) entryStart(n int32) int {
	par := &c.T.Nodes[c.T.Nodes[n].Parent]
	if par.Kind == cst.Sequence {
		return c.dashOf(n)
	}
	s := int(c.T.FullStart(n))
	i := s - 1
	for i >= 0 && (c.Src[i] == ' ' || c.Src[i] == '\t') {
		i--
	}
	if i >= 0 && c.Src[i] == '?' && (i == 0 || c.Src[i-1] == ' ' || isBreak(c.Src[i-1]) || i == c.bom) {
		return i
	}
	// An implicit key has its colon right after it on the same line; only a
	// key that lacks that can follow a "?" on an earlier line. The test keeps
	// the common case from searching backwards.
	if nd := &c.T.Nodes[n]; nd.Kind == cst.Scalar && nd.End > nd.Start {
		k := int(nd.End)
		for k < len(c.Src) && (c.Src[k] == ' ' || c.Src[k] == '\t') {
			k++
		}
		if k < len(c.Src) && c.Src[k] == ':' {
			return s
		}
	}
	lo := 0
	if p := c.T.Prev(n); p != 0 {
		// The "?" cannot lie before the previous entry's value ends.
		lo = int(c.T.Nodes[p].End)
	}
	return c.questionBefore(s, lo)
}

// startsEntryLine reports whether only blanks and sequence dashes ("- - ") lie
// between the start of the line and offset i, so that an indicator at i begins
// a block entry.
func (c *Ctx) startsEntryLine(i int) bool {
	ls := c.lineStart(i)
	for i > ls {
		switch c.Src[i-1] {
		case ' ', '\t':
			i--
		case '-':
			if i-1 > ls && c.Src[i-2] != ' ' && c.Src[i-2] != '\t' {
				return false
			}
			i--
		default:
			return false
		}
	}
	return true
}

// questionBefore finds the "?" of an explicit key whose content starts on a
// later line ("?\n  key"), and returns s when there is none. Only blanks, line
// breaks and comment lines may lie between the "?" and the key, and the "?"
// must be the first thing on its line, so an earlier entry that ends in a
// question mark is not mistaken for it. The entry's indentation is the "?"
// column, not the column of the key. Only a few comment lines are looked past:
// the search runs on every add, and a longer gap is left as it was (the edit is
// then refused, never written wrong).
func (c *Ctx) questionBefore(s, lo int) int {
	i := s
	for steps := 0; steps < 8; steps++ {
		// Step back over blanks and breaks.
		j := i - 1
		for j >= 0 && (c.Src[j] == ' ' || c.Src[j] == '\t' || isBreak(c.Src[j])) {
			j--
		}
		if j < lo {
			return s
		}
		if c.Src[j] == '?' {
			if c.startsEntryLine(j) && (j+1 >= len(c.Src) || c.Src[j+1] == ' ' || c.Src[j+1] == '\t' || isBreak(c.Src[j+1])) {
				return j
			}
			return s
		}
		// A comment ends the line: drop it and look at what precedes it.
		ls := c.lineStart(j)
		cut := -1
		for k := ls; k <= j; k++ {
			if c.Src[k] == '#' && (k == ls || c.Src[k-1] == ' ' || c.Src[k-1] == '\t') {
				cut = k
				break
			}
		}
		if cut < 0 {
			return s
		}
		i = cut
	}
	return s
}

// textEnd returns the end of the text of n for line-based work: the end of a
// block scalar's last non-blank content line (its trailing blank lines are
// separators unless the scalar keeps them), and the node's end otherwise. For a
// block collection it is the end of its last leaf.
func (c *Ctx) textEnd(n int32) int {
	for {
		nd := &c.T.Nodes[n]
		switch {
		case nd.Kind == cst.Scalar && (nd.Style == scan.Literal || nd.Style == scan.Folded):
			if c.blockChomp(n) == '+' {
				return int(nd.End)
			}
			return c.blockTextEnd(n)
		case (nd.Kind == cst.Mapping || nd.Kind == cst.Sequence) && !nd.Flow && c.T.First(n) != 0:
			n = c.T.Last(n)
		default:
			return int(nd.End)
		}
	}
}

// contentEnd is textEnd without the line break that closes a block scalar: the
// offset right after the last character of the node's text.
func (c *Ctx) contentEnd(n int32) int {
	e := c.textEnd(n)
	if e > c.bom && isBreak(c.Src[e-1]) {
		if c.Src[e-1] == '\n' && e >= 2 && c.Src[e-2] == '\r' {
			return e - 2
		}
		return e - 1
	}
	return e
}

// blockChomp returns '-', '+' or 0 for the header of block scalar n.
func (c *Ctx) blockChomp(n int32) byte {
	nd := &c.T.Nodes[n]
	for i := int(nd.Start) + 1; i < int(nd.End); i++ {
		switch c.Src[i] {
		case '-', '+':
			return c.Src[i]
		case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		default:
			return 0
		}
	}
	return 0
}

// blockTextEnd returns the offset just after the line break of the last content
// line of block scalar n, or the start of its first content line when it has none.
func (c *Ctx) blockTextEnd(n int32) int {
	nd := &c.T.Nodes[n]
	first := c.afterBreak(int(nd.Start))
	end := int(nd.End)
	if first >= end {
		return first
	}
	last := first
	for ls := first; ls < end; {
		if !c.blankLine(ls) {
			last = c.afterBreak(ls)
		}
		next := c.afterBreak(ls)
		if next <= ls {
			break
		}
		ls = next
	}
	if last > end {
		last = end
	}
	return last
}

// hdrEnd returns the offset after the indicator and the properties that
// introduce node n: the end of its anchor and tag if it has any, else the end of
// the ":" or "-" it follows, else the start of the node.
func (c *Ctx) hdrEnd(n int32) int {
	nd := &c.T.Nodes[n]
	e := 0
	if p := c.T.PropOf(n); p != nil {
		e = int(max(p.AnchorEnd, p.TagEnd))
	}
	if nd.Parent != 0 {
		par := &c.T.Nodes[nd.Parent]
		switch {
		case par.Kind == cst.Mapping && !par.Flow && !c.T.IsKey(n):
			e = max(e, c.colonEnd(c.T.Prev(n)))
		case par.Kind == cst.Sequence && !par.Flow:
			e = max(e, c.dashOf(n)+1)
		}
	}
	if e == 0 {
		e = int(nd.Start)
	}
	return e
}

func wrap(kind error, format string, a ...any) error {
	return &detailError{kind: kind, msg: sprintf(format, a...)}
}

type detailError struct {
	kind error
	msg  string
}

func (e *detailError) Error() string { return e.kind.Error() + ": " + e.msg }
func (e *detailError) Unwrap() error { return e.kind }

// LineCommentSpan returns the span of the comment that follows node n on its
// line, empty if there is none. Block collections have no line comment.
func (c *Ctx) LineCommentSpan(n int32) (int, int) {
	nd := &c.T.Nodes[n]
	i := int(nd.End)
	switch {
	case isBlockScalarNode(nd):
		i = int(nd.Start) + 1
		for i < len(c.Src) && (c.Src[i] == '-' || c.Src[i] == '+' || c.Src[i] >= '1' && c.Src[i] <= '9') {
			i++
		}
	case (nd.Kind == cst.Mapping || nd.Kind == cst.Sequence) && !nd.Flow:
		return 0, 0
	}
	for i < len(c.Src) && (c.Src[i] == ' ' || c.Src[i] == '\t') {
		i++
	}
	if i < len(c.Src) && c.Src[i] == '#' {
		return i, c.lineEnd(i)
	}
	return 0, 0
}

// HeadCommentSpan returns the span of the comment lines directly above the entry
// that node n belongs to, empty if there are none.
func (c *Ctx) HeadCommentSpan(n int32) (int, int) {
	first := n
	if nd := &c.T.Nodes[n]; nd.Parent != 0 && c.T.Nodes[nd.Parent].Kind == cst.Mapping && !c.T.IsKey(n) {
		first = c.T.Prev(n)
	}
	if c.T.Nodes[first].Parent == 0 || c.T.Nodes[c.T.Nodes[first].Parent].Flow {
		return 0, 0
	}
	s0 := c.entryStart(first)
	if !c.onlyBlankBefore(s0) {
		return 0, 0
	}
	ls := c.lineStart(s0)
	from := c.ownedStart(ls, c.floorOf(first))
	return from, ls
}

// floorOf returns where the text of the entry that starts with node first can
// begin at the earliest: the end of the entry before it, 0 for the first entry.
// Lines before that offset are part of other entries, whatever they look like.
func (c *Ctx) floorOf(first int32) int {
	if p := c.T.Prev(first); p != 0 {
		return int(c.T.Nodes[p].End)
	}
	return 0
}

func isBlockScalarNode(nd *cst.Node) bool {
	return nd.Kind == cst.Scalar && (nd.Style == scan.Literal || nd.Style == scan.Folded)
}
