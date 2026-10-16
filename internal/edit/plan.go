package edit

import (
	"bytes"
	"sort"
	"strings"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/scan"
)

// Repl replaces Src[Start:End] with Text. A zero-width Repl is an insertion.
type Repl struct {
	Start, End int
	Text       []byte
}

// Apply returns src with repls applied. repls may be in any order but must not
// overlap; insertions at the same offset keep their order.
func Apply(dst, src []byte, repls []Repl) ([]byte, error) {
	sort.SliceStable(repls, func(i, j int) bool { return repls[i].Start < repls[j].Start })
	pos := 0
	for _, r := range repls {
		if r.Start < pos || r.End < r.Start || r.End > len(src) {
			return nil, ErrUnsupported
		}
		dst = append(dst, src[pos:r.Start]...)
		dst = append(dst, r.Text...)
		pos = r.End
	}
	return append(dst, src[pos:]...), nil
}

// Options select how an edit is written.
type Options struct {
	// Safe writes strings double-quoted and collections in flow style: the
	// plainest forms that always hold the value. The caller retries with it when
	// the first rendering did not read back exactly.
	Safe bool
}

// ---- layout discovery ----

func (c *Ctx) parentFlow(n int32) bool {
	p := c.T.Nodes[n].Parent
	return p != 0 && c.T.Nodes[p].Flow
}

func (c *Ctx) newLayout(at int, opt Options) *layout {
	return &layout{nl: c.nlNear(at), step: 2, seqOff: 2, gap: 1, safe: opt.Safe, yaml11: c.YAML11}
}

// nlWindow bounds how far nlNear reads for a line break.
const nlWindow = 1024

// nlNear is nlAt for a layout: the line break that ends the line holding i. A
// line that goes on for longer than nlWindow bytes (a flow collection written on
// one line) uses the document's own break, so that planning an edit does not read
// the line again from its start, once per edit, however long the line is.
func (c *Ctx) nlNear(i int) string {
	end := min(i+nlWindow, len(c.Src))
	for j := i; j < end; j++ {
		if isBreak(c.Src[j]) {
			return c.nlAt(i)
		}
	}
	if end >= len(c.Src) {
		return c.nlAt(i)
	}
	return c.firstNL()
}

