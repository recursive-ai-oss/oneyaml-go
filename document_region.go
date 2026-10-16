package oneyaml

import (
	"bytes"
	"slices"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
)

// spliceHook, when set, is called after an edit has been spliced into the tree.
// Tests set it to compare the tree with a parse of the whole text.
var spliceHook func(s *docStream, r *regionDebug)

// regionDebug describes a region edit for spliceHook.
type regionDebug struct {
	cn        int32
	S, E, pad int
	text      []byte
	repls     []edit.Repl
}

// regionMinSrc is the stream size below which an edit parses the whole stream: a
// parse of a few hundred bytes costs less than setting up a region. Tests set it
// to zero so that small documents take the region path too.
var regionMinSrc = 512

// regionBigShare is the size from which a stream is not edited through a region
// that is more than half of it: parsing that region costs what parsing the stream
// does, and the whole parse is the simpler path.
const regionBigShare = 2048

// regionOff makes every edit parse the whole stream; tests compare the two ways.
var regionOff bool

// maxRegionLevels bounds how many enclosing containers an edit tries as its
// region before it gives up and parses the stream.
const maxRegionLevels = 3

// editRegion writes an edit by parsing only the entries it touches. The edit
// changes the text of a few consecutive entries of one block collection; those
// entries are cut out, their edited text is parsed on its own, and the nodes that
// result replace the old ones in the tree, with every offset after them moved. The
// same check as for a whole-stream parse (the value reads back, the entries beside
// it are as they were) runs on the region's tree before anything is changed, so a
// refusal leaves the stream untouched. It reports whether the edit was committed;
// if not, nothing has changed and the caller parses the stream.
func (s *docStream) editRegion(c *edit.Ctx, pl *planned) bool {
	if regionOff || pl.parent == 0 || len(pl.repls) == 0 || len(s.src) < regionMinSrc {
		return false
	}
	repls := pl.repls
	slices.SortStableFunc(repls, func(a, b edit.Repl) int { return a.Start - b.Start })
	rs, re := repls[0].Start, repls[0].End
	pos := 0
	for _, r := range repls {
		if r.Start < pos || r.End < r.Start || r.End > len(s.src) {
			return false
		}
		pos = r.End
		re = max(re, r.End)
	}
	defer s.trimScratch()
	t := &s.tree
	tries := 0
	for cn := pl.parent; cn != 0 && tries < maxRegionLevels; cn = t.Nodes[cn].Parent {
		nd := &t.Nodes[cn]
		if t.First(cn) == 0 || nd.Pair || (nd.Kind != cst.Mapping && nd.Kind != cst.Sequence) {
			continue // an empty collection has no entries; a single pair is part of its item
		}
		tries++
		skip := 0
		for p := t.Nodes[cn].Parent; p != 0; p = t.Nodes[p].Parent {
			skip++
		}
		if s.tryRegion(c, pl, cn, skip, rs, re) {
			return true
		}
	}
	return false
}

// geom reads where the entries of a container sit in the text. The entries of a
// block collection are whole lines; those of a flow collection are the spans of
// their items, since the lines they share belong to other entries too.
type geom struct {
	c    *edit.Ctx
	t    *cst.Tree
	flow bool
}

// lo is the start of the text of the entry whose first node is f.
func (g geom) lo(f int32) int {
	if g.flow {
		return int(g.t.FullStart(f))
	}
	return g.c.EntryLo(f)
}

// hi is the end of the text of the entry whose last node is l.
func (g geom) hi(l int32) int {
	if g.flow {
		return int(g.t.Nodes[l].End)
	}
	return g.c.EntryHi(l)
}

// runInfo is the run of consecutive entries of a container that an edit touches.
type runInfo struct {
	first, last, prev int32 // first node of the first entry, last node of the last, the sibling before
	lo, hi            int   // text span of the run
	before, kids      int   // entries before the run, children in it
}

