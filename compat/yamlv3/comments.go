package yaml

import (
	"bytes"
	"sort"
	"strings"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/scan"
)

// Comments are recovered from the text between tokens, which the tree keeps
// as spans of the source. The rules follow what yaml.v3 does in the cases that
// occur in real files:
//
//   - a comment after a node on its line is the node's line comment, or its
//     key's when the key is the last thing on the line;
//   - comment lines directly above a key or sequence item are its head comment;
//   - comment lines that follow a node directly and are then followed by a blank
//     line are the foot comment of the node they are indented under;
//   - at the start of a document, comment lines followed by a blank line are
//     the document's head comment, and at its end, after a blank line, its foot
//     comment.

type role uint8

const (
	roleRoot role = iota
	roleKey
	roleValue
	roleItem
)

// recent records a key or item and its column, for finding the owner of a foot
// comment by indentation.
type recent struct {
	n   *Node
	col int32
	// ind is the indentation of the collection the entry belongs to, which
	// differs from col for an explicit key (the column of its "?").
	ind int32
}

// itemBit marks, in recent.ind, an entry that is a sequence item. It shares the
// field so that the entry stays 16 bytes.
const itemBit = 1 << 30

func (r recent) indent() int { return int(r.ind &^ itemBit) }

func (r recent) isItem() bool { return r.ind&itemBit != 0 }

func newRecent(n *Node, col, ind int, item bool) recent {
	r := recent{n: n, col: int32(col), ind: int32(ind)}
	if item {
		r.ind |= itemBit
	}
	return r
}

type commentState struct {
	on     bool
	recent []recent
	// seenToken is set once the first token has been handled.
	seenToken bool
	// headLine is the line a head comment was last taken for (plus one), so that
	// the nodes that start on one line do not all claim the comment above it.
	headLine int
	// buf is reused for the lines of each comment block.
	buf []span
	// prevEmpty is set when the node handled last was a mapping value with no
	// text at all ("key:" and nothing after it). yaml.v3 does not close a key's
	// comments at a blank line after such a value.
	prevEmpty bool
	// firstKey is set while the first key of a mapping that is the value of the
	// key before it is placed: nothing but that key's colon comes before it.
	firstKey bool
	// gap and text are reused by gapComments.
	gap  []gapLine
	text []byte
	// pendHead and pendLine are comments that yaml.v3 puts on the first scalar
	// inside a block collection rather than on the collection: they wait here for
	// that scalar.
	pendHead, pendLine string
	// tokenHead is set when the head comment just given to a node began on the
	// line of an item that has no value ("- # c"). yaml.v3 hands such a comment to
	// the first scalar of the item's collection, not to the collection.
	tokenHead bool
	// keepHead is the part of that head comment that stays on the collection: what
	// stands above the dash line, on the line of the document start.
	keepHead string
	// collNode and collAt are the last collection item to take a head comment and
	// the offset where it starts (the start of its line is found when needed).
	// Text above that line is what it took, and a node read later must not take it
	// again.
	collNode *Node
	collAt   int
	// headRng is the stretch of source lines the head comment given to the node
	// being placed was read from; collRng and pendRng are the same for collNode's
	// head comment and for pendHead. Comment text can repeat, so "was this line
	// already given out" is decided by where a line is, never by what it says. A
	// zero range means the place is not known.
	headRng, collRng, pendRng srcRange
	// footRng is where the lines lie that were last given out as a foot comment
	// from above a dash line.
	footRng srcRange
	// gapLS is the start of the line gapComments was last called for.
	gapLS int
	// textLo is where the text that gapComments built last starts.
	textLo int
}

func (b *builder) initComments() {
	sh := b.sh
	if !sh.hashKnown {
		sh.hashKnown = true
		sh.hasHash = bytes.IndexByte(b.ref.Src, '#') >= 0
		if stepCount != nil {
			stepCount.Add(int64(len(b.ref.Src)))
		}
	}
	b.cs.on = sh.hasHash
	if !b.cs.on {
		return
	}
	t := b.ref.Tree
	// A key or an item is recorded for every other node or so.
	b.cs.recent = make([]recent, 0, len(t.Nodes)/2+len(t.Nodes)/8)
	for i := 1; i < len(t.Nodes); i++ {
		nd := &t.Nodes[i]
		if nd.Kind == cst.Scalar && nd.End > nd.Start && bytes.IndexByte(b.ref.Src[nd.Start:nd.End], '\n') >= 0 {
			sh.addSpan([2]int32{nd.Start, nd.End})
		}
	}
}

// insideScalar reports whether offset off is within a multi-line scalar, past
// its first line.
func (b *builder) insideScalar(off int) bool {
	sh := b.sh
	if sh.unsorted {
		sort.Slice(sh.spans, func(i, j int) bool { return sh.spans[i][0] < sh.spans[j][0] })
		sh.unsorted = false
	}
	sp := sh.spans
	i := sort.Search(len(sp), func(i int) bool { return int(sp[i][0]) > off })
	return i > 0 && off < int(sp[i-1][1])
}

func lineStart(src []byte, i int) int {
	for i > 0 && src[i-1] != '\n' {
		i--
	}
	return i
}

func lineEnd(src []byte, i int) int {
	for i < len(src) && src[i] != '\n' {
		i++
	}
	return i
}

// lineComment returns the comment that starts on the line of offset i after
// optional blanks and one comma, or "".
func lineComment(src []byte, i int) string {
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	if i < len(src) && src[i] == ',' {
		i++
		for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
			i++
		}
	}
	if i < len(src) && src[i] == '#' {
		e := lineEnd(src, i)
		return string(bytes.TrimRight(src[i:e], " \t\r"))
	}
	return ""
}

// span is a stretch of the source text, kept as offsets so that finding a comment
// block allocates nothing until the comment is joined into its final string.
type span struct{ a, b int }

