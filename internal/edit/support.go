package edit

// SameNumber reports whether the scalar texts a and b, both of kind k (Bool, Int or
// Float), have the same value under the Core schema: 0x1F and 31 are the same
// integer, 1 and 1.0 are not compared here because their kinds differ.
func SameNumber(a, b []byte, k Kind) bool { return sameNumber(a, b, k) }

// AnchorOf returns the node an alias node refers to: the nearest earlier node of
// the same document that carries the alias's anchor name. It returns 0 if there
// is none.
func (c *Ctx) AnchorOf(alias int32) int32 { return c.anchorOf(alias) }

// DocEnd returns one past the last node index of the document.
func (c *Ctx) DocEnd() int32 {
	if c.Doc+1 < len(c.T.Docs) {
		return c.T.Docs[c.Doc+1].Root
	}
	return int32(len(c.T.Nodes))
}

// LastDescendant returns the highest node index inside the subtree of n,
// n itself when it has no children. Nodes are stored in document order, so a
// subtree is the run of nodes that starts at n and ends before the first node
// that starts at or after n's end.
func (c *Ctx) LastDescendant(n int32) int32 {
	last := n
	if c.T.First(n) == 0 {
		return last
	}
	end := c.DocEnd()
	for i := n + 1; i < end; i++ {
		// A node of the subtree has n as an ancestor.
		p := c.T.Nodes[i].Parent
		for p > n {
			p = c.T.Nodes[p].Parent
		}
		if p != n {
			break
		}
		last = i
	}
	return last
}

// EntryLo returns where the lines of the block entry that starts with node first
// begin: the start of its line when only blanks precede the entry, otherwise (an
// item that shares its line with the indicator of its parent) the entry itself.
func (c *Ctx) EntryLo(first int32) int {
	s := c.entryStart(first)
	if c.onlyBlankBefore(s) {
		return c.lineStart(s)
	}
	return s
}

// EntryHi returns the end of the last line of the entry whose last node is last,
// after its line break.
func (c *Ctx) EntryHi(last int32) int {
	e := int(c.T.Nodes[last].End)
	if e > int(c.bom) && e <= len(c.Src) && isBreak(c.Src[e-1]) {
		return e
	}
	return c.afterBreak(e)
}

// LineStart returns the offset where the line holding offset i begins.
func (c *Ctx) LineStart(i int) int { return c.lineStart(i) }

// Col returns the column of offset i.
func (c *Ctx) Col(i int) int { return c.col(i) }

// EntryCol returns the column of the block entry that starts with node first.
func (c *Ctx) EntryCol(first int32) int { return c.col(c.entryStart(first)) }

// GapEnd returns the start of the first line at or after from, a line start, that
// holds anything but white space or a comment; len(Src) if there is none.
func (c *Ctx) GapEnd(from int) int {
	i := from
	for i < len(c.Src) {
		if !c.blankLine(i) && !c.commentOnly(i) {
			return i
		}
		next := c.afterBreak(i)
		if next <= i {
			break
		}
		i = next
	}
	return len(c.Src)
}

// EndsWithKeep reports whether the last leaf of n is a block scalar that keeps its
// trailing blank lines.
func (c *Ctx) EndsWithKeep(n int32) bool { return c.endsWithKeep(n) }
