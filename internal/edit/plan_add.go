package edit

import (
	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/scan"
)

// insertAfter returns where text that follows node n on its own lines goes: the
// start of the line after n's last line.
func (c *Ctx) insertAfter(n int32) int {
	e := c.textEnd(n)
	if e > c.bom && isBreak(c.Src[e-1]) {
		return e
	}
	return c.afterBreak(e)
}

// lines prepares text made of whole lines for insertion at at: when the source
// ends without a line break there, the break is written first and the final one
// dropped, so the file still ends the way it did.
func (c *Ctx) lines(text []byte, at int, nl string) []byte {
	if at >= len(c.Src) && len(c.Src) > 0 && !isBreak(c.Src[len(c.Src)-1]) {
		return append([]byte(nl), trimNL(text, nl)...)
	}
	return text
}

// endsWithKeep reports whether the last leaf of n is a block scalar that keeps
// its trailing blank lines.
func (c *Ctx) endsWithKeep(n int32) bool {
	for {
		l := c.T.Last(n)
		if l == 0 || c.T.Nodes[n].Flow {
			break
		}
		n = l
	}
	return isBlockScalarNode(&c.T.Nodes[n]) && c.blockChomp(n) == '+'
}

// endingBlockIndent returns the indentation of the text of the block scalar that
// ends node n, or -1 when n does not end with one or the scalar has no text line.
func (c *Ctx) endingBlockIndent(n int32) int {
	for {
		l := c.T.Last(n)
		if l == 0 || c.T.Nodes[n].Flow {
			break
		}
		n = l
	}
	nd := &c.T.Nodes[n]
	if !isBlockScalarNode(nd) {
		return -1
	}
	for ls := c.afterBreak(int(nd.Start)); ls < int(nd.End); {
		if !c.blankLine(ls) {
			return c.spacesAt(ls)
		}
		next := c.afterBreak(ls)
		if next <= ls {
			break
		}
		ls = next
	}
	return -1
}

// spacesAt counts the spaces that start the line at ls.
func (c *Ctx) spacesAt(ls int) int {
	i := ls
	for i < len(c.Src) && c.Src[i] == ' ' {
		i++
	}
	return i - ls
}

// stripFix returns the repair needed when text is appended after node n and the
// source has no final line break: a block scalar that ends the file then gains
// the break the new text starts with, so its header becomes "strip" to keep its
// value. It returns nil when nothing is needed.
func (c *Ctx) stripFix(n int32, at int) []Repl {
	if at < len(c.Src) || len(c.Src) == 0 || isBreak(c.Src[len(c.Src)-1]) {
		return nil
	}
	// The last node of the subtree in document order that has text; an entry
	// without a value ends with an empty node after the scalar.
	n = c.lastIndex(n)
	for c.T.Nodes[n].Kind == cst.Scalar && c.T.Nodes[n].Start == c.T.Nodes[n].End && n > 1 {
		n--
	}
	nd := &c.T.Nodes[n]
	if !isBlockScalarNode(nd) || c.blockChomp(n) == '-' || int(nd.End) != len(c.Src) {
		return nil
	}
	// Trailing blank-looking text after the last content line (a space with no
	// break) means that line already has its break; the new text only adds a
	// blank line, which a clipped scalar drops.
	if c.blockTextEnd(n) < int(nd.End) {
		return nil
	}
	he := int(nd.Start) + 1
	for he < int(nd.End) && (c.Src[he] == '-' || c.Src[he] == '+' || c.Src[he] >= '1' && c.Src[he] <= '9') {
		he++
	}
	hdr := []byte{}
	for i := int(nd.Start) + 1; i < he; i++ {
		if c.Src[i] >= '1' && c.Src[i] <= '9' {
			hdr = append(hdr, c.Src[i])
		}
	}
	return []Repl{{Start: int(nd.Start) + 1, End: he, Text: append(hdr, '-')}}
}

// Add plans the insertion of v below the container at loc.Parent: as the new
// mapping member loc.Tok, or as the item at loc.Index of a sequence. loc.Node must be 0.
func (c *Ctx) Add(loc Loc, v *Value, opt Options) ([]Repl, error) {
	if err := valueDepth(v); err != nil {
		return nil, err
	}
	par := loc.Parent
	if par == 0 {
		return nil, ErrNotFound
	}
	pn := &c.T.Nodes[par]
	switch pn.Kind {
	case cst.Mapping:
		if pn.Pair {
			return nil, wrap(ErrUnsupported, "adding to a single-pair flow mapping")
		}
		return c.addMember(par, loc.Tok, v, opt), nil
	case cst.Sequence:
		return c.addItem(par, loc.Index, v, opt), nil
	}
	return nil, notFound("cannot add below a scalar")
}