// commentBlockAbove collects the comment-only lines that end directly above the
// line starting at ls, in text order, appending them to dst. blankBelow is the
// number of blank lines between the block and ls.
func (b *builder) commentBlockAbove(src []byte, ls int, dst []span) (block []span, start int, blankBelow int) {
	i := ls
	// prevLine returns the line that ends just before offset i, without its line
	// break.
	prevLine := func(i int) (s []byte, ps int, ok bool) {
		if i <= 0 {
			return nil, 0, false
		}
		ps = lineStart(src, i-1)
		return bytes.TrimRight(src[ps:i-1], "\r"), ps, true
	}
	for {
		l, ps, ok := prevLine(i)
		if !ok || len(bytes.TrimSpace(l)) != 0 {
			break
		}
		blankBelow++
		i = ps
	}
	start = i
	block = dst[:0]
	for {
		l, ps, ok := prevLine(i)
		if !ok {
			break
		}
		t := bytes.TrimSpace(l)
		dash := false
		if len(t) > 0 && t[0] == '-' {
			// An item with no value and a comment, "- # c": yaml.v3 hands the
			// comment to whatever comes next, as if it stood on a line of its own.
			// A dash counts only when a space follows it or it ends the line:
			// "-# c" is a plain scalar.
			// Known differences from yaml.v3: a tab after the dash (it reads the
			// document as null), a second dash on the line ("- - # c", which it
			// turns into a foot comment) and a mapping item ("- # c" over "  k: v",
			// where it heads the key).
			u := t
			for len(u) > 0 && u[0] == '-' && (len(u) == 1 || u[1] == ' ') {
				u = bytes.TrimLeft(u[1:], " ")
				dash = true
			}
			if dash {
				t = u
			}
		}
		if len(t) == 0 || t[0] != '#' || b.insideScalar(ps+cap(l)-cap(t)) {
			break
		}
		lead := cap(l) - cap(t)
		block = append(block, span{ps + lead, ps + lead + len(t)})
		i = ps
		start = i
		if dash {
			// The lines above belong to the item on this line, not to the next.
			break
		}
	}
	// The lines were found bottom up.
	for x, y := 0, len(block)-1; x < y; x, y = x+1, y-1 {
		block[x], block[y] = block[y], block[x]
	}
	return block, start, blankBelow
}

// joinSpans joins the lines of a comment block with line breaks.
func joinSpans(src []byte, block []span) string {
	if len(block) == 1 {
		return string(src[block[0].a:block[0].b])
	}
	n := len(block) - 1
	for _, s := range block {
		n += s.b - s.a
	}
	var sb strings.Builder
	sb.Grow(n)
	for i, s := range block {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.Write(src[s.a:s.b])
	}
	return sb.String()
}

// commentsFor sets the comments of the node n that was built from tree node c.
func (b *builder) commentsFor(t *cst.Tree, c int32, n *Node, r role) {
	if !b.cs.on {
		return
	}
	src := b.ref.Src
	nd := &t.Nodes[c]
	b.cs.tokenHead, b.cs.keepHead = false, ""
	b.cs.headRng = srcRange{}
	start := int(t.FullStart(c))
	end := int(nd.End)
	// Line comment.
	switch {
	case r == roleKey && nd.Kind == cst.Scalar:
		i := end
		for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
			i++
		}
		if i < len(src) && src[i] == ':' {
			n.LineComment = lineComment(src, i+1)
		} else if nd.Start != nd.End && explicitKeyLine(src, start) {
			// "? key # c": the key stands alone on its line.
			n.LineComment = lineComment(src, end)
		}
	case nd.Kind == cst.Scalar && (nd.Style == scan.Literal || nd.Style == scan.Folded):
		// The comment goes after the header, which is on the first line.
		i := int(nd.Start) + 1
		for i < len(src) && (src[i] == '+' || src[i] == '-' || src[i] >= '0' && src[i] <= '9') {
			i++
		}
		n.LineComment = lineComment(src, i)
	case nd.Kind == cst.Scalar || nd.Kind == cst.Alias || (nd.Kind != cst.Scalar && (nd.Flow || nd.Pair)):
		if r != roleKey && !(nd.Kind == cst.Scalar && nd.Start == nd.End) {
			n.LineComment = lineComment(src, end)
		}
	}
	// Head comment, and foot comment of what came before.
	collection := (nd.Kind == cst.Mapping || nd.Kind == cst.Sequence) && !nd.Flow && !nd.Pair
	flowParent := false
	if p := nd.Parent; p != 0 {
		flowParent = t.Nodes[p].Flow || t.Nodes[p].Pair
	}
	valueScalar := r == roleValue && nd.Kind == cst.Scalar && nd.Start != nd.End && startsLine(src, start)
	if comment := b.headerComment(t, c, src, collection); comment != "" {
		if collection {
			b.cs.pendLine = comment
		} else if n.LineComment == "" && nd.Kind != cst.Mapping && nd.Kind != cst.Sequence {
			n.LineComment = comment
		}
	}
	if (r == roleKey || r == roleItem || r == roleRoot || valueScalar || flowParent && r != roleValue) && (!collection || r == roleItem) {
		col, ok := firstOnLineIn(src, start, valueScalar, flowParent)
		if ls := lineStart(src, start); ok && ls+1 != b.cs.headLine {
			b.cs.headLine = ls + 1
			b.cs.firstKey = r == roleKey && nd.Parent != 0 && c == nd.Parent+1
			if b.cs.seenToken || dashAbove(src, ls) {
				// Placed from the whole stretch of comments and blank lines above. When
				// there is no comment in it there is nothing to place.
				b.gapComments(src, ls, n)
			} else if block, bstart, blank := b.commentBlockAbove(src, ls, b.cs.buf); true {
				b.cs.buf = block
				if !b.cs.seenToken {
					if h, ok := b.docStartHead(src, block, bstart, blank); ok {
						n.HeadComment = h
						block = nil
					}
				}
				if len(block) > 0 {
					switch {
					case blank == 0 && b.cs.seenToken && bstart > 0 && indentOf("", src, bstart) > col && !b.blankAbove(bstart):
						// The comment is indented deeper than the node after it, so it ends
						// the collection before: it is a foot comment.
						b.footFor(joinSpans(src, block), indentOf("", src, bstart))
					case blank == 0:
						n.HeadComment = joinSpans(src, block)
						b.cs.tokenHead = dashLine(src, bstart)
					default:
						// Separated from this node by blank lines.
						if !b.cs.seenToken {
							// The start of the document: see documentComments.
						} else if bstart > 0 && b.blankAbove(bstart) {
							n.HeadComment = joinSpans(src, block) + "\n"
						} else if bstart > 0 {
							b.footFor(joinSpans(src, block), indentOf("", src, bstart))
						}
					}
				}
				if len(block) > 0 && bstart > 0 && b.cs.seenToken && b.blankAbove(bstart) && !dashLine(src, bstart) {
					// Another block above, set off by a blank line and directly under the
					// previous content, is that content's foot comment.
					block2, bstart2, _ := b.commentBlockAbove(src, bstart, nil)
					if len(block2) > 0 && bstart2 > 0 && !b.blankAbove(bstart2) {
						b.footFor(joinSpans(src, block2), indentOf("", src, bstart2))
					}
				}
			}
			if r == roleItem {
				// An item is indented to its dash, not to the text after it, and the
				// dash may be on the line above ("- # c" and then the item).
				col = indentOf("", src, lineStart(src, start))
				if ls := lineStart(src, start); ls > 0 && len(bytes.TrimSpace(src[ls:start])) == 0 {
					pl := lineStart(src, ls-1)
					if dashComment(bytes.TrimSpace(src[pl : ls-1])) {
						col = indentOf("", src, pl)
					}
				}
			}
			ind := col
			if r == roleKey && nd.Kind == cst.Scalar && nd.Start != nd.End && explicitKeyLine(src, start) {
				// An explicit key is indented to its "?".
				ind = bytes.LastIndexByte(src[lineStart(src, start):start], '?')
			}
			if r != roleValue {
				b.cs.recent = append(b.cs.recent, newRecent(n, col, ind, r == roleItem))
			}
		} else if ok && (r == roleKey || r == roleItem) {
			// A second node that starts on the line (the key of "- key: v") is not
			// given the comments above, but a foot comment further down can belong
			// to it.
			if r == roleItem {
				col = dashColumn(src, start, col)
			}
			b.cs.recent = append(b.cs.recent, newRecent(n, col, col, r == roleItem))
		}
	}
	if r == roleItem && collection {
		b.cs.collNode, b.cs.collAt, b.cs.collRng = n, start, b.cs.headRng
	}
	if r == roleItem && collection && n.HeadComment != "" && (nd.Prop != 0 || b.cs.tokenHead) {
		// With a tag or an anchor in front, yaml.v3 gives the comment above the item
		// to the first scalar inside it.
		head := n.HeadComment
		n.HeadComment = ""
		k := b.cs.keepHead
		if k != "" && strings.HasPrefix(head, k+"\n") {
			head, n.HeadComment = head[len(k)+1:], k
		}
		rng := b.cs.headRng
		if k != "" && n.HeadComment == k && rng.known() {
			// The kept part is the first lines; the rest starts at the node's own text.
			rng.lo = b.cs.textLo
		}
		if b.cs.pendHead != "" {
			// An outer item's comment is still waiting: both go to the first scalar.
			head = b.cs.pendHead + "\n" + head
			if b.cs.pendRng.known() && rng.known() {
				rng.lo = b.cs.pendRng.lo
			} else {
				rng = srcRange{}
			}
		}
		b.cs.pendHead, b.cs.pendRng = head, rng
	}
	b.applyPending(n, nd, collection)
	b.cs.prevEmpty = r == roleValue && nd.Kind == cst.Scalar && nd.Start == nd.End && nd.Prop == 0
	if !collection {
		b.cs.seenToken = true
	}
}