// scanRun finds the entries of cn, from entry number idx (whose first node is f and
// whose predecessor ends with prev), whose lines the edit [rs, re] touches. With
// margin it adds the entry on each side, which the check reads.
func scanRun(g geom, margin, mapping bool, f int32, idx int, prev int32, rs, re int) (r runInfo, ok bool) {
	t := g.t
	step := 1
	if mapping {
		step = 2
	}
	var (
		prevLast, prevPrevLast, prevFirst int32
		prevLo, prevHi                    int
	)
	prevLast = prev
	for ; f != 0; idx++ {
		l := f
		if mapping {
			l = t.Nodes[f].Next
			if l == 0 {
				return r, false
			}
		}
		lo, hi := g.lo(f), g.hi(l)
		if mapping && !g.flow && prevLast != 0 && indicatorAbove(g.c.Src, int(t.Nodes[prevLast].End), lo) {
			// The key starts on a later line than its "?", so the lines of the entry
			// are not the lines this code finds from the node.
			return r, false
		}
		if lo < prevHi {
			return r, false // entries that overlap in the text: a layout this code does not read
		}
		prevHi = hi
		if lo > re {
			if r.first != 0 && margin { // the entry after the run
				r.last, r.hi = l, hi
				r.kids += step
			}
			break
		}
		if hi >= rs {
			if r.first == 0 {
				if prevFirst != 0 && margin { // the entry before the run
					r.first, r.prev, r.lo, r.before = prevFirst, prevPrevLast, prevLo, idx-1
					r.kids += step
				} else {
					r.first, r.prev, r.lo, r.before = f, prevLast, lo, idx
				}
			}
			r.last, r.hi = l, hi
			r.kids += step
		}
		prevFirst, prevPrevLast, prevLast, prevLo = f, prevLast, l, lo
		f = t.Nodes[l].Next
	}
	return r, r.first != 0
}

// indicatorAbove reports whether src[from:to], the text between the end of one
// entry and the line where the next entry's key starts, holds a "?".
func indicatorAbove(src []byte, from, to int) bool {
	return from >= 0 && from <= to && to <= len(src) && bytes.IndexByte(src[from:to], '?') >= 0
}

// nearEnd is how many entries back from the last one findRun looks for the entry
// of an edit before it walks forward from the first.
const nearEnd = 64

// findRun looks for the touched entries. The walk to the entry that holds the
// anchor node follows links only; the text of the entries is read from two entries
// before it, which is as far back as an edit of that entry reaches.
func findRun(g geom, margin bool, cn, anchor int32, rs, re int) (runInfo, bool) {
	t := g.t
	mapping := t.Nodes[cn].Kind == cst.Mapping
	first := t.First(cn)
	if anchor != 0 {
		// The entry is one of the last: go back from the end, not forward from the
		// start, so that an edit at the end does not walk the whole container.
		if last := t.Last(cn); last != 0 {
			entries := t.Count(cn)
			start := last
			if mapping {
				entries /= 2
				start = t.Prev(last)
			}
			back := -1
			for i := 0; i < nearEnd && start != 0; i++ {
				l := start
				if mapping {
					l = t.Nodes[start].Next
				}
				if start == anchor || l == anchor {
					back = i
					break
				}
				if start = t.Prev(start); mapping && start != 0 {
					start = t.Prev(start)
				}
			}
			if back >= 0 {
				idx := entries - 1 - back
				sidx := max(idx-2, 0)
				for i := idx; i > sidx; i-- {
					start = t.Prev(start)
					if mapping {
						start = t.Prev(start)
					}
				}
				prev := t.Prev(start)
				if sidx == 0 || rs >= g.lo(start) {
					r, ok := scanRun(g, margin, mapping, start, sidx, prev, rs, re)
					if ok && !(sidx > 0 && r.first == start && margin) {
						return r, true
					}
				}
				return scanRun(g, margin, mapping, first, 0, 0, rs, re)
			}
		}
		var ef, el [4]int32
		idx := 0
		for f := first; f != 0; idx++ {
			l := f
			if mapping {
				if l = t.Nodes[f].Next; l == 0 {
					break
				}
			}
			ef[idx%4], el[idx%4] = f, l
			if f == anchor || l == anchor {
				sidx := max(idx-2, 0)
				var prev int32
				if sidx > 0 {
					prev = el[(sidx-1)%4]
				}
				start := ef[sidx%4]
				if sidx == 0 || rs >= g.lo(start) {
					r, ok := scanRun(g, margin, mapping, start, sidx, prev, rs, re)
					if ok && !(sidx > 0 && r.first == start && margin) {
						return r, true
					}
				}
				break
			}
			f = t.Nodes[l].Next
		}
	}
	return scanRun(g, margin, mapping, first, 0, 0, rs, re)
}