// keyHint is the quote style of the last key of a mapping, given its last value.
func (c *Ctx) keyHint(k int32) scan.Style {
	if k != 0 {
		if s := c.T.Nodes[k].Style; c.T.Nodes[k].Kind == cst.Scalar && (s == scan.SingleQuoted || s == scan.DoubleQuoted) {
			return s
		}
	}
	return scan.Plain
}

func (c *Ctx) addMember(par int32, key string, v *Value, opt Options) []Repl {
	pn := &c.T.Nodes[par]
	count := c.T.Count(par) / 2
	at := int(pn.End)
	if c.T.First(par) != 0 {
		at = int(c.T.First(par))
	}
	l := c.newLayout(at, opt)
	l.hint = c.styleHint(par, count)
	// The last key and value.
	last := c.T.Last(par)
	lastKey := c.T.Prev(last)
	l.keyH = c.keyHint(lastKey)
	k := Str(key)
	if pn.Flow {
		l.flow = true
		var text []byte
		if longKey(&k) {
			text = append(text, "? "...)
			text = l.stringText(text, k.Text, l.keyH, true, false)
			text = append(text, " : "...)
		} else {
			text = l.scalarText(text, &k, true, true)
			text = append(text, ": "...)
		}
		text = l.scalarOrFlow(text, v, false)
		if count == 0 {
			return []Repl{{Start: int(pn.Start) + 1, End: int(pn.End) - 1, Text: text}}
		}
		end := int(c.T.Nodes[c.T.Last(par)].End)
		return []Repl{{Start: end, End: end, Text: append([]byte(", "), text...)}}
	}
	c.discover(l, par)
	// Copy the shape of the nearest sibling collection: flow if it is flow.
	if last != 0 && c.T.Nodes[last].Flow {
		l.flow = true
	}
	first := c.T.First(par)
	col := c.col(c.entryStart(first))
	ins := c.insertAfter(last)
	l.nl = c.nlAt(max(ins-1, 0))
	text := l.entry(nil, &k, v, col, true)
	return append(c.stripFix(last, ins), Repl{Start: ins, End: ins, Text: c.lines(text, ins, l.nl)})
}

func (c *Ctx) addItem(par int32, idx int, v *Value, opt Options) []Repl {
	pn := &c.T.Nodes[par]
	count := c.T.Count(par)
	l := c.newLayout(int(pn.Start), opt)
	l.hint = c.styleHint(par, idx)
	if pn.Flow {
		l.flow = true
		text := l.scalarOrFlow(nil, v, false)
		switch {
		case count == 0:
			return []Repl{{Start: int(pn.Start) + 1, End: int(pn.End) - 1, Text: text}}
		case idx >= count:
			end := int(c.T.Nodes[c.T.Last(par)].End)
			return []Repl{{Start: end, End: end, Text: append([]byte(", "), text...)}}
		}
		it := c.nth(par, idx)
		at := int(c.T.FullStart(it))
		return []Repl{{Start: at, End: at, Text: append(text, ", "...)}}
	}
	c.discover(l, c.T.Nodes[par].Parent)
	ref := c.nth(par, min(idx, count-1))
	ds := c.dashOf(ref)
	l.gap = 1
	if fs := int(c.T.FullStart(ref)); fs > ds+1 && !containsBreak(c.Src[ds:fs]) {
		l.gap = fs - ds - 1
	}
	col := c.col(ds)
	if idx >= count {
		ins := c.insertAfter(c.T.Last(par))
		l.nl = c.nlAt(max(ins-1, 0))
		text := l.item(nil, v, col, true)
		return append(c.stripFix(c.T.Last(par), ins), Repl{Start: ins, End: ins, Text: c.lines(text, ins, l.nl)})
	}
	ds = c.dashOf(c.nth(par, idx))
	l.nl = c.nlAt(ds)
	if c.onlyBlankBefore(ds) {
		ins := c.ownedStart(c.lineStart(ds), c.floorOf(c.nth(par, idx)))
		return []Repl{{Start: ins, End: ins, Text: l.item(nil, v, col, true)}}
	}
	// The item shares its line with the parent's indicator: "- - a".
	text := l.item(nil, v, col, false)
	text = append(text, make([]byte, 0)...)
	for i := 0; i < col; i++ {
		text = append(text, ' ')
	}
	return []Repl{{Start: ds, End: ds, Text: text}}
}