// dashAbove reports whether the comment and blank lines above the line that
// starts at ls are headed by an item with no value and a comment ("- # c"). At
// the start of a document those lines need the same reading as anywhere else.
func dashAbove(src []byte, ls int) bool {
	_, ok := dashLineAbove(src, ls)
	return ok
}

// dashLineAbove returns the start of the line of an item with no value and a
// comment that the comment and blank lines above the line at ls lead up to.
func dashLineAbove(src []byte, ls int) (int, bool) {
	for i := ls; i > 0; {
		ps := lineStart(src, i-1)
		t := bytes.TrimSpace(src[ps : i-1])
		if len(t) == 0 || t[0] == '#' {
			i = ps
			continue
		}
		return ps, dashComment(t)
	}
	return 0, false
}

// dashColumn returns the column of the "-" that stands just before offset pos
// on its line, or def when there is none.
func dashColumn(src []byte, pos, def int) int {
	ls := lineStart(src, pos)
	j := pos
	for j > ls && (src[j-1] == ' ' || src[j-1] == '	') {
		j--
	}
	if j > ls && src[j-1] == '-' {
		return j - 1 - ls
	}
	return def
}

// srcRange is a stretch of the source, from the start of its first comment
// line to the end of its last one. The zero value means unknown.
type srcRange struct{ lo, hi int }

func (r srcRange) known() bool { return r.hi > 0 }

// footOnce gives the comment lines in block out as a foot comment, unless those
// very lines were given out that way already: the items nested on one dash line
// each look at the stretch above it, and a line is somebody's foot once.
func (b *builder) footOnce(block []span, col int) {
	r := spanRange(block)
	if f := b.cs.footRng; f.known() && f.lo <= r.lo && r.hi <= f.hi {
		return
	}
	b.cs.footRng = r
	b.footFor(joinSpans(b.ref.Src, block), col)
}

// spanRange returns the range covered by the comment lines of block.
func spanRange(block []span) srcRange {
	if len(block) == 0 {
		return srcRange{}
	}
	return srcRange{block[0].a, block[len(block)-1].b}
}

// gapLine is a line of the stretch that gapComments reads.
type gapLine struct {
	blank bool
	col   int
	a, e  int
	// tokenLine is a comment on the line of an item that has no value ("- # c").
	tokenLine bool
	// dash is the column of its dash.
	dash int
}

