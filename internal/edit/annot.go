package edit

import (
	"strings"
	"unicode/utf8"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/scan"
)

// Errors of the key and comment edits. Match with errors.Is.
var (
	// ErrKeyExists: a rename would give two entries the same key.
	ErrKeyExists = wrapSentinel("key already exists")
	// ErrNotEntry: the pointer does not address a mapping entry.
	ErrNotEntry = wrapSentinel("not a mapping entry")
	// ErrBadComment: the comment text cannot be written as a comment.
	ErrBadComment = wrapSentinel("invalid comment text")
)

type sentinel struct{ msg string }

func (e *sentinel) Error() string { return e.msg }

func wrapSentinel(msg string) error { return &sentinel{msg} }

// Splice is the one replacement of a key or comment edit: the bytes [At, End) of
// the old text become NewLen bytes.
type Splice struct{ At, End, NewLen int }

// SameShape reports whether nt, parsed from the text after the splice sp, is old
// with nothing but that splice applied: the same nodes with the same links, kinds,
// styles and properties, and every offset where the splice puts it. Node skip (0
// for none) is the one that holds the replaced text; only its end moves freely.
// Together with an unchanged text outside the splice this shows that no value
// changed.
func SameShape(old, nt *cst.Tree, sp Splice, skip int32) bool {
	if len(old.Nodes) != len(nt.Nodes) || len(old.Props) != len(nt.Props) || len(old.Docs) != len(nt.Docs) {
		return false
	}
	delta := int32(sp.NewLen - (sp.End - sp.At))
	at, end := int32(sp.At), int32(sp.End)
	moved := func(o, n int32) bool {
		switch {
		case o <= at && n == o:
			return true
		case o >= end && n == o+delta:
			return true
		}
		return false
	}
	for i := 1; i < len(old.Nodes); i++ {
		a, b := &old.Nodes[i], &nt.Nodes[i]
		// The replaced key may change style (plain to quoted).
		if a.Kind != b.Kind || (a.Style != b.Style && int32(i) != skip) || a.Flow != b.Flow || a.Pair != b.Pair ||
			a.Parent != b.Parent || a.Next != b.Next || (a.Prop == 0) != (b.Prop == 0) || a.Indent != b.Indent {
			return false
		}
		if int32(i) == skip {
			if b.Start != a.Start && !moved(a.Start, b.Start) {
				return false
			}
			continue
		}
		if a.Start == a.End {
			// An empty scalar's offset has no meaning; it only has to be empty again.
			if b.Start != b.End {
				return false
			}
			continue
		}
		if !moved(a.Start, b.Start) || !moved(a.End, b.End) {
			return false
		}
	}
	for i := 1; i < len(old.Props); i++ {
		a, b := old.Props[i], nt.Props[i]
		for _, p := range [][2]int32{{a.AnchorStart, b.AnchorStart}, {a.AnchorEnd, b.AnchorEnd}, {a.TagStart, b.TagStart}, {a.TagEnd, b.TagEnd}} {
			if p[0] != p[1] && !moved(p[0], p[1]) {
				return false
			}
		}
	}
	for i := range old.Docs {
		a, b := &old.Docs[i], &nt.Docs[i]
		if a.Root != b.Root || a.ExplicitStart != b.ExplicitStart || a.ExplicitEnd != b.ExplicitEnd ||
			a.VersionMajor != b.VersionMajor || a.VersionMinor != b.VersionMinor {
			return false
		}
		if a.ExplicitStart && (!moved(a.StartMarker[0], b.StartMarker[0]) || !moved(a.StartMarker[1], b.StartMarker[1])) {
			return false
		}
		if a.ExplicitEnd && (!moved(a.EndMarker[0], b.EndMarker[0]) || !moved(a.EndMarker[1], b.EndMarker[1])) {
			return false
		}
	}
	return true
}