func containsBreak(b []byte) bool {
	for _, c := range b {
		if isBreak(c) {
			return true
		}
	}
	return false
}

// deeperComment reports whether the line starting at at holds only a comment that
// is indented by more than col columns.
func (c *Ctx) deeperComment(at, col int) bool {
	i := at
	for i < len(c.Src) && c.Src[i] == ' ' {
		i++
	}
	return i-at > col && i < len(c.Src) && c.Src[i] == '#'
}

// nth returns the idx-th child of par.
func (c *Ctx) nth(par int32, idx int) int32 {
	ch := c.T.First(par)
	for ; idx > 0 && ch != 0; idx-- {
		ch = c.T.Nodes[ch].Next
	}
	return ch
}

// Remove plans the removal of the node at loc from its container.
func (c *Ctx) Remove(loc Loc) ([]Repl, error) {
	n := loc.Node
	if n == 0 {
		return nil, ErrNotFound
	}
	par := loc.Parent
	if par == 0 {
		return nil, wrap(ErrUnsupported, "removing the document root")
	}
	if err := c.anchorsInUse(n, true); err != nil {
		return nil, err
	}
	if loc.Key != 0 {
		// The key goes too; an alias may refer to an anchor on it.
		if err := c.anchorsInUse(loc.Key, true); err != nil {
			return nil, err
		}
	}
	pn := &c.T.Nodes[par]
	first, last := n, n // the first node of the entry and its last node
	entries := c.T.Count(par)
	if pn.Kind == cst.Mapping {
		first = loc.Key
		entries /= 2
	}
	if entries == 1 {
		empty := "[]"
		if pn.Kind == cst.Mapping {
			empty = "{}"
		}
		if pn.Pair {
			// "[a: 0]" has no braces to empty: the pair itself becomes "{}".
			return []Repl{{Start: int(pn.Start), End: int(pn.End), Text: []byte("{}")}}, nil
		}
		if pn.Flow {
			return []Repl{{Start: int(pn.Start) + 1, End: int(pn.End) - 1}}, nil
		}
		l := c.newLayout(int(pn.Start), Options{})
		return c.inlineReplace(par, empty, l), nil
	}
	next := c.T.Nodes[last].Next
	if pn.Flow {
		if next != 0 {
			return []Repl{{Start: int(c.T.FullStart(first)), End: int(c.T.FullStart(next))}}, nil
		}
		prev := c.T.Prev(first)
		return []Repl{{Start: int(c.T.Nodes[prev].End), End: int(c.T.Nodes[last].End)}}, nil
	}
	s0 := c.entryStart(first)
	if c.onlyBlankBefore(s0) {
		from := c.ownedStart(c.lineStart(s0), c.floorOf(first))
		to := c.insertAfter(last)
		// Comment lines right below the entry and indented more than its key belong
		// to it. Left behind they would follow the previous entry, and after a
		// block scalar they would become part of its text.
		for col := c.col(s0); to < len(c.Src) && c.deeperComment(to, col); {
			to = c.afterBreak(to)
		}
		if prev := c.T.Prev(first); prev != 0 && c.endsWithKeep(prev) {
			// A keep-chomped block scalar owns the blank lines after it. Blank
			// lines that now follow it would change its value, so they go too.
			for to < len(c.Src) && c.blankLine(to) {
				to = c.afterBreak(to)
			}
		} else if prev != 0 {
			// A line of spaces indented deeper than a block scalar's text is text
			// of the scalar once nothing stands between them, so it goes too.
			if ind := c.endingBlockIndent(prev); ind >= 0 {
				for to < len(c.Src) && c.blankLine(to) && c.spacesAt(to) > ind {
					to = c.afterBreak(to)
				}
			}
		}
		return []Repl{{Start: from, End: to}}, nil
	}
	// The entry shares its line with an indicator ("- a: 1"): take it up to the
	// start of the next entry so that the next one moves into its place.
	if next == 0 {
		return nil, wrap(ErrUnsupported, "removing the last entry that shares a line")
	}
	return []Repl{{Start: s0, End: c.entryStart(next)}}, nil
}