// gapComments places the comments in the stretch of comment lines and blank
// lines that lies between the previous content and the node on the line that
// starts at ls, the way yaml.v3 reads them as one piece of text:
//
//   - a first block that follows the previous content directly and is then
//     followed by a blank line is its foot, unless the content was a key with no
//     value;
//   - a line indented less than the enclosing collection, and different from the
//     block's first line, starts a new piece, and what came before it is a foot;
//   - everything left, blank lines between blocks included, is the head comment of
//     the node, which is a foot instead if the node itself is dedented.
//
// It reports false, having changed nothing, when the stretch is not of that
// kind, so the caller can use the simpler rules.
func (b *builder) gapComments(src []byte, ls int, n *Node) bool {
	prevGap := b.cs.gapLS
	b.cs.gapLS = ls
	up := b.cs.gap[:0]
	defer func() { b.cs.gap = up[:0] }()
	hasComment := false
	tokLS := -1
	i := ls
	for {
		if i <= 0 {
			return false
		}
		ps := lineStart(src, i-1)
		l := bytes.TrimRight(src[ps:i-1], "\r")
		t := bytes.TrimSpace(l)
		if len(t) == 0 {
			up = append(up, gapLine{blank: true})
			i = ps
			continue
		}
		lead := indexNonBlank(l)
		if t[0] == '#' && !b.insideScalar(ps+lead) {
			up = append(up, gapLine{col: lead, a: ps + lead, e: ps + lead + len(t)})
			hasComment = true
			i = ps
			continue
		}
		if dashComment(t) {
			// The comment belongs to what follows the item, and it starts the text.
			h := bytes.IndexByte(l, '#')
			up = append(up, gapLine{col: h, a: ps + h, e: ps + lead + len(t), tokenLine: true, dash: dashColumn(src, ps+h, lead)})
			hasComment = true
			tokLS = ps
			break
		}
		if t[0] == '%' || isMarkerLine(t) {
			return false
		}
		break
	}
	if !hasComment {
		return false
	}
	// up was filled from the bottom; lines is the same stretch in text order.
	lines := up
	for x, y := 0, len(lines)-1; x < y; x, y = x+1, y-1 {
		lines[x], lines[y] = lines[y], lines[x]
	}
	indent := 0
	if r := b.cs.recent; len(r) > 0 {
		indent = r[len(r)-1].indent()
	}
	if lines[0].tokenLine {
		// Reading the dash ended every collection that was more deeply indented.
		indent = lines[0].dash
	}
	nodeCol := indentOf("", src, ls)
	firstEmpty := !lines[0].blank
	text := b.cs.text[:0]
	defer func() { b.cs.text = text[:0] }()
	var textCol, startIdx, textLo, textHi int
	recentEmpty := false
	for idx, l := range lines {
		if l.blank {
			if !recentEmpty {
				if firstEmpty && ((startIdx == 0 && !lines[0].tokenLine && !b.cs.prevEmpty && !b.cs.firstKey) || textCol < indent) {
					if len(text) > 0 {
						b.footFor(string(text), textCol)
						text = text[:0]
					}
				} else if len(text) > 0 {
					text = append(text, '\n')
				}
			}
			firstEmpty = false
			recentEmpty = true
			continue
		}
		if len(text) > 0 && l.col < indent && l.col != textCol {
			b.footFor(string(text), textCol)
			text = text[:0]
		}
		if len(text) == 0 {
			textCol, startIdx, textLo = l.col, idx, l.a
		} else {
			text = append(text, '\n')
		}
		text = append(text, src[l.a:l.e]...)
		textHi = l.e
		recentEmpty = false
	}
	aboveHead := ""
	var aboveRng srcRange
	// With a second dash on the dash line ("- - # c") the item before it took what
	// stands above the line, and a sibling at the dash's column is not the content of an item.
	singleDash := tokLS > 0 && lines[0].tokenLine && lines[0].dash == indentOf("", src, tokLS) && nodeCol > lines[0].dash
	if singleDash && b.cs.seenToken {
		// The stretch stops at the dash line. The block above it, set off by a blank
		// line and directly under the previous content, is that content's foot. Any
		// other block above the dash heads the collection; a block above that one,
		// set off by a blank line and directly under the content, is the foot.
		if above, astart, ablank := b.commentBlockAbove(src, tokLS, nil); len(above) > 0 && astart > 0 {
			if ablank > 0 && !b.blankAbove(astart) && !b.pendingHolds(joinSpans(src, above), tokLS, spanRange(above)) {
				b.footOnce(above, indentOf("", src, astart))
			} else {
				aboveHead = joinSpans(src, above)
				aboveRng = spanRange(above)
				if ablank > 0 {
					aboveHead += "\n"
				}
				if up2, s2, blank2 := b.commentBlockAbove(src, astart, nil); len(up2) > 0 && blank2 > 0 && s2 > 0 && !b.blankAbove(s2) && !b.pendingHolds(joinSpans(src, up2), tokLS, spanRange(up2)) {
					b.footOnce(up2, indentOf("", src, s2))
				}
			}
		}
	}
	if len(text) == 0 {
		return true
	}
	if nodeCol < indent && nodeCol != textCol {
		b.footFor(string(text), textCol)
		return true
	}
	n.HeadComment = string(text)
	b.cs.tokenHead = lines[0].tokenLine
	b.cs.textLo = textLo
	b.cs.headRng = srcRange{textLo, textHi}
	if aboveHead != "" {
		aboveHead, aboveRng = b.narrowAbove(aboveHead, aboveRng, tokLS, prevGap)
		b.takeFromOuter(aboveHead, aboveRng, tokLS)
		b.cs.keepHead = aboveHead
		n.HeadComment = aboveHead + "\n" + n.HeadComment
		if aboveRng.known() && textHi > 0 {
			b.cs.headRng = srcRange{aboveRng.lo, textHi}
		} else {
			b.cs.headRng = srcRange{}
		}
	}
	if b.cs.tokenHead && !b.cs.seenToken && singleDash && len(b.cs.recent) == 0 {
		// The line of the document start, with what stands above it, heads the
		// collection; the dash line and what follows go to its first scalar. So
		// do comments that stand right above the dash line.
		if h, ok := b.docStartHead(src, nil, tokLS, 0); ok {
			b.cs.keepHead = h
			b.cs.headRng = srcRange{}
			n.HeadComment = h + "\n" + n.HeadComment
		} else if above, _, ablank := b.commentBlockAbove(src, tokLS, nil); len(above) > 0 && ablank == 0 {
			h := joinSpans(src, above)
			b.cs.keepHead = h
			b.cs.headRng = srcRange{}
			n.HeadComment = h + "\n" + n.HeadComment
		}
	}
	return true
}

// collLS returns the start of the line of the collection item last read.
func (b *builder) collLS() int { return lineStart(b.ref.Src, b.cs.collAt) }