// regionScratch is the tree and the text a region is parsed from.
type regionScratch struct {
	tree cst.Tree
	buf  []byte
}

// trimScratch lets go of the region scratch space after a big region, so that a
// document does not keep memory for its largest edit.
func (s *docStream) trimScratch() {
	if s.ed != nil && s.ed.rs != nil && (cap(s.ed.rs.buf) > 64<<10 || cap(s.ed.rs.tree.Nodes) > 4096) {
		s.ed.rs = nil
	}
}

// tryRegion tries container cn as the region's root.
func (s *docStream) tryRegion(c *edit.Ctx, pl *planned, cn int32, skip, rs, re int) bool {
	t := &s.tree
	mapping := t.Nodes[cn].Kind == cst.Mapping
	anchor := pl.anchor
	for anchor != 0 && t.Nodes[anchor].Parent != cn {
		anchor = t.Nodes[anchor].Parent
	}
	flow := t.Nodes[cn].Flow
	run, ok := findRun(geom{c, t, flow}, pl.margin, cn, anchor, rs, re)
	if !ok {
		return false
	}
	runFirst, runLast, runPrev := run.first, run.last, run.prev
	lo0, hi0, before, kids := run.lo, run.hi, run.before, run.kids
	if flow {
		return s.tryFlowRegion(c, pl, cn, skip, rs, re, run)
	}
	// The text of the run starts at a line start when its first entry begins its
	// line (then comment lines above the entry may be part of the edit), and at
	// the entry itself when it shares the line with its parent's indicator.
	S, E := lo0, hi0
	if runPrev == 0 && lo0 > int(t.Nodes[cn].Start) {
		return false // the container's text starts before its first entry does
	}
	if rs < S {
		if runPrev == 0 || S != c.LineStart(S) {
			return false
		}
		S = c.LineStart(rs)
	}
	if re > E || (len(s.src) >= regionBigShare && E-S > len(s.src)/2) {
		return false
	}
	total := t.Count(cn)
	if skip == 0 && kids >= total {
		return false // the whole document: no gain over parsing it
	}
	pad := S - c.LineStart(S)
	if !standInOK(s.src[S-pad : S]) {
		return false
	}
	if hidesBadText(s.src[S:E]) {
		return false
	}

	maxDepth := s.depth()
	if maxDepth > 0 {
		if maxDepth -= skip; maxDepth <= 0 {
			return false
		}
	}

	// The edited text of the run, indented as it is in the stream.
	es := s.es()
	if es.rs == nil {
		es.rs = new(regionScratch)
	}
	scr := es.rs
	buf := scr.buf[:0]
	for i := 0; i < pad; i++ {
		buf = append(buf, ' ')
	}
	at := S
	for _, r := range pl.repls {
		buf = append(buf, s.src[at:r.Start]...)
		buf = append(buf, r.Text...)
		at = r.End
	}
	buf = append(buf, s.src[at:E]...)
	newEnd := len(buf)

	// What follows the run decides where its last node ends (a block scalar takes
	// the blank lines after it, up to the next token), so the lines up to the next
	// content are parsed with it, and a stand-in entry plays the next token.
	tail := c.GapEnd(E)
	buf = append(buf, s.src[E:tail]...)
	sentinel := 0
	if tail < len(s.src) {
		if newEnd > pad && !isLineBreak(buf[len(buf)-1]) {
			scr.buf = buf
			return false // the next entry would share the line: not an edit to check here
		}
		for i := c.EntryCol(runFirst); i > 0; i-- {
			buf = append(buf, ' ')
		}
		if mapping {
			buf = append(buf, "0: 0\n"...)
			sentinel = 2
		} else {
			buf = append(buf, "- 0\n"...)
			sentinel = 1
		}
	}
	scr.buf = buf

	rt := &scr.tree
	err := s.buildTree(rt, buf, maxDepth)
	if err != nil || !regionShape(rt, &t.Nodes[cn], sentinel) {
		return false
	}
	if sentinel > 0 {
		// Drop the stand-in: its nodes are the last ones, and the entry before it
		// is the last of the run.
		first := int32(len(rt.Nodes) - sentinel)
		for ch := rt.First(1); ch != 0; ch = rt.Nodes[ch].Next {
			if rt.Nodes[ch].Next == first {
				rt.Nodes[ch].Next = 0
				break
			}
		}
		rt.Nodes = rt.Nodes[:first]
		rt.Nodes[1].Indent -= int32(sentinel)
	}
	rg := regionInfo{skip: skip, before: before, outside: total - kids}
	return s.commitRegion(pl, cn, rt, buf, pad, newEnd, S, E, runPrev, runFirst, runLast, &rg)
}