// insideScalar reports whether offset at lies strictly inside the text of a
// scalar other than skip: text put there would become part of the scalar.
//
// The non-empty scalars of a document lie in the node array in text order, one
// after the other, so the one that can hold at is the last that starts before it,
// and a binary search finds it. Collections and empty scalars lie between them;
// when more than probeSpan of them follow each other the search gives up and reads
// every node.
func (c *Ctx) insideScalar(at int, skip int32) bool {
	const probeSpan = 64
	nodes := c.T.Nodes
	text := func(i int) bool { return nodes[i].Kind == cst.Scalar && nodes[i].Start < nodes[i].End }
	lo, hi := 1, len(nodes)
	for lo < hi {
		mid := lo + (hi-lo)/2
		j := mid
		for j < hi && j < mid+probeSpan && !text(j) {
			j++
		}
		if j >= hi || j >= mid+probeSpan {
			if j >= hi && j < mid+probeSpan {
				hi = mid // no scalar from mid on: the search is in the lower half
				continue
			}
			return c.insideScalarAll(at, skip)
		}
		if int(nodes[j].Start) < at {
			lo = j + 1
		} else {
			hi = mid
		}
	}
	// The last scalar that starts before at is the last one before lo.
	n := 0
	for i := lo - 1; i >= 1; i-- {
		if text(i) {
			return int32(i) != skip && int(nodes[i].Start) < at && at < int(nodes[i].End)
		}
		if n++; n > probeSpan {
			return c.insideScalarAll(at, skip)
		}
	}
	return false
}

// insideScalarAll is insideScalar by reading every node.
func (c *Ctx) insideScalarAll(at int, skip int32) bool {
	for i := 1; i < len(c.T.Nodes); i++ {
		nd := &c.T.Nodes[i]
		if nd.Kind == cst.Scalar && int32(i) != skip && int(nd.Start) < at && at < int(nd.End) {
			return true
		}
	}
	return false
}

// CheckPatchTree reports what text of a patch written in YAML may not contain,
// though the YAML reader would accept it: a scalar the decoder refuses to read
// (a quoted escape that is not a Unicode scalar value, for one), and a tag outside
// the core schema, which JSON has no counterpart for and which a patch would drop. The error names
// the position when it has one.
func CheckPatchTree(t *cst.Tree, src []byte) error {
	var buf []byte
	for i := 1; i < len(t.Nodes); i++ {
		nd := &t.Nodes[i]
		if nd.Kind == cst.Scalar {
			ev := parse.Event{Kind: parse.Scalar, Style: nd.Style, Start: int(nd.Start), End: int(nd.End), Indent: int(nd.Indent)}
			var err error
			if buf, err = parse.AppendScalar(buf[:0], src, &ev); err != nil {
				return err
			}
		}
		if p := t.PropOf(int32(i)); p != nil && p.HasTag() {
			// The non-specific tag "!" only makes a plain scalar a string, so it is
			// not a tag outside the core schema.
			if c := CanonTag(t, src, int32(i)); c != "!" && !strings.HasPrefix(c, "!!") {
				return scan.NewError(src, int(p.TagStart), scan.Syntax, "a patch may not use a tag outside the core schema: "+string(src[p.TagStart:p.TagEnd]))
			}
		}
	}
	return nil
}

// ---- rename ----

// maxRenameKey is the longest key a rename writes: longer keys need the explicit
// form, which a rename does not produce.
const maxRenameKey = 1000