// pendingHolds reports whether the waiting head comment of an outer item holds
// the text s, read from above the line at ls: that item took the lines. The
// lines are at rng; the same words elsewhere are other lines.
func (b *builder) pendingHolds(s string, ls int, rng srcRange) bool {
	p := strings.TrimRight(b.cs.pendHead, "\n")
	if b.cs.collNode == nil || ls != b.collLS() || p == "" || !strings.Contains(p, s) {
		return false
	}
	if pr := b.cs.pendRng; pr.known() && rng.known() {
		return pr.lo <= rng.lo && rng.hi <= pr.hi
	}
	return true
}

// narrowAbove cuts the text above the dash line at ls down to what the collection
// that starts there still holds: the earlier lines were given away as the foot of
// something else when that collection was read. It returns the cut text and
// where it lies in the source (rng is where the whole text lies).
func (b *builder) narrowAbove(above string, rng srcRange, ls, prevGap int) (string, srcRange) {
	if b.cs.collNode == nil || ls != b.collLS() {
		return above, rng
	}
	t := strings.TrimRight(above, "\n")
	type held struct {
		text string
		rng  srcRange
	}
	for _, h := range []held{{strings.TrimRight(b.cs.pendHead, "\n"), b.cs.pendRng}, {strings.TrimRight(b.cs.collNode.HeadComment, "\n"), b.cs.collRng}} {
		if h.text == "" {
			continue
		}
		if h.text != t && strings.HasSuffix(t, "\n"+h.text) {
			if h.rng.known() && rng.known() && (h.rng.hi != rng.hi || h.rng.lo <= rng.lo) {
				// The same words stand elsewhere: these are other lines.
				continue
			}
			lo := h.rng.lo
			if !h.rng.known() {
				lo = 0
			}
			return h.text + above[len(t):], srcRange{lo, rng.hi}
		}
		if h.text == t {
			if h.rng.known() && rng.known() && h.rng != rng {
				continue
			}
			return above, rng
		}
	}
	if prevGap == ls {
		// That collection read the whole stretch and kept none of it.
		return "", srcRange{}
	}
	return above, rng
}

// takeFromOuter takes, from the head comment waiting for an outer item and from
// the head comment of the collection item that starts on the line at ls, the
// lines of the text above that this node reads again: yaml.v3 gives them to the
// innermost collection that starts at the dash line, and no line is given out
// twice. The lines are matched by where they lie (rng), not by what they say.
func (b *builder) takeFromOuter(above string, rng srcRange, ls int) {
	if b.cs.collNode == nil || ls != b.collLS() {
		return
	}
	trim := func(s string) string { return strings.TrimRight(s, "\n") }
	if p := b.cs.pendHead; p != "" {
		pr := b.cs.pendRng
		both := pr.known() && rng.known()
		switch {
		case strings.HasSuffix(trim(above), trim(p)) && (!both || (pr.hi == rng.hi && rng.lo <= pr.lo)):
			b.cs.pendHead, b.cs.pendRng = "", srcRange{}
		case strings.HasSuffix(trim(p), trim(above)) && (!both || (pr.hi == rng.hi && pr.lo <= rng.lo)):
			b.cs.pendHead = strings.TrimSuffix(strings.TrimSuffix(trim(p), trim(above)), "\n")
			if both {
				b.cs.pendRng = srcRange{pr.lo, rng.lo}
			}
		}
	}
	if n := b.cs.collNode; n != nil && n.HeadComment != "" && trim(n.HeadComment) == trim(above) {
		if cr := b.cs.collRng; !cr.known() || !rng.known() || cr == rng {
			n.HeadComment = ""
		}
	}
}

// indexNonBlank returns the offset of the first byte of l that is not a space or
// a tab.
func indexNonBlank(l []byte) int {
	i := 0
	for i < len(l) && (l[i] == ' ' || l[i] == '	') {
		i++
	}
	return i
}

// isMarkerLine reports whether the trimmed line t is a document start or end
// marker.
func isMarkerLine(t []byte) bool {
	return (bytes.HasPrefix(t, []byte("---")) || bytes.HasPrefix(t, []byte("..."))) && (len(t) == 3 || t[3] == ' ' || t[3] == '	')
}

// dashLine reports whether the line that starts at ls is an item with a comment
// and no value ("- # c"). The block that ends at such a line stops there, so what
// lies above it was not taken as part of the block and is not a separate one.
func dashLine(src []byte, ls int) bool {
	t := bytes.TrimSpace(src[ls:lineEnd(src, ls)])
	return len(t) > 0 && t[0] == '-' && !bytes.HasPrefix(t, []byte("---")) && (len(t) == 1 || t[1] == ' ')
}

// blankAbove reports whether the line above the line that starts at ls is blank.
func (b *builder) blankAbove(ls int) bool {
	if ls <= 0 {
		return false
	}
	ps := lineStart(b.ref.Src, ls-1)
	return len(bytes.TrimSpace(b.ref.Src[ps:ls-1])) == 0
}

func indentOf(line string, src []byte, ls int) int {
	n := 0
	for n < len(src)-ls && (src[ls+n] == ' ' || src[ls+n] == '\t') {
		n++
	}
	return n
}

// footFor attaches a foot comment to the most recent key or item that is
// indented no deeper than the comment. It reports true when that entry is an
// item that has a foot comment already, so nothing was attached.
func (b *builder) footFor(text string, col int) (full bool) {
	return b.footForAt(text, col, true)
}

// footForAt is footFor. With join, text that finds a key which has a foot
// comment already is added to it, so that the comments at the end of a document
// are not lost.
func (b *builder) footForAt(text string, col int, join bool) (full bool) {
	for i := len(b.cs.recent) - 1; i >= 0; i-- {
		if int(b.cs.recent[i].col) <= col {
			n := b.cs.recent[i].n
			if n.FootComment == "" {
				n.FootComment = text
				return false
			}
			if join && !b.cs.recent[i].isItem() {
				n.FootComment += "\n" + text
				return false
			}
			return b.cs.recent[i].isItem()
		}
	}
	return false
}