// commitRegion checks the tree rt of the edited run against the edit, and if it
// passes puts the run's text and nodes in place of the old ones. buf holds pad
// bytes of stand-in text, then the run's new text up to newEnd.
func (s *docStream) commitRegion(pl *planned, cn int32, rt *cst.Tree, buf []byte, pad, newEnd, S, E int, runPrev, runFirst, runLast int32, rg *regionInfo) bool {
	t := &s.tree
	rg.lo, rg.hi, rg.cn, rg.shift = runFirst, t.SubtreeEnd(runLast), cn, int32(S-pad)
	if !pl.check(rt, buf, rg) {
		return false
	}

	// The new entries must sit between their neighbours, as the old ones did.
	text := buf[pad:newEnd]
	delta := len(text) - (E - S)
	if !seamsOK(t, rt, runPrev, runLast, int32(S-pad), delta) {
		return false
	}

	// Commit: the text, then the tree.
	s.ed.work.moved = addSat(s.ed.work.moved, max(len(s.src)-E, 0))
	s.spliceText(S, E, text)
	if s.shared {
		s.unshare()
	}
	s.ed.work.shifted = addSat(s.ed.work.shifted, len(t.Nodes)-1-int(t.SubtreeEnd(runLast)))
	oldKids := t.Count(cn) - rg.outside
	newKids := rt.Count(1)
	t.Splice(cn, runPrev, runFirst, runLast, rt, int32(S-pad), int32(E), int32(delta))
	s.dirty = true
	gen := s.gen
	s.gen++
	step := 1 + b2i(t.Nodes[cn].Kind == cst.Mapping)
	s.keepCursor(gen, runFirst, rg.hi, int32(len(rt.Nodes)-2)-(rg.hi-runFirst+1), cn, rg.before*step, newKids-oldKids)
	s.ed.work.regions++
	if spliceHook != nil {
		spliceHook(s, &regionDebug{cn: cn, S: S, E: E, pad: pad, text: text, repls: pl.repls})
	}
	return true
}