// Rename plans the replacement of the key text of the mapping entry at loc by
// newKey. The key keeps its style when that style can hold the new text. safe
// writes the key double-quoted. The returned Splice is the one replacement, and
// key is the node that holds it.
func (c *Ctx) Rename(loc Loc, newKey string, safe bool) (repls []Repl, sp Splice, key int32, err error) {
	if loc.Node == 0 || loc.Parent == 0 || loc.Key == 0 || c.T.Nodes[loc.Parent].Kind != cst.Mapping {
		return nil, sp, 0, wrap(ErrNotEntry, "the pointer does not address a mapping entry")
	}
	kn := &c.T.Nodes[loc.Key]
	switch {
	case kn.Kind == cst.Alias:
		return nil, sp, 0, wrap(ErrUnsupported, "the key is an alias")
	case kn.Kind != cst.Scalar:
		return nil, sp, 0, wrap(ErrUnsupported, "the key is a collection")
	case kn.Style == scan.Literal || kn.Style == scan.Folded:
		return nil, sp, 0, wrap(ErrUnsupported, "the key is a block scalar")
	case kn.Start == kn.End:
		return nil, sp, 0, wrap(ErrUnsupported, "the key is empty")
	}
	if !utf8.ValidString(newKey) {
		return nil, sp, 0, wrap(ErrUnsupported, "the new key is not valid UTF-8")
	}
	if len(newKey) > maxRenameKey {
		return nil, sp, 0, wrap(ErrUnsupported, "the new key is longer than %d bytes", maxRenameKey)
	}
	if kn.Style == scan.Plain && string(c.Src[kn.Start:kn.End]) == "<<" {
		return nil, sp, 0, wrap(ErrUnsupported, "the key is a merge key")
	}
	found, _, amb := c.findKeyLight(loc.Parent, newKey)
	if amb || (found != 0 && found != loc.Node) {
		return nil, sp, 0, wrap(ErrKeyExists, "%q is already a key of the mapping", newKey)
	}
	if found == loc.Node {
		return nil, sp, 0, nil // the key already reads as newKey
	}
	if err := c.anchorsInUse(loc.Key, true); err != nil {
		// An alias of the key's anchor would read the new text too.
		return nil, sp, 0, err
	}
	if p := c.T.PropOf(loc.Key); p != nil && p.HasTag() {
		if !tagKeeps(CanonTag(c.T, c.Src, loc.Key), &Value{Kind: String, Text: newKey}, cst.Scalar) {
			return nil, sp, 0, wrap(ErrUnsupported, "the tag of the key does not fit the new key")
		}
	}
	par := &c.T.Nodes[loc.Parent]
	l := &layout{safe: safe, yaml11: c.YAML11}
	h := kn.Style
	if h != scan.SingleQuoted && h != scan.DoubleQuoted {
		h = scan.Plain
	}
	text := l.stringText(nil, newKey, h, par.Flow || par.Pair, true)
	sp = Splice{At: int(kn.Start), End: int(kn.End), NewLen: len(text)}
	if c.insideScalar(sp.At, loc.Key) {
		return nil, sp, 0, wrap(ErrUnsupported, "the key lies inside another scalar")
	}
	return []Repl{{Start: sp.At, End: sp.End, Text: text}}, sp, loc.Key, nil
}

// ---- comments ----

// CommentLines turns comment text into the lines to write: each line gets a "#"
// when it has none, lines are trimmed at the end, and a blank line inside the text
// becomes a bare "#". It returns nil for the empty text, and an error for text that
// is not valid UTF-8 or holds a character that cannot be part of a comment.
func CommentLines(text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	text = strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
	if !utf8.ValidString(text) {
		return nil, wrap(ErrBadComment, "not valid UTF-8")
	}
	var out []string
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimRight(strings.TrimSuffix(ln, "\r"), " \t")
		ln = strings.TrimLeft(ln, " \t")
		for _, r := range ln {
			if r == '\r' || r == 0x85 || r == 0x2028 || r == 0x2029 || (r < 0x20 && r != '\t') || r == 0x7f || (r >= 0x80 && r < 0xA0) || r == 0xFEFF || r == 0xFFFE || r == 0xFFFF {
				return nil, wrap(ErrBadComment, "a comment cannot hold the character U+%04X", r)
			}
		}
		switch {
		case ln == "":
			ln = "#"
		case ln[0] != '#':
			ln = "# " + ln
		}
		out = append(out, ln)
	}
	return out, nil
}