// documentComments finishes the comments of a document node: the comment block
// at the very end of the text is the document's foot when a blank line comes
// before it, and the foot of a key or item otherwise.
func (b *builder) documentComments(d *Node) {
	if !b.cs.on {
		return
	}
	if b.emptyDocumentFoot(d) {
		return
	}
	src := b.ref.Src
	b.endIndicatorComment(d)
	until := directivesStart(src, b.ref.DocStart)
	if firstContent(src) >= lineStart(src, until) {
		// Only the first document of a stream has the comments at the top of the
		// text for its head.
		d.HeadComment = leadingBlock(src)
	}
	end, marked := b.regionEnd()
	for end > 0 && (src[end-1] == '\n' || src[end-1] == '\r' || src[end-1] == ' ' || src[end-1] == '\t') {
		end--
	}
	if b.emptyItemFoot(d, end, marked) {
		return
	}
	var block []string
	bs := lineStart(src, end)
	for {
		l := strings.TrimSpace(string(src[bs:lineEnd(src, bs)]))
		if !strings.HasPrefix(l, "#") {
			break
		}
		block = append([]string{l}, block...)
		if bs == 0 {
			return
		}
		bs = lineStart(src, bs-1)
	}
	if len(block) == 0 {
		return
	}
	// bs is the start of the line above the block; the block starts on the next.
	first := lineEnd(src, bs) + 1
	if marked || strings.TrimSpace(string(src[bs:lineEnd(src, bs)])) == "" {
		// Comments at the end of a document that another document or an end marker
		// follows are the document's foot, however close they sit to the content.
		d.FootComment = strings.Join(block, "\n")
		if marked {
			// Another block above it, set off by a blank line and directly under
			// the content, is the foot comment of that content.
			spans, s2, _ := b.commentBlockAbove(src, first, nil)
			if len(spans) > 0 && s2 > 0 && !b.blankAbove(s2) {
				b.footFor(joinSpans(src, spans), indentOf("", src, s2))
			}
		}
		return
	}
	if bs == 0 && endsInFlow(d) {
		// yaml.v3 gives the comments that follow a flow collection on the very
		// first line of the text to the document, not to the key or item.
		d.FootComment = strings.Join(block, "\n")
		return
	}
	if text := strings.Join(block, "\n"); b.footForAt(text, indentOf(block[0], src, first), true) && d.FootComment == "" {
		// The entry that would take it has a foot comment already; the comment
		// stays with the document rather than being lost.
		d.FootComment = text
	}
}

// emptyItemFoot handles a document whose last entry is a sequence item with no
// value, with comments after it or on its line ("- # c"). yaml.v3 has no node
// to hang them on and makes them, from the one on the dash line to the end of
// the text, the document's foot comment. end is where the document's text ends,
// without trailing blanks. It reports whether it set the foot.
func (b *builder) emptyItemFoot(d *Node, end int, marked bool) bool {
	src := b.ref.Src
	seg := src[:end]
	var tail []string // the lines after the item, last first
	var item []byte
	for {
		nl := bytes.LastIndexByte(seg, '\n')
		l := bytes.TrimSpace(seg[nl+1:])
		switch {
		case len(l) == 0:
			tail = append(tail, "")
		case l[0] == '#' && !b.insideScalar(nl+1+indexNonBlank(seg[nl+1:])):
			tail = append(tail, string(l))
		default:
			item = l
		}
		if item != nil {
			if b.insideScalar(nl + 1 + indexNonBlank(seg[nl+1:])) {
				return false
			}
			break
		}
		if nl < 0 {
			return false
		}
		seg = seg[:nl]
	}
	// Only dashes (and their comment) may be on the line.
	dashes := 0
	for len(item) > 0 && item[0] == '-' && (len(item) == 1 || item[1] == ' ') {
		item = bytes.TrimLeft(item[1:], " ")
		dashes++
	}
	if dashes == 0 || len(item) > 0 && item[0] != '#' {
		return false
	}
	var lines []string
	if len(item) > 0 {
		lines = append(lines, string(item))
	}
	for i := len(tail) - 1; i >= 0; i-- {
		if tail[i] == "" && len(lines) == 0 {
			continue
		}
		lines = append(lines, tail[i])
	}
	if len(lines) == 0 {
		return false
	}
	text := strings.Join(lines, "\n")
	if !marked && bytes.Count(src[end:], []byte("\n")) >= 2 {
		text += "\n"
	}
	d.FootComment = text
	return true
}

// endsInFlow reports whether the last thing in the document is a flow
// collection, reached through the last entry of block collections.
func endsInFlow(d *Node) bool {
	if len(d.Content) == 0 {
		return false
	}
	n := d.Content[0]
	for n != nil && (n.Kind == SequenceNode || n.Kind == MappingNode) {
		if n.Style&FlowStyle != 0 {
			return true
		}
		if len(n.Content) == 0 {
			return false
		}
		n = n.Content[len(n.Content)-1]
	}
	return false
}

// emptyDocumentFoot handles a document that has a "---" marker and no content.
// yaml.v3 has nothing to attach the comments to there, so the comment on the
// marker line and the comment lines after it (blank lines between them kept) all
// become the foot comment of the document node. It reports whether it did so.
func (b *builder) emptyDocumentFoot(d *Node) bool {
	t := b.ref.Tree
	src := b.ref.Src
	if nd := &t.Nodes[b.ref.Node]; nd.Kind != cst.Scalar || nd.Start != nd.End || nd.Prop != 0 {
		return false
	}
	at := b.ref.DocStart
	if at < 0 || at+3 > len(src) || string(src[at:at+3]) != "---" {
		return false
	}
	var lines []string
	pending := 0 // blank lines seen since the last comment line
	// Comment lines before the marker of the first document come first.
	for i := 0; i < at; {
		e := lineEnd(src, i)
		l := bytes.TrimSpace(src[i:e])
		if i == 0 && bytes.HasPrefix(l, []byte("\xEF\xBB\xBF")) {
			l = bytes.TrimSpace(l[3:])
		}
		switch {
		case len(l) == 0:
			pending++
		case l[0] == '#':
			if len(lines) > 0 {
				for ; pending > 0; pending-- {
					lines = append(lines, "")
				}
			}
			pending = 0
			lines = append(lines, string(l))
		default:
			// Another document's text, or a directive, comes before the marker.
			return false
		}
		i = e + 1
	}
	first := true
	for i := at; i < len(src); {
		e := lineEnd(src, i)
		l := bytes.TrimSpace(src[i:e])
		if first {
			// The marker line: only a comment after "---" counts.
			l = bytes.TrimSpace(src[i+3 : e])
			if len(l) > 0 && l[0] != '#' {
				return false
			}
			first = false
		}
		switch {
		case len(l) == 0:
			pending++
		case l[0] == '#':
			if len(lines) > 0 {
				for ; pending > 0; pending-- {
					lines = append(lines, "")
				}
			}
			pending = 0
			lines = append(lines, string(l))
		default:
			// The next document marker or other text ends the region.
			i = len(src)
			continue
		}
		i = e + 1
	}
	if len(lines) == 0 {
		return false
	}
	d.FootComment = strings.Join(lines, "\n")
	if pending > 0 {
		// Blank lines after the last comment end the text with one line break.
		d.FootComment += "\n"
	}
	if len(d.Content) == 1 {
		// The empty root keeps none of them.
		d.Content[0].HeadComment, d.Content[0].LineComment, d.Content[0].FootComment = "", "", ""
	}
	return true
}