// discover learns the indentation of nested block collections from the
// siblings in container par, or failing that from the first nested collection of
// the document, so that a new nested collection copies the layout.
func (c *Ctx) discover(l *layout, par int32) {
	// In a large mapping that is being edited again and again the answer is kept
	// with the key index, and is as valid as the index is.
	slot := c.memoSlot(par)
	if slot != nil && slot.lay.ok {
		l.step, l.seqOff = slot.lay.step, slot.lay.seqOff
		return
	}
	defer func() {
		if slot != nil {
			slot.lay = layoutMemo{ok: true, step: l.step, seqOff: l.seqOff}
		}
	}()
	foundStep, foundSeq := false, false
	// A bound on the nodes and entries read, so that an add into a container with
	// no nested collection costs the same however large the container is.
	budget := discoverBudget
	probe := func(k, v int32) {
		vn := &c.T.Nodes[v]
		if vn.Flow || c.T.First(v) == 0 {
			return
		}
		kc := c.col(c.entryStart(k))
		switch vn.Kind {
		case cst.Mapping:
			if !foundStep {
				if d := c.firstEntryCol(k) - kc; d > 0 {
					l.step, foundStep = d, true
				}
			}
		case cst.Sequence:
			if !foundSeq {
				if d := c.col(c.dashOf(c.T.First(v))) - kc; d >= 0 {
					l.seqOff, foundSeq = d, true
				}
			}
		}
	}
	look := func(m int32) {
		nd := &c.T.Nodes[m]
		if nd.Kind != cst.Mapping || nd.Flow {
			return
		}
		// The first entries, then the nodes at the end of the mapping, where a new
		// entry goes: a nested collection near either tells the layout.
		seen := 0
		for k := c.T.First(m); k != 0 && !(foundStep && foundSeq) && seen < discoverEntries && budget > 0; seen++ {
			v := c.T.Nodes[k].Next
			if v == 0 {
				break
			}
			probe(k, v)
			k = c.T.Nodes[v].Next
			budget--
		}
		if foundStep && foundSeq || seen < discoverEntries {
			return
		}
		end := c.T.SubtreeEnd(m)
		for x := end; x > m && end-x < discoverTail && !(foundStep && foundSeq) && budget > 0; x-- {
			budget--
			xn := &c.T.Nodes[x]
			if xn.Parent == m && (xn.Kind == cst.Mapping || xn.Kind == cst.Sequence) {
				if k := c.T.Prev(x); k != 0 {
					probe(k, x)
				}
			}
		}
	}
	if par != 0 {
		look(par)
		if !foundStep {
			// The container's own step under its key beats the step of some
			// earlier mapping elsewhere in the document.
			if d := c.ownStep(par); d > 0 {
				l.step, foundStep = d, true
			}
		}
	}
	if !foundStep || !foundSeq {
		// Fall back to the first nested collection near the start of the document.
		lo := int(c.Root())
		hi := min(len(c.T.Nodes), lo+discoverHead)
		for i := lo; i < hi && !(foundStep && foundSeq) && budget > 0; i++ {
			if n := &c.T.Nodes[i]; n.Kind != cst.Mapping || n.Flow || int32(i) == par {
				continue // not a mapping to read, or read above
			}
			budget--
			look(int32(i))
		}
	}
	// A kind with no sibling to copy takes the step of the other kind, so a document
	// nested by four spaces gets its new sequences four spaces in; with neither,
	// both stay at two.
	switch {
	case foundStep && !foundSeq:
		l.seqOff = l.step
	case foundSeq && !foundStep && l.seqOff > 0:
		l.step = l.seqOff
	}
}

// ownStep returns how far block mapping m is indented past the key it is the
// value of, or 0 when it is not the value of a key in a block mapping (the
// root, a sequence item, a flow collection).
func (c *Ctx) ownStep(m int32) int {
	nd := &c.T.Nodes[m]
	if nd.Kind != cst.Mapping || nd.Flow || c.T.First(m) == 0 {
		return 0
	}
	k := c.T.Prev(m)
	if k == 0 || c.T.Nodes[nd.Parent].Kind != cst.Mapping || c.T.Nodes[nd.Parent].Flow {
		return 0
	}
	return max(0, c.firstEntryCol(k)-c.col(c.entryStart(k)))
}

// firstEntryCol returns the column where the first entry of the block mapping that is
// the value of key k, begins: that of its "?" when the entry has one, even when the
// key's content starts on a later line, else that of the key.
func (c *Ctx) firstEntryCol(k int32) int {
	i := cst.SkipTrivia(c.Src, int(c.T.Nodes[k].End))
	if i < len(c.Src) && c.Src[i] == ':' {
		i = cst.SkipTrivia(c.Src, i+1)
		if i < len(c.Src) && c.Src[i] == '?' && (i+1 == len(c.Src) || c.Src[i+1] == ' ' || c.Src[i+1] == '\t' || isBreak(c.Src[i+1])) {
			return c.col(i)
		}
	}
	return c.col(int(c.T.FullStart(c.T.Nodes[k].Next + 1)))
}

const (
	// discoverEntries is how many entries from the start of a mapping discover reads.
	discoverEntries = 256
	// discoverTail is how many nodes from the end of a mapping it reads.
	discoverTail = 256
	// discoverHead is how many nodes from the root the fallback looks at.
	discoverHead = 1024
	// discoverBudget bounds all the reading of one call.
	discoverBudget = 8192
)

// hintReach is how many siblings on each side of the insertion point styleHint
// reads. The style is that of the nearest string scalar; a container where none of
// the neighbours is a string gets plain scalars, and the cost of an add does not
// grow with the size of the container.
const hintReach = 64