// TrimComment returns comment text as read: each line without its indentation
// and trailing blanks, joined by line feeds.
func TrimComment(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.Split(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"), "\n")
	out := parts[:0]
	for _, p := range parts {
		out = append(out, strings.Trim(p, " \t\r"))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// docFloor is the offset before which the text is not the document's own: lines
// above it cannot be a head comment of the root.
func (c *Ctx) docFloor() int {
	if d := &c.T.Docs[c.Doc]; d.ExplicitStart {
		return int(d.StartMarker[1])
	}
	return int(c.bom)
}

// HeadComment plans the replacement of the comment lines above the entry that
// node n belongs to by lines (none removes them). The new lines have the entry's
// indentation. It returns no replacement when the comment already reads so.
func (c *Ctx) HeadComment(n int32, lines []string) ([]Repl, Splice, error) {
	var sp Splice
	from, ls, ok := c.headSpan(n)
	if !ok {
		return nil, sp, wrap(ErrUnsupported, "the entry shares its line with another or has no lines of its own")
	}
	first := n
	if nd := &c.T.Nodes[n]; nd.Parent != 0 && c.T.Nodes[nd.Parent].Kind == cst.Mapping && !c.T.IsKey(n) {
		first = c.T.Prev(n)
	}
	s0 := c.entryStart(first)
	if nd := &c.T.Nodes[n]; nd.Parent == 0 {
		s0 = int(c.T.FullStart(n))
	}
	indent := c.Src[ls:s0]
	nl := c.nlAt(max(s0-1, 0))
	var b strings.Builder
	for _, ln := range lines {
		b.Write(indent)
		b.WriteString(ln)
		b.WriteString(nl)
	}
	text := b.String()
	if string(c.Src[from:ls]) == text {
		return nil, sp, nil
	}
	sp = Splice{At: from, End: ls, NewLen: len(text)}
	if c.insideScalar(from, 0) || (ls > from && c.insideScalar(ls, 0)) {
		return nil, sp, wrap(ErrUnsupported, "the place lies inside a scalar")
	}
	return []Repl{{Start: from, End: ls, Text: []byte(text)}}, sp, nil
}

// headSpan returns the span of the head comment lines of the entry of node n, as
// HeadCommentSpan, and ls, the start of the entry's own line. ok is false when the
// entry has no line of its own.
func (c *Ctx) headSpan(n int32) (from, ls int, ok bool) {
	nd := &c.T.Nodes[n]
	first := n
	var s0 int
	switch {
	case nd.Parent == 0:
		if nd.Kind == cst.Scalar && nd.Start == nd.End {
			return 0, 0, false
		}
		s0 = int(c.T.FullStart(n))
	default:
		par := &c.T.Nodes[nd.Parent]
		if par.Kind == cst.Mapping && !c.T.IsKey(n) {
			first = c.T.Prev(n)
		}
		if par.Flow || par.Pair {
			return 0, 0, false
		}
		s0 = c.entryStart(first)
	}
	if !c.onlyBlankBefore(s0) {
		return 0, 0, false
	}
	ls = c.lineStart(s0)
	floor := c.docFloor()
	if nd.Parent != 0 {
		floor = c.floorOf(first)
	}
	return c.ownedStart(ls, floor), ls, true
}

// lineCommentBase returns the offset after which the line comment of node n may
// stand, and whether n can have one. Scalars have it after their text (a block
// scalar after its header), a block collection after the indicator and properties
// that introduce it when its content starts on a later line, and an empty value
// after its colon or dash.
func (c *Ctx) lineCommentBase(n int32) (int, bool) {
	nd := &c.T.Nodes[n]
	if nd.Parent != 0 {
		if par := &c.T.Nodes[nd.Parent]; par.Flow || par.Pair {
			if nd.Kind == cst.Scalar && nd.Start == nd.End {
				return 0, false
			}
			return int(nd.End), true
		}
	}
	switch {
	case isBlockScalarNode(nd):
		i := int(nd.Start) + 1
		for i < len(c.Src) && (c.Src[i] == '-' || c.Src[i] == '+' || c.Src[i] >= '1' && c.Src[i] <= '9') {
			i++
		}
		return i, true
	case (nd.Kind == cst.Mapping || nd.Kind == cst.Sequence) && !nd.Flow:
		if nd.Parent == 0 {
			return 0, false
		}
		p := c.hdrEnd(n)
		if c.lineEnd(p) >= int(nd.Start) {
			return 0, false
		}
		return p, true
	case nd.Kind == cst.Scalar && nd.Start == nd.End:
		if nd.Parent == 0 {
			return 0, false
		}
		par := &c.T.Nodes[nd.Parent]
		if par.Kind == cst.Mapping && !c.hasColon(n) {
			return 0, false
		}
		return c.hdrEnd(n), true
	}
	return int(nd.End), true
}

// LineComment plans the replacement of the comment that follows node n on its
// line by text, which is a whole comment ("# ..."); empty removes it.
func (c *Ctx) LineComment(n int32, comment string) ([]Repl, Splice, error) {
	var sp Splice
	p, ok := c.lineCommentBase(n)
	if !ok {
		return nil, sp, wrap(ErrUnsupported, "this node has no place for a comment after it on its line")
	}
	nd := &c.T.Nodes[n]
	q := p
	for q < len(c.Src) && (c.Src[q] == ' ' || c.Src[q] == '\t') {
		q++
	}
	hasComment := q < len(c.Src) && c.Src[q] == '#'
	switch {
	case hasComment:
		e := c.lineEnd(q)
		cur := strings.TrimRight(string(c.Src[q:e]), " \t\r")
		switch {
		case comment == "":
			sp = Splice{At: p, End: e, NewLen: 0}
			return []Repl{{Start: p, End: e}}, sp, nil
		case cur == comment:
			return nil, sp, nil
		}
		sp = Splice{At: q, End: e, NewLen: len(comment)}
		return []Repl{{Start: q, End: e, Text: []byte(comment)}}, sp, nil
	case comment == "":
		return nil, sp, nil
	}
	if q < len(c.Src) && !isBreak(c.Src[q]) {
		return nil, sp, wrap(ErrUnsupported, "other text follows the value on its line")
	}
	if nd.Kind == cst.Scalar && nd.Start != nd.End && !isBlockScalarNode(nd) && p == int(nd.End) && c.insideScalar(p, 0) {
		return nil, sp, wrap(ErrUnsupported, "the place lies inside a scalar")
	}
	text := " " + comment
	sp = Splice{At: p, End: q, NewLen: len(text)}
	return []Repl{{Start: p, End: q, Text: []byte(text)}}, sp, nil
}

// SameRegionShape is SameShape for the tree of one region: nt holds only the run
// of children of container cn that the old nodes lo..hi of old make up, as the
// children of its root node 1, with offsets that are shift less than those of the
// stream after the splice sp. The run must come out as it was, node for node, with
// every link and offset where the splice puts it. Node skip (0 for none) is the one
// that holds the replaced text. The nodes outside the run are not read: a splice
// inside the run does not touch them.
func SameRegionShape(old *cst.Tree, lo, hi, cn int32, nt *cst.Tree, shift int32, sp Splice, skip int32) bool {
	if lo < 2 || hi < lo || int(hi) >= len(old.Nodes) || len(nt.Nodes) < 2 || int32(len(nt.Nodes))-2 != hi-lo+1 {
		return false
	}
	delta := int32(sp.NewLen - (sp.End - sp.At))
	at, end := int32(sp.At), int32(sp.End)
	moved := func(o, n int32) bool {
		n += shift
		switch {
		case o <= at && n == o:
			return true
		case o >= end && n == o+delta:
			return true
		}
		return false
	}
	idx := 2 - lo // old node number plus idx is the node number in nt
	props := 0
	for i := lo; i <= hi; i++ {
		a, b := &old.Nodes[i], &nt.Nodes[i+idx]
		if a.Kind != b.Kind || (a.Style != b.Style && i != skip) || a.Flow != b.Flow || a.Pair != b.Pair ||
			a.Indent != b.Indent || (a.Prop == 0) != (b.Prop == 0) {
			return false
		}
		if a.Parent == cn {
			if b.Parent != 1 {
				return false
			}
		} else if a.Parent < lo || b.Parent != a.Parent+idx {
			return false
		}
		switch {
		case a.Next == 0:
			if b.Next != 0 {
				return false
			}
		case a.Next > hi:
			// The last child of the run: the old link leaves the run, the new one ends.
			if a.Parent != cn || b.Next != 0 {
				return false
			}
		case b.Next != a.Next+idx:
			return false
		}
		if i == skip {
			if b.Start+shift != a.Start && !moved(a.Start, b.Start) {
				return false
			}
		} else if a.Start == a.End {
			// An empty scalar's offset has no meaning; it only has to be empty again.
			if b.Start != b.End {
				return false
			}
		} else if !moved(a.Start, b.Start) || !moved(a.End, b.End) {
			return false
		}
		if a.Prop != 0 {
			props++
			pa, pb := old.Props[a.Prop], nt.Props[b.Prop]
			for _, p := range [][2]int32{{pa.AnchorStart, pb.AnchorStart}, {pa.AnchorEnd, pb.AnchorEnd}, {pa.TagStart, pb.TagStart}, {pa.TagEnd, pb.TagEnd}} {
				if (p[0] != 0 || p[1] != 0) && p[0] != p[1]+shift && !moved(p[0], p[1]) {
					return false
				}
			}
		}
	}
	return len(nt.Props)-1 == props
}