// regionEnd returns where the text that belongs to the document ends: the end of
// the comment lines that follow its last node, before the next line that is
// neither blank nor a comment (a document marker, as the document has been read
// to its end). Comments after that line are another document's. marked says
// that such a line was found.
func (b *builder) regionEnd() (end int, marked bool) {
	src := b.ref.Src
	t := b.ref.Tree
	last := 0
	for i := 1; i < len(t.Nodes); i++ {
		nd := &t.Nodes[i]
		if nd.Kind == cst.Scalar && nd.Start == nd.End {
			continue
		}
		if e := int(nd.End); e > last {
			last = e
		}
	}
	if last > len(src) {
		last = len(src)
	}
	end = len(src)
	for i := lineEnd(src, last); i < len(src); {
		// i is at a line break; the next line starts after it.
		ls := i + 1
		if ls >= len(src) {
			break
		}
		le := lineEnd(src, ls)
		t := bytes.TrimSpace(src[ls:le])
		if len(t) != 0 && t[0] != '#' {
			end, marked = ls, true
			break
		}
		i = le
	}
	return end, marked
}

// firstContent returns the offset of the first line that is neither blank nor a
// comment (after a byte order mark).
func firstContent(src []byte) int {
	i := 0
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		i = 3
	}
	for i < len(src) {
		le := lineEnd(src, i)
		if t := bytes.TrimSpace(src[i:le]); len(t) != 0 && t[0] != '#' {
			return i
		}
		i = le + 1
	}
	return len(src)
}

// leadingBlock returns the comment lines at the very start of the text when a
// blank line follows them: the head comment of the document. Comment lines that
// run straight into the first node belong to that node.
func leadingBlock(src []byte) string {
	i := 0
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		i = 3
	}
	var block []string
	for i < len(src) {
		e := lineEnd(src, i)
		l := strings.TrimSpace(string(src[i:e]))
		if !strings.HasPrefix(l, "#") {
			break
		}
		block = append(block, l)
		i = e + 1
	}
	if len(block) == 0 || i >= len(src) || strings.TrimSpace(string(src[i:lineEnd(src, i)])) != "" {
		return ""
	}
	return strings.Join(block, "\n")
}

// addSpan records a multi-line scalar, whose lines may start with "#" without
// being comments. Spans arrive in text order unless a call goes back to an
// earlier part of the document; they are sorted when next searched.
func (sh *buildShared) addSpan(sp [2]int32) {
	if n := len(sh.spans); n > 0 {
		last := sh.spans[n-1]
		if sp[0] == last[0] {
			return
		}
		if sp[0] < last[0] {
			sh.unsorted = true
		}
	}
	sh.spans = append(sh.spans, sp)
}

// dashComment reports whether the trimmed line t is an item with a comment and no
// value ("- # c"), which the rules above leave to the simpler code.
func dashComment(t []byte) bool {
	if len(t) == 0 || t[0] != '-' {
		return false
	}
	for len(t) > 0 && t[0] == '-' && (len(t) == 1 || t[1] == ' ') {
		t = bytes.TrimLeft(t[1:], " ")
	}
	return len(t) > 0 && t[0] == '#'
}

// applyPending hands the comments that wait for the first scalar of a collection
// to n when it is that scalar, keeps them for a collection that is entered, and
// drops them for anything else.
func (b *builder) applyPending(n *Node, nd *cst.Node, collection bool) {
	cs := &b.cs
	if cs.pendHead == "" && cs.pendLine == "" || collection {
		return
	}
	if nd.Kind == cst.Scalar && nd.Start == nd.End {
		// An empty key or value has no place for a line comment. A head comment
		// stays with it: it is the first node of the item, and left waiting it
		// would end up on an unrelated scalar further down.
		if cs.pendHead != "" && n.HeadComment == "" {
			n.HeadComment, cs.pendHead = cs.pendHead, ""
		}
		return
	}
	if flow := nd.Kind != cst.Scalar && nd.Kind != cst.Alias && (nd.Flow || nd.Pair); flow && cs.pendHead != "" {
		// A flow collection is the first node of the item: the head comment is its.
		if n.HeadComment == "" {
			n.HeadComment = cs.pendHead
		} else {
			n.HeadComment = cs.pendHead + "\n" + n.HeadComment
		}
	}
	if nd.Kind == cst.Scalar {
		if cs.pendHead != "" {
			if n.HeadComment == "" {
				n.HeadComment = cs.pendHead
			} else {
				n.HeadComment = cs.pendHead + "\n" + n.HeadComment
			}
		}
		if cs.pendLine != "" && n.LineComment == "" {
			n.LineComment = cs.pendLine
		}
	}
	cs.pendHead, cs.pendLine = "", ""
}