// styleHint returns the quote style a new string copies from the scalar siblings
// around position idx of container par: that of the nearest preceding string
// scalar, else of the nearest following one. In a mapping the values count.
func (c *Ctx) styleHint(par int32, idx int) scan.Style {
	if s, ok := c.nearHint(par, idx); ok {
		return s
	}
	// Nothing before idx within reach: look at the entries from idx on.
	mapping := c.T.Nodes[par].Kind == cst.Mapping
	if n := c.T.Count(par); idx >= n || (mapping && idx >= n/2) {
		return scan.Plain // an add at the end has no entry after it
	}
	ch := c.T.First(par)
	for i := 0; i < idx && ch != 0; i++ {
		ch = c.T.Nodes[ch].Next
		if mapping && ch != 0 {
			ch = c.T.Nodes[ch].Next
		}
	}
	for i := 0; i < hintReach && ch != 0; i++ {
		v := ch
		if mapping {
			if v = c.T.Nodes[ch].Next; v == 0 {
				break
			}
		}
		if s, ok := c.stringStyle(v); ok {
			return s
		}
		ch = c.T.Nodes[v].Next
	}
	return scan.Plain
}

// stringStyle returns the style of node n if it is a string scalar.
func (c *Ctx) stringStyle(n int32) (scan.Style, bool) {
	nd := &c.T.Nodes[n]
	if nd.Kind != cst.Scalar {
		return 0, false
	}
	switch nd.Style {
	case scan.SingleQuoted, scan.DoubleQuoted:
		return nd.Style, true
	case scan.Plain:
		c.hintReads++
		if nd.Prop == 0 {
			// Most plain scalars are told apart by their first byte: a word that does
			// not start like true, false or null is a string, and a run of digits is
			// an integer. Anything else is resolved in full.
			raw := c.Src[nd.Start:nd.End]
			if len(raw) > 0 && len(raw) <= 18 {
				switch ch := raw[0]; {
				case ch >= '0' && ch <= '9':
					digits := true
					for _, b := range raw {
						if b < '0' || b > '9' {
							digits = false
							break
						}
					}
					if digits {
						return 0, false
					}
				case (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z'):
					switch ch {
					case 't', 'T', 'f', 'F', 'n', 'N':
					default:
						if bytes.IndexByte(raw, '\n') < 0 && bytes.IndexByte(raw, '\r') < 0 {
							return scan.Plain, true
						}
					}
				}
			}
		}
		c.scratch = DecodeScalar(c.scratch[:0], c.T, c.Src, n)
		if scalarKind(c.T, c.Src, n, c.scratch) == String {
			return scan.Plain, true
		}
	}
	return 0, false
}

// nearHint reads the siblings before idx once, allocating nothing, and returns the
// style of the nearest string scalar among the last hintReach of them.
func (c *Ctx) nearHint(par int32, idx int) (scan.Style, bool) {
	mapping := c.T.Nodes[par].Kind == cst.Mapping
	entries := c.T.Count(par)
	if mapping {
		entries /= 2
	}
	if idx >= entries {
		// An add at the end: go back from the last entry, which costs the depth of
		// the tree and not the number of entries.
		x := c.T.Last(par)
		for j := 0; j < hintReach && x != 0; j++ {
			if s, ok := c.stringStyle(x); ok {
				return s, true
			}
			if x = c.T.Prev(x); mapping && x != 0 {
				x = c.T.Prev(x)
			}
		}
		return 0, false
	}
	var ring [hintReach]int32
	n := 0
	ch := c.T.First(par)
	for n < idx && ch != 0 {
		v := ch
		if mapping {
			v = c.T.Nodes[ch].Next
			if v == 0 {
				break
			}
		}
		ring[n%hintReach] = v
		n++
		ch = c.T.Nodes[v].Next
	}
	for j := 1; j <= min(n, hintReach); j++ {
		if s, ok := c.stringStyle(ring[(n-j)%hintReach]); ok {
			return s, true
		}
	}
	return 0, false
}

// ---- anchors ----

// lastIndex returns the highest node index in the subtree of n. Nodes are stored
// in document order, so a subtree is a contiguous run of indexes.
func (c *Ctx) lastIndex(n int32) int32 {
	for {
		l := c.T.Last(n)
		if l == 0 {
			return n
		}
		n = l
	}
}

// anchorsInUse refuses an edit that would delete an anchor still used by an alias
// outside the deleted text. self says whether the anchor on n itself counts.
//
// The deleted subtree is read once for its anchors, which is nearly always none;
// only then is the rest of the document read, once, for aliases that name them. An
// alias after a later definition of the same name refers to that one, so a name
// stops counting at its next definition.
func (c *Ctx) anchorsInUse(n int32, self bool) error {
	hi := c.lastIndex(n)
	c.T.SettleTo(hi)
	lo := n
	if !self {
		lo = n + 1
	}
	var names map[string]struct{}
	for i := lo; i <= hi; i++ {
		p := c.T.Nodes[i].Prop
		if p == 0 || !c.T.Props[p].HasAnchor() {
			continue
		}
		pr := &c.T.Props[p]
		if names == nil {
			names = make(map[string]struct{})
		}
		names[string(c.Src[pr.AnchorStart:pr.AnchorEnd])] = struct{}{}
	}
	if len(names) == 0 {
		return nil
	}
	c.T.Settle() // the text after the node is read
	docEnd := int32(len(c.T.Nodes))
	if c.Doc+1 < len(c.T.Docs) {
		docEnd = c.T.Docs[c.Doc+1].Root
	}
	for j := hi + 1; j < docEnd && len(names) > 0; j++ {
		c.tailVisits++
		nd := &c.T.Nodes[j]
		if nd.Kind == cst.Alias {
			if _, ok := names[string(c.Src[nd.Start+1:nd.End])]; ok {
				name := string(c.Src[nd.Start+1 : nd.End])
				return wrap(ErrAnchorInUse, "anchor &%s is used by an alias at offset %d", name, nd.Start)
			}
		}
		if q := nd.Prop; q != 0 && c.T.Props[q].HasAnchor() {
			delete(names, string(c.Src[c.T.Props[q].AnchorStart:c.T.Props[q].AnchorEnd]))
		}
	}
	return nil
}

// ---- replace ----

// Replace plans the replacement of the node at loc by v.
func (c *Ctx) Replace(loc Loc, v *Value, opt Options) ([]Repl, error) {
	n := loc.Node
	if n == 0 {
		return nil, ErrNotFound
	}
	if err := valueDepth(v); err != nil {
		return nil, err
	}
	if Equal(c.T, c.Src, n, v) {
		return nil, nil
	}
	if err := c.anchorsInUse(n, false); err != nil {
		return nil, err
	}
	nd := &c.T.Nodes[n]
	par := nd.Parent
	l := c.newLayout(int(nd.Start), opt)
	l.hint = c.oldHint(n)
	l.flow = nd.Flow || c.parentFlow(n) || c.noColon(n)
	isColl := v.Kind == Map || v.Kind == Seq
	var out []Repl
	switch {
	case isBlockScalar(nd) && v.Kind == String && v.Tag == "" && !opt.Safe && (v.Text == "" || blockOK(v.Text)):
		out = c.replaceBlockScalar(n, v, l)
	case !isColl || len(v.Items) == 0:
		out = c.inlineReplace(n, string(l.flowText(nil, v)), l)
	case l.flow || opt.Safe:
		out = c.inlineReplace(n, string(l.flowText(nil, v)), l)
	default:
		c.discover(l, par)
		out = c.blockReplace(n, v, l)
	}
	return append(out, c.tagRepl(n, v)...), nil
}

// oldHint is the quote style of the scalar being replaced, which a new string
// keeps if it can.
func (c *Ctx) oldHint(n int32) scan.Style {
	nd := &c.T.Nodes[n]
	if nd.Kind == cst.Scalar {
		switch nd.Style {
		case scan.SingleQuoted, scan.DoubleQuoted:
			return nd.Style
		}
	}
	return scan.Plain
}

// tagRepl removes the tag of node n if it would contradict v.
func (c *Ctx) tagRepl(n int32, v *Value) []Repl {
	p := c.T.PropOf(n)
	if p == nil || !p.HasTag() {
		return nil
	}
	tag := CanonTag(c.T, c.Src, n)
	if tagKeeps(tag, v, c.T.Nodes[n].Kind) {
		return nil
	}
	s, e := int(p.TagStart), int(p.TagEnd)
	t := s
	for t > 0 && c.Src[t-1] == ' ' {
		t--
	}
	if t > c.lineStart(s) && t != s {
		return []Repl{{Start: t, End: e}}
	}
	for e < len(c.Src) && c.Src[e] == ' ' {
		e++
	}
	return []Repl{{Start: s, End: e}}
}

func tagKeeps(tag string, v *Value, old cst.Kind) bool {
	if v.Tag != "" {
		// The new value brings its own tag, which is written in place of this one.
		return false
	}
	if tag == "!" && old == cst.Scalar {
		return v.Kind == String
	}
	switch tag {
	case "!!str":
		return v.Kind == String
	case "!!int":
		return v.Kind == Int
	case "!!float":
		return v.Kind == Float
	case "!!bool":
		return v.Kind == Bool
	case "!!null":
		return v.Kind == Null
	case "!!map":
		return v.Kind == Map
	case "!!seq":
		return v.Kind == Seq
	}
	if len(tag) > 2 && tag[:2] == "!!" {
		// Another core tag (!!binary, !!timestamp, ...) vouches for the text of the
		// old value only.
		return false
	}
	if v.Kind == Alias {
		return false
	}
	if v.Tag == tag {
		return true
	}
	oldColl := old == cst.Mapping || old == cst.Sequence
	newColl := v.Kind == Map || v.Kind == Seq
	if oldColl != newColl {
		return false
	}
	if oldColl {
		return (old == cst.Mapping) == (v.Kind == Map)
	}
	return true
}

// inlineReplace replaces the text of n by text that fits on one line.
func (c *Ctx) inlineReplace(n int32, text string, l *layout) []Repl {
	nd := &c.T.Nodes[n]
	switch {
	case nd.Kind == cst.Scalar && (nd.Style == scan.Literal || nd.Style == scan.Folded):
		ind := c.hdrEnd(n)
		if nd.Parent != 0 && ind < int(nd.Start) && containsBreak(c.Src[ind:nd.Start]) {
			// The header sits on a later line than its indicator: bring the value up.
			return []Repl{
				{Start: ind, End: ind, Text: []byte(" " + text)},
				{Start: c.lineEnd(ind), End: max(c.contentEnd(n), c.lineEnd(ind))},
			}
		}
		return []Repl{{Start: int(nd.Start), End: c.textEnd(n), Text: []byte(text + c.nlAt(int(nd.Start)))}}
	case nd.Kind == cst.Scalar || nd.Kind == cst.Alias:
		if nd.Start == nd.End {
			sep := " "
			if nd.Parent == 0 {
				return c.rootInsert(nd, text, false)
			} else if pn := &c.T.Nodes[nd.Parent]; pn.Kind == cst.Mapping && !c.hasColon(n) {
				// An entry without a colon ("{a}" or "? a"): the colon comes with the value.
				sep = ": "
				if !pn.Flow {
					// The colon goes on a line of its own in the key's column, after
					// whatever else the key's line holds (a comment).
					nl := c.nlAt(int(nd.Start))
					colon := strings.Repeat(" ", c.keyCol(n)) + ": " + text
					if at := int(nd.Start); at > int(c.bom) && isBreak(c.Src[at-1]) {
						// The key ends with its line break (a block scalar): the colon
						// line goes right there.
						return []Repl{{Start: at, End: at, Text: []byte(colon + nl)}}
					}
					at := c.lineEnd(int(nd.Start))
					return []Repl{{Start: at, End: at, Text: []byte(nl + colon)}}
				}
			}
			return []Repl{{Start: int(nd.Start), End: int(nd.End), Text: []byte(sep + text)}}
		}
		return []Repl{{Start: int(nd.Start), End: int(nd.End), Text: []byte(c.afterColon(n, text))}}
	case nd.Flow:
		return []Repl{{Start: int(nd.Start), End: int(nd.End), Text: []byte(c.afterColon(n, text))}}
	}
	end := c.contentEnd(n)
	ind := c.hdrEnd(n)
	if ind >= int(nd.Start) || !strings.ContainsAny(string(c.Src[ind:nd.Start]), "\r\n") {
		return []Repl{{Start: int(nd.Start), End: end, Text: []byte(text)}}
	}
	return []Repl{
		{Start: ind, End: ind, Text: []byte(" " + text)},
		{Start: c.lineEnd(ind), End: end},
	}
}

// hasColon reports whether the empty value n of a mapping entry has a ":" after its key.
func (c *Ctx) hasColon(n int32) bool {
	k := c.T.Prev(n)
	if k == 0 {
		return false
	}
	i := cst.SkipTrivia(c.Src, int(c.T.Nodes[k].End))
	if i >= len(c.Src) || c.Src[i] != ':' {
		return false
	}
	// A colon in a column that is not the entry's belongs to an outer mapping
	// (a block key followed by a shallower ": value").
	if kn := &c.T.Nodes[k]; kn.Start != kn.End && c.T.Nodes[kn.Parent].Kind == cst.Mapping {
		return c.T.ColonIsValueOf(c.Src, k, i)
	}
	return true
}

// noColon reports whether n is the empty value of a block mapping entry written
// without a ":" ("? key"), which only the inline forms can fill.
func (c *Ctx) noColon(n int32) bool {
	nd := &c.T.Nodes[n]
	if nd.Kind != cst.Scalar || nd.Start != nd.End || nd.Parent == 0 {
		return false
	}
	pn := &c.T.Nodes[nd.Parent]
	return pn.Kind == cst.Mapping && !pn.Flow && !c.T.IsKey(n) && !c.hasColon(n)
}

func (c *Ctx) keyCol(n int32) int {
	par := c.T.Nodes[n].Parent
	if par == 0 {
		return -1
	}
	switch c.T.Nodes[par].Kind {
	case cst.Mapping:
		k := n
		if !c.T.IsKey(n) {
			k = c.T.Prev(n)
		}
		return c.col(c.entryStart(k))
	case cst.Sequence:
		return c.col(c.dashOf(n))
	}
	return -1
}

// blockReplace replaces n by the non-empty collection v written in block style.
func (c *Ctx) blockReplace(n int32, v *Value, l *layout) []Repl {
	nd := &c.T.Nodes[n]
	render := func(col int, indentFirst bool) []byte {
		// Room for the text up front: growing the buffer by appends leaves a
		// trail of buffers behind that is several times the size of the result.
		dst := make([]byte, 0, v.TextHint())
		if v.Kind == Map {
			return l.mapping(dst, v.Items, col, indentFirst)
		}
		return l.sequence(dst, v.Items, col, indentFirst)
	}
	if (nd.Kind == cst.Mapping || nd.Kind == cst.Sequence) && !nd.Flow {
		col := c.col(int(nd.Start))
		return []Repl{{Start: int(nd.Start), End: c.contentEnd(n), Text: trimNL(render(col, false), l.nl)}}
	}
	if nd.Parent == 0 {
		body := trimNL(render(0, true), l.nl)
		if nd.Start == nd.End {
			return c.rootInsert(nd, string(body), true)
		}
		if !c.onlyBlankBefore(int(nd.Start)) {
			// "--- value": a block collection cannot start on the marker's line.
			body = append([]byte(l.nl), body...)
		}
		return []Repl{{Start: int(nd.Start), End: int(nd.End), Text: body}}
	}
	par := &c.T.Nodes[nd.Parent]
	hasProps := nd.Prop != 0
	if par.Kind == cst.Sequence && !hasProps {
		itemCol := c.col(c.dashOf(n)) + 1 + max(l.gap, 1)
		text := render(itemCol, false)
		if !endsWithBreak(c, nd) {
			text = trimNL(text, l.nl)
		}
		if nd.Start == nd.End {
			if le := c.lineEnd(int(nd.Start)); c.commentAhead(int(nd.Start), le) {
				// "- # c": the comment ends the item's line and stays there; the
				// collection goes on the lines below, where the text of an item
				// starts (the siblings' gap after the dash).
				body := render(c.col(c.dashOf(n))+1+c.siblingGap(n), true)
				at := c.afterBreak(le)
				if at >= len(c.Src) && (len(c.Src) == 0 || !isBreak(c.Src[len(c.Src)-1])) {
					body = append([]byte(l.nl), trimNL(body, l.nl)...)
				}
				return []Repl{{Start: at, End: at, Text: body}}
			}
			return []Repl{{Start: int(nd.Start), End: int(nd.End), Text: append([]byte(" "), text...)}}
		}
		return []Repl{{Start: int(nd.Start), End: int(nd.End), Text: text}}
	}
	// The value goes on the lines below its key or dash.
	kc := c.keyCol(n)
	var col int
	switch {
	case par.Kind == cst.Sequence:
		col = kc + 2
	case v.Kind == Seq:
		col = kc + l.seqOff
	default:
		col = kc + l.step
	}
	body := render(col, true)
	ind := c.hdrEnd(n)
	if nd.Kind == cst.Scalar && (nd.Style == scan.Literal || nd.Style == scan.Folded) {
		end := c.textEnd(n)
		return []Repl{{Start: ind, End: end, Text: append([]byte(l.nl), body...)}}
	}
	at := c.afterBreak(ind)
	if nd.End > nd.Start {
		at = c.afterBreak(int(nd.End))
	}
	if at >= len(c.Src) && (len(c.Src) == 0 || !isBreak(c.Src[len(c.Src)-1])) {
		body = append([]byte(l.nl), trimNL(body, l.nl)...)
	}
	out := []Repl{}
	if nd.End > nd.Start {
		out = append(out, Repl{Start: ind, End: int(nd.End)})
	}
	return append(out, Repl{Start: at, End: at, Text: body})
}

// endsWithBreak reports whether the text of node nd ends with its line break: a
// block scalar's text includes the break that ends its last line (and any blank
// lines after it), so a replacement that is a whole line must not drop one.
func endsWithBreak(c *Ctx, nd *cst.Node) bool {
	return nd.Kind == cst.Scalar && (nd.Style == scan.Literal || nd.Style == scan.Folded) &&
		nd.End > nd.Start && int(nd.End) <= len(c.Src) && isBreak(c.Src[nd.End-1])
}

// replaceBlockScalar rewrites the header and content of block scalar n to hold v.
func (c *Ctx) replaceBlockScalar(n int32, v *Value, l *layout) []Repl {
	nd := &c.T.Nodes[n]
	start := int(nd.Start)
	folded := nd.Style == scan.Folded
	// Header: indicator characters after the style character.
	he := start + 1
	hasDigit := false
	digit := 0
	for he < int(nd.End) && (c.Src[he] == '-' || c.Src[he] == '+' || (c.Src[he] >= '1' && c.Src[he] <= '9')) {
		if c.Src[he] >= '1' {
			hasDigit = true
			digit = int(c.Src[he] - '0')
		}
		he++
	}
	base := c.keyCol(n)
	if hasDigit {
		base = int(nd.Indent) - digit
	}
	indent := int(nd.Indent)
	if indent <= base {
		indent = base + 2
	}
	if indent < 1 {
		indent = 1
	}
	chomp, needInd, body := blockBody(v.Text, folded, c.blockChomp(n), true, indent, c.nlAt(start))
	hdr := []byte{c.Src[start]}
	if hasDigit || needInd {
		d := indent - base
		if d < 1 || d > 9 {
			// No indicator can express this indentation; the caller's read-back
			// check will reject it and retry with a safe form.
			d = max(1, min(9, d))
		}
		hdr = append(hdr, byte('0'+d))
	}
	if chomp != 0 {
		hdr = append(hdr, chomp)
	}
	cs := c.afterBreak(start)
	ce := c.textEnd(n)
	if chomp == '+' {
		ce = int(nd.End)
	}
	if cs > int(nd.End) {
		cs = int(nd.End)
	}
	if ce < cs {
		ce = cs
	}
	if cs == len(c.Src) && cs > 0 && !isBreak(c.Src[cs-1]) {
		body = append([]byte(l.nl), body...)
	}
	return []Repl{
		{Start: start, End: he, Text: hdr},
		{Start: cs, End: ce, Text: body},
	}
}

func isBlockScalar(nd *cst.Node) bool {
	return nd.Kind == cst.Scalar && (nd.Style == scan.Literal || nd.Style == scan.Folded)
}

// rootInsert writes text where the empty root node nd is, which can follow a
// document start marker on the same line or precede the next marker.
func (c *Ctx) rootInsert(nd *cst.Node, text string, block bool) []Repl {
	at := int(nd.Start)
	if d := &c.T.Docs[c.Doc]; d.ExplicitStart {
		// The empty node is reported where the next token starts, which can be
		// after comments; the value belongs right after the "---" marker.
		at = int(d.StartMarker[1])
		from := at
		if nd.Prop != 0 {
			// A tag or anchor on the marker line is not text before the comment.
			q := &c.T.Props[nd.Prop]
			if e := int(max(q.AnchorEnd, q.TagEnd)); e > from && e <= c.lineEnd(at) {
				from = e
			}
		}
		if le := c.lineEnd(at); block && le > at && c.onlyCommentBetween(from, le) {
			// A comment ends the marker line: the block goes below it, so that the
			// comment stays on the marker's line.
			at = le
		}
	}
	prefix, suffix := "", ""
	if at > 0 && !isBreak(c.Src[at-1]) {
		if block {
			prefix = c.nlAt(at - 1)
		} else if c.Src[at-1] != ' ' {
			prefix = " "
		}
	}
	if at < len(c.Src) && !isBreak(c.Src[at]) {
		suffix = c.nlAt(at)
	}
	return []Repl{{Start: at, End: at, Text: []byte(prefix + text + suffix)}}
}

// siblingGap returns the spaces between the dash and the text in the items next
// to item n (the first that has text on its dash line), or 1.
func (c *Ctx) siblingGap(n int32) int {
	for _, s := range [2]int32{c.T.Nodes[n].Next, c.T.Prev(n)} {
		if s == 0 {
			continue
		}
		ds := c.dashOf(s)
		if fs := int(c.T.FullStart(s)); fs > ds+1 && !containsBreak(c.Src[ds:fs]) {
			return fs - ds - 1
		}
	}
	return 1
}

// commentAhead reports whether the text from a to b (on one line) is blanks
// followed by a comment.
func (c *Ctx) commentAhead(a, b int) bool {
	for ; a < b; a++ {
		switch c.Src[a] {
		case ' ', '	':
		case '#':
			return true
		default:
			return false
		}
	}
	return false
}

// onlyCommentBetween reports whether the text from a to b (on one line) is blanks,
// or blanks and a comment.
func (c *Ctx) onlyCommentBetween(a, b int) bool {
	for ; a < b; a++ {
		switch c.Src[a] {
		case ' ', '\t':
		case '#':
			return true
		default:
			return false
		}
	}
	return true
}

// afterColon puts a space before a value that would otherwise touch the ":" after
// a plain or empty key of a flow mapping: "{k:v}" is one scalar, not an entry. After
// a quoted key the colon needs no space.
func (c *Ctx) afterColon(n int32, text string) string {
	nd := &c.T.Nodes[n]
	s := int(nd.Start)
	if s == 0 || c.Src[s-1] != ':' || text == "" || nd.Parent == 0 || !c.T.Nodes[nd.Parent].Flow {
		return text
	}
	if k := c.T.Prev(n); k != 0 && c.T.Nodes[k].Kind == cst.Scalar && c.T.Nodes[k].Style == scan.Plain {
		return " " + text
	}
	return text
}
