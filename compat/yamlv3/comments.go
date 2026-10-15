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
	col int
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
	b.cs.recent = make([]recent, 0, len(t.Nodes)/2)
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

// firstOnLine reports whether only blanks and the indicators "-" and "?" come
// before offset pos on its line, and returns the column of pos in bytes.
func firstOnLine(src []byte, pos int) (int, bool) {
	ls := lineStart(src, pos)
	for _, c := range src[ls:pos] {
		if c != ' ' && c != '\t' && c != '-' && c != '?' {
			return 0, false
		}
	}
	return pos - ls, true
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
		if len(t) > 0 && t[0] == '-' && !bytes.HasPrefix(t, []byte("---")) {
			// An item with no value and a comment, "- # c": yaml.v3 hands the
			// comment to whatever comes next, as if it stood on a line of its own.
			t = bytes.TrimLeft(t, "- 	")
		}
		if len(t) == 0 || t[0] != '#' || b.insideScalar(ps) {
			break
		}
		lead := cap(l) - cap(t)
		block = append(block, span{ps + lead, ps + lead + len(t)})
		i = ps
		start = i
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
	if (r == roleKey || r == roleItem || r == roleRoot) && (!collection || r == roleItem) {
		col, ok := firstOnLine(src, start)
		if ls := lineStart(src, start); ok && ls+1 != b.cs.headLine {
			b.cs.headLine = ls + 1
			if !b.cs.seenToken && ls > 0 {
				// Comments before the "---" that starts the first document belong to
				// the first node, as if the marker were not there.
				if pl := lineStart(src, ls-1); string(bytes.TrimSpace(src[pl:ls-1])) == "---" {
					ls = pl
				}
			}
			block, bstart, blank := b.commentBlockAbove(src, ls, b.cs.buf)
			b.cs.buf = block
			if len(block) > 0 {
				switch {
				case blank == 0 && b.cs.seenToken && bstart > 0 && indentOf("", src, bstart) > col && !b.blankAbove(bstart):
					// The comment is indented deeper than the node after it, so it ends
					// the collection before: it is a foot comment.
					b.footFor(joinSpans(src, block), indentOf("", src, bstart))
				case blank == 0:
					n.HeadComment = joinSpans(src, block)
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
			if len(block) > 0 && bstart > 0 && b.cs.seenToken && b.blankAbove(bstart) {
				// Another block above, set off by a blank line and directly under the
				// previous content, is that content's foot comment.
				block2, bstart2, _ := b.commentBlockAbove(src, bstart, nil)
				if len(block2) > 0 && bstart2 > 0 && !b.blankAbove(bstart2) {
					b.footFor(joinSpans(src, block2), indentOf("", src, bstart2))
				}
			}
			if r == roleItem {
				// An item is indented to its dash, not to the text after it.
				col = indentOf("", src, lineStart(src, start))
			}
			b.cs.recent = append(b.cs.recent, recent{n: n, col: col})
		}
	}
	if !collection {
		b.cs.seenToken = true
	}
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
// indented no deeper than the comment.
func (b *builder) footFor(text string, col int) {
	for i := len(b.cs.recent) - 1; i >= 0; i-- {
		if b.cs.recent[i].col <= col {
			n := b.cs.recent[i].n
			if n.FootComment == "" {
				n.FootComment = text
			}
			return
		}
	}
}

// documentComments finishes the comments of a document node: the comment block
// at the very end of the text is the document's foot when a blank line comes
// before it, and the foot of a key or item otherwise.
func (b *builder) documentComments(d *Node) {
	if !b.cs.on {
		return
	}
	src := b.ref.Src
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
	b.footFor(strings.Join(block, "\n"), indentOf(block[0], src, first))
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