// tryFlowRegion is tryRegion for a flow collection cn: the entries of the run are
// the items the edit touches, their text is parsed on its own inside brackets of
// the collection's kind, and the nodes that result replace the old ones. The
// brackets, the items outside the run and the text between them are not read.
func (s *docStream) tryFlowRegion(c *edit.Ctx, pl *planned, cn int32, skip, rs, re int, run runInfo) bool {
	t := &s.tree
	cnn := &t.Nodes[cn]
	S, E := run.lo, run.hi
	if rs < S || re > E || run.kids >= t.Count(cn) && skip == 0 {
		return false
	}
	if len(s.src) >= regionBigShare && E-S > len(s.src)/2 {
		return false
	}
	// Only whole items are cut out: what lies before the run must be the comma that
	// follows the item before it (or the bracket) and nothing that shapes the item
	// after it, such as the "?" of an explicit key.
	from := int(cnn.Start) + 1
	if run.prev != 0 {
		from = int(t.Nodes[run.prev].End)
	}
	if !flowGapOK(s.src, from, S, run.prev != 0) {
		return false
	}
	for _, r := range pl.repls {
		if bytes.IndexByte(r.Text, '\n') >= 0 || bytes.IndexByte(r.Text, '\r') >= 0 {
			// A new line would have to be indented as the stream indents it, which the
			// text of the run alone does not tell.
			return false
		}
	}
	if hidesBadText(s.src[S:E]) {
		return false
	}
	maxDepth := s.depth()
	if maxDepth > 0 {
		if maxDepth -= skip; maxDepth <= 0 {
			return false
		}
	}

	es := s.es()
	if es.rs == nil {
		es.rs = new(regionScratch)
	}
	scr := es.rs
	open, shut := byte('['), byte(']')
	if cnn.Kind == cst.Mapping {
		open, shut = '{', '}'
	}
	buf := append(scr.buf[:0], open)
	at := S
	for _, r := range pl.repls {
		buf = append(buf, s.src[at:r.Start]...)
		buf = append(buf, r.Text...)
		at = r.End
	}
	buf = append(buf, s.src[at:E]...)
	newEnd := len(buf)
	buf = append(buf, shut)
	scr.buf = buf

	rt := &scr.tree
	if err := s.buildTree(rt, buf, maxDepth); err != nil || !regionShape(rt, cnn, 0) {
		return false
	}
	if lastIsPair(rt) {
		// A single pair ("[a: b]") ends where the next token starts, white space
		// before it included. The white space after the run is not part of the
		// run, so the pair is parsed with it, or it would end too early.
		tok := cst.SkipTrivia(s.src, E)
		gap := s.src[E:tok]
		if len(gap) > 0 {
			if bytes.IndexByte(gap, '#') >= 0 || bytes.IndexByte(gap, '\n') >= 0 || bytes.IndexByte(gap, '\r') >= 0 {
				return false
			}
			buf = append(append(buf[:newEnd], gap...), shut)
			scr.buf = buf
			if err := s.buildTree(rt, buf, maxDepth); err != nil || !regionShape(rt, cnn, 0) || !lastIsPair(rt) {
				return false
			}
		}
	}
	rg := regionInfo{skip: skip, before: run.before, outside: t.Count(cn) - run.kids}
	return s.commitRegion(pl, cn, rt, buf, 1, newEnd, S, E, run.prev, run.first, run.last, &rg)
}

// lastIsPair reports whether the last item of the region's root is a single pair.
func lastIsPair(rt *cst.Tree) bool {
	if len(rt.Nodes) < 2 {
		return false
	}
	last := int32(0)
	for ch := rt.First(1); ch != 0; ch = rt.Nodes[ch].Next {
		last = ch
	}
	return last != 0 && rt.Nodes[last].Pair
}