// headerComment returns the comment that ends the line a node's header is on,
// when the node's text starts on a later line: after its tag or anchor, or after
// a "?" or ":" that has nothing else on its line.
func (b *builder) headerComment(t *cst.Tree, c int32, src []byte, collection bool) string {
	nd := &t.Nodes[c]
	if p := t.PropOf(c); p != nil {
		e := -1
		if p.HasAnchor() {
			e = int(p.AnchorEnd)
		}
		if p.HasTag() && int(p.TagEnd) > e {
			e = int(p.TagEnd)
		}
		if e < 0 || !collection {
			return ""
		}
		return lineComment(src, e)
	}
	if nd.Kind == cst.Scalar && nd.Start == nd.End {
		return ""
	}
	start := int(nd.Start)
	// Only blanks (and one ":") may come before the node on its line.
	j := start
	for j > 0 && (src[j-1] == ' ' || src[j-1] == '\t') {
		j--
	}
	if j > 0 && src[j-1] == ':' {
		j--
		for j > 0 && (src[j-1] == ' ' || src[j-1] == '\t') {
			j--
		}
	}
	if j == 0 || src[j-1] != '\n' {
		return ""
	}
	ls := j
	pl := lineStart(src, ls-1)
	l := bytes.TrimSpace(src[pl : ls-1])
	if len(l) < 2 || l[0] != '?' && l[0] != ':' {
		return ""
	}
	if rest := bytes.TrimLeft(l[1:], " \t"); len(rest) < len(l)-1 && len(rest) > 0 && rest[0] == '#' {
		return string(rest)
	}
	return ""
}

// explicitKeyLine reports whether the node that starts at pos is the key of a
// "?" entry that begins its line.
func explicitKeyLine(src []byte, pos int) bool {
	ls := lineStart(src, pos)
	pre := bytes.TrimRight(src[ls:pos], " \t")
	if len(pre) == 0 || pre[len(pre)-1] != '?' {
		return false
	}
	for _, c := range pre[:len(pre)-1] {
		if c != ' ' && c != '\t' && c != '-' {
			return false
		}
	}
	return true
}

// startsLine reports whether only blanks, and perhaps one ":", come before
// offset pos on its line.
func startsLine(src []byte, pos int) bool {
	j := pos
	for j > 0 && (src[j-1] == ' ' || src[j-1] == '\t') {
		j--
	}
	if j > 0 && src[j-1] == ':' {
		j--
		for j > 0 && (src[j-1] == ' ' || src[j-1] == '\t') {
			j--
		}
	}
	return j > 0 && src[j-1] == '\n'
}

// firstOnLineIn is firstOnLine, also letting a mapping value count when only a
// ":" comes before it and a flow entry when only a "," does.
func firstOnLineIn(src []byte, pos int, colon, comma bool) (int, bool) {
	ls := lineStart(src, pos)
	for _, c := range src[ls:pos] {
		if c != ' ' && c != '\t' && c != '-' && c != '?' && !(colon && c == ':') && !(comma && c == ',') {
			return 0, false
		}
	}
	return pos - ls, true
}

// docStartHead returns the head comment of the first node of a document that
// has a "---" marker on the line above the node's comments: the comment lines
// above the marker, the comment on the marker line, and the lines between the
// marker and the node, as one text.
func (b *builder) docStartHead(src []byte, block []span, bstart, blank int) (string, bool) {
	if blank != 0 || bstart <= 0 {
		return "", false
	}
	pl := lineStart(src, bstart-1)
	t := bytes.TrimSpace(src[pl : bstart-1])
	if !bytes.HasPrefix(t, []byte("---")) || len(t) > 3 && t[3] != ' ' && t[3] != '\t' {
		return "", false
	}
	rest := bytes.TrimSpace(t[3:])
	for len(rest) > 0 && (rest[0] == '!' || rest[0] == '&') {
		j := bytes.IndexAny(rest, " \t")
		if j < 0 {
			rest = nil
			break
		}
		rest = bytes.TrimSpace(rest[j:])
	}
	if len(rest) > 0 && rest[0] != '#' {
		return "", false
	}
	var parts []string
	if above, _, abBlank := b.commentBlockAbove(src, pl, nil); len(above) > 0 && abBlank == 0 {
		parts = append(parts, joinSpans(src, above))
	}
	if len(rest) > 0 {
		parts = append(parts, string(rest))
	}
	if len(block) > 0 {
		parts = append(parts, joinSpans(src, block))
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n"), true
}

// endIndicatorComment handles a text that ends in a "?" or ":" with a comment and
// nothing after it: yaml.v3 has no node to give the comment to and puts it on
// the mapping that holds the entry.
func (b *builder) endIndicatorComment(d *Node) {
	src := bytes.TrimRight(b.ref.Src, " \t\r\n")
	l := bytes.TrimSpace(src[lineStart(src, len(src)):])
	if len(l) < 2 || l[0] != '?' && l[0] != ':' || len(d.Content) != 1 || d.Content[0].Kind != MappingNode {
		return
	}
	if rest := bytes.TrimLeft(l[1:], " \t"); len(rest) < len(l)-1 && len(rest) > 0 && rest[0] == '#' && d.Content[0].LineComment == "" {
		d.Content[0].LineComment = string(rest)
	}
}

// flowFoot returns the comment lines that stand alone between the last entry of
// the flow collection nd and its closing bracket, blank lines inside the block
// kept. yaml.v3 gives them to the last key or item as its foot comment.
func (b *builder) flowFoot(t *cst.Tree, nd *cst.Node) string {
	if !b.cs.on || !nd.Flow || nd.End < 2 {
		return ""
	}
	src := b.ref.Src
	p := int(nd.End) - 1
	if p >= len(src) || (src[p] != ']' && src[p] != '}') {
		return ""
	}
	ls := lineStart(src, p)
	if len(bytes.TrimSpace(src[ls:p])) != 0 || ls == 0 {
		return ""
	}
	var lines []string
	for i := ls; i > 0; {
		ps := lineStart(src, i-1)
		l := bytes.TrimSpace(src[ps : i-1])
		if len(l) == 0 {
			lines = append(lines, "")
		} else if l[0] == '#' && !b.insideScalar(ps+indexNonBlank(src[ps:i-1])) {
			lines = append(lines, string(l))
		} else {
			break
		}
		i = ps
	}
	// Collected bottom-up; drop the blank lines at either end and reverse.
	// A blank line before the bracket stays as one newline at the end of the text.
	tail := ""
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
		tail = "\n"
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	for x, y := 0, len(lines)-1; x < y; x, y = x+1, y-1 {
		lines[x], lines[y] = lines[y], lines[x]
	}
	return strings.Join(lines, "\n") + tail
}