// flowGapOK reports whether src[from:to], the text between two items of a flow
// collection (or between its bracket and the first item), is only white space,
// comments and, after an item, the comma that separates it from the next.
func flowGapOK(src []byte, from, to int, comma bool) bool {
	if from > to || to > len(src) {
		return false
	}
	commas := 0
	for i := from; i < to; i++ {
		switch src[i] {
		case ' ', '\t', '\n', '\r':
		case ',':
			commas++
		case '#':
			if i > from && src[i-1] != ' ' && src[i-1] != '\t' && src[i-1] != '\n' && src[i-1] != '\r' {
				return false
			}
			for i < to && src[i] != '\n' && src[i] != '\r' {
				i++
			}
		default:
			return false
		}
	}
	return commas == b2i(comma)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// standInOK reports whether spaces can stand in for the text before the run on its
// first line without changing how the run reads: the text must be blanks, or the
// "- " indicators of the block sequences the run's entries are items of. Anything
// else (a "?" or ":" indicator, a comment, a key) shapes the entries after it, and
// a tab or a multi-byte character is not one column of its own.
func standInOK(prefix []byte) bool {
	for i, b := range prefix {
		switch {
		case b == ' ':
		case b == '-' && i+1 < len(prefix) && prefix[i+1] == ' ' && (i == 0 || prefix[i-1] == ' '):
		default:
			return false
		}
	}
	return true
}

// hidesBadText reports whether text holds a byte that the scanner reports as bad
// only when nothing worse comes before it: a DEL, or the first byte of a C1
// control or of U+FFFE and U+FFFF. A stream that is accepted because of such a
// character may hold invalid text behind it, and taking the character out would
// expose that text, which only a parse of the whole stream shows.
func hidesBadText(text []byte) bool {
	for _, b := range text {
		if b == 0x7F || b == 0xC2 || b == 0xEF {
			return true
		}
	}
	return false
}

// seamsOK checks that the region's nodes lie after the entry before the run and
// end before the entry after it, in the coordinates of the edited text.
func seamsOK(t, rt *cst.Tree, prev, last, shift int32, delta int) bool {
	first := rt.First(1)
	if prev != 0 && t.Nodes[prev].End > rt.FullStart(first)+shift {
		return false
	}
	if nx := t.Nodes[last].Next; nx != 0 {
		end := int32(0)
		for ch := first; ch != 0; ch = rt.Nodes[ch].Next {
			end = rt.Nodes[ch].End
		}
		if end+shift > t.FullStart(nx)+int32(delta) {
			return false
		}
	}
	return true
}

// regionShape reports whether the parse of a region is a block collection of the
// kind of the container it stands for, with the stand-in entry after the entries
// of the region, and nothing else.
func regionShape(rt *cst.Tree, want *cst.Node, sentinel int) bool {
	if len(rt.Docs) != 1 || len(rt.Nodes) < 3+sentinel {
		return false
	}
	d := &rt.Docs[0]
	if d.Root != 1 || d.ExplicitStart || d.ExplicitEnd || d.VersionMajor != 0 {
		return false
	}
	r := &rt.Nodes[1]
	if r.Kind != want.Kind || r.Flow != want.Flow || r.Pair || r.Prop != 0 || rt.First(1) == 0 {
		return false
	}
	for i := len(rt.Nodes) - sentinel; i < len(rt.Nodes); i++ {
		if n := &rt.Nodes[i]; n.Parent != 1 || n.Kind != cst.Scalar || n.Prop != 0 {
			return false
		}
	}
	return true
}

func isLineBreak(b byte) bool { return b == '\n' || b == '\r' }

// spliceText replaces src[from:to] with text. A buffer the stream owns is edited
// in place; otherwise the new text goes in a new buffer with a little room.
func (s *docStream) spliceText(from, to int, text []byte) {
	old := len(s.src)
	n := old - (to - from) + len(text)
	if s.owned && cap(s.src) >= n {
		delta := len(text) - (to - from)
		switch {
		case delta > 0:
			s.src = s.src[:n]
			copy(s.src[to+delta:], s.src[to:old])
		case delta < 0:
			copy(s.src[from+len(text):], s.src[to:old])
			s.src = s.src[:n]
		}
		copy(s.src[from:], text)
		return
	}
	nb := make([]byte, n, n+n/16+32)
	copy(nb, s.src[:from])
	copy(nb[from:], text)
	copy(nb[from+len(text):], s.src[to:])
	s.setSrc(nb)
}
