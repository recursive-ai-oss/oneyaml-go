package yaml

import (
	"bytes"
	"unicode/utf8"
)

// This file writes a flow collection that holds comments, the way yaml.v3's
// emitter (libyaml) does. Without comments a flow collection is written on one
// line by flowCollection; with them the line breaks, the indentation and where a
// comma goes all depend on the emitter's state, so that state is followed here:
// the column, whether the last thing written was white space or an indentation,
// the indentation of the flow collection and the comments waiting to be written.

// hasInnerComment reports whether any node below n has a comment.
func hasInnerComment(n *Node) bool {
	stack := append([]*Node(nil), n.Content...)
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if c == nil {
			continue
		}
		if c.HeadComment != "" || c.LineComment != "" || c.FootComment != "" {
			return true
		}
		stack = append(stack, c.Content...)
	}
	return false
}

type flowEm struct {
	w          *nodeWriter
	col        int
	indent     int
	footIndent int
	// topOuter is the indentation outside the outermost flow collection.
	topOuter int
	// minCol is the column a line inside the collection must start at to belong
	// to it: past the entry that holds the collection (0 at the root). The
	// emitter puts a closing bracket or a comma at the margin after a comment
	// line, which a reader refuses inside a block collection.
	minCol     int
	indention  bool
	whitespace bool
	// The comments of the events read so far that have not been written yet. A
	// new event with a comment of the same kind replaces the waiting one, as in
	// the emitter.
	head, line, foot, tail string
}

func (e *flowEm) text(s string) {
	e.w.buf = append(e.w.buf, s...)
	e.col += utf8.RuneCountInString(s)
}

// sync brings the column up to date after the writer appended from offset start.
func (e *flowEm) sync(start int) {
	seg := e.w.buf[start:]
	if i := bytes.LastIndexByte(seg, '\n'); i >= 0 {
		e.col = utf8.RuneCount(seg[i+1:])
	} else {
		e.col += utf8.RuneCount(seg)
	}
}

func (e *flowEm) putBreak() {
	e.w.buf = append(e.w.buf, '\n')
	e.col = 0
	e.indention = true
}

func (e *flowEm) writeIndent() {
	ind := max(e.indent, 0)
	if !e.indention || e.col > ind || (e.col == ind && !e.whitespace) {
		e.putBreak()
	}
	if e.footIndent == ind {
		e.putBreak()
	}
	for e.col < max(ind, e.minCol) {
		e.text(" ")
	}
	e.whitespace = true
	e.footIndent = -1
}

func (e *flowEm) indicator(s string, needWS, isWS, isInd bool) {
	if e.col == 0 {
		for e.col < e.minCol {
			e.text(" ")
		}
	}
	if needWS && !e.whitespace {
		e.text(" ")
	}
	e.text(s)
	e.whitespace = isWS
	e.indention = e.indention && isInd
}

func (e *flowEm) writeComment(c string) {
	breaks, pound := false, false
	for i := 0; i < len(c); {
		r, sz := utf8.DecodeRuneInString(c[i:])
		if isBreak(r) {
			i += sz
			if r == '\r' && i < len(c) && c[i] == '\n' {
				i++
			}
			e.putBreak()
			breaks, pound = true, false
			continue
		}
		if breaks {
			e.writeIndent()
		}
		if !pound {
			if c[i] != '#' {
				e.text("# ")
			}
			pound = true
		}
		e.text(c[i : i+sz])
		i += sz
		e.indention = false
		breaks = false
	}
	if !breaks {
		e.putBreak()
	}
	e.whitespace = true
}

func (e *flowEm) processHead() {
	if e.tail != "" {
		e.writeIndent()
		e.writeComment(e.tail)
		e.tail = ""
		e.footIndent = max(e.indent, 0)
	}
	if e.head == "" {
		return
	}
	e.writeIndent()
	e.writeComment(e.head)
	e.head = ""
}

func (e *flowEm) processLine() {
	if e.line == "" {
		return
	}
	if !e.whitespace {
		e.text(" ")
	}
	e.writeComment(e.line)
	e.line = ""
}

func (e *flowEm) processFoot() {
	if e.foot == "" {
		return
	}
	e.writeIndent()
	e.writeComment(e.foot)
	e.foot = ""
	e.footIndent = max(e.indent, 0)
}

func (e *flowEm) pending() bool { return e.line != "" || e.foot != "" || e.tail != "" }

func (e *flowEm) analyze(h, l, f, t string) {
	if h != "" {
		e.head = h
	}
	if l != "" {
		e.line = l
	}
	if f != "" {
		e.foot = f
	}
	if t != "" {
		e.tail = t
	}
}

// startEvent takes in the comments the start event of n carries: a collection's
// line and foot comments belong to its end event, and an alias has no place for
// a tail comment.
func (e *flowEm) startEvent(n *Node, tail string) {
	switch n.Kind {
	case SequenceNode:
		e.analyze(n.HeadComment, "", "", "")
	case MappingNode:
		e.analyze(n.HeadComment, "", "", tail)
	case AliasNode:
		e.analyze(n.HeadComment, n.LineComment, n.FootComment, "")
	default:
		e.analyze(n.HeadComment, n.LineComment, n.FootComment, tail)
	}
}

func isCollection(n *Node) bool { return n.Kind == SequenceNode || n.Kind == MappingNode }

// child writes the start of n, runs after (which the emitter does before the
// first item of a collection) and then writes the items of a collection.
func (e *flowEm) child(n *Node, ik bool, after func()) {
	w := e.w
	if w.err != nil {
		return
	}
	if !isCollection(n) {
		if !e.whitespace {
			e.text(" ")
		}
		if e.col == 0 {
			for e.col < e.minCol {
				e.text(" ")
			}
		}
		start := len(w.buf)
		w.node(n, npos{m: nFlow, ik: ik, col: e.indent}, "")
		e.sync(start)
		e.whitespace, e.indention = false, false
		if after != nil {
			after()
		}
		return
	}
	w.depth++
	defer func() { w.depth-- }()
	if w.depth > maxNodeDepth {
		w.fail("node is nested too deeply to write")
		return
	}
	if n.Anchor != "" && !validAnchor(n.Anchor) {
		w.fail("anchor value must contain alphanumerical characters only")
		return
	}
	tag, _ := nodeTag(n)
	if n.Anchor != "" || tag != "" {
		if !e.whitespace {
			e.text(" ")
		}
		start := len(w.buf)
		w.props(n, tag)
		e.sync(start)
		e.whitespace, e.indention = true, false
	}
	if after != nil {
		after()
	}
	e.body(n, false)
}

// body writes the items of the flow collection n between its brackets. A
// collection that starts the flow (top) already has its indentation.
func (e *flowEm) body(n *Node, top bool) {
	seq := n.Kind == SequenceNode
	open, closeCh := "{", "}"
	if seq {
		open, closeCh = "[", "]"
	}
	e.indicator(open, true, true, false)
	outer := e.indent
	if top {
		outer = e.topOuter
	} else {
		if e.indent < 0 {
			e.indent = e.w.step
		} else {
			e.indent = e.w.nested(e.indent)
		}
	}
	first, trail := true, false
	if seq {
		for _, it := range n.Content {
			if e.w.err != nil {
				return
			}
			e.startEvent(it, "")
			if !first && !trail {
				e.indicator(",", false, false, false)
			}
			e.processHead()
			if e.col == 0 {
				e.writeIndent()
			}
			trail = e.pending() && !isCollection(it)
			e.child(it, false, func() {
				// A collection's comma comes after its closing bracket: before its
				// opening one it would end the entry early.
				if e.pending() && !isCollection(it) {
					e.indicator(",", false, false, false)
				}
				e.processLine()
				e.processFoot()
			})
			first = false
		}
		if !top {
			e.analyze("", n.LineComment, n.FootComment, "")
		}
	} else {
		tail := ""
		for i := 0; i+1 < len(n.Content); i += 2 {
			if e.w.err != nil {
				return
			}
			k, v := n.Content[i], n.Content[i+1]
			foot := k.FootComment
			kk := k
			if foot != "" {
				c := *k
				c.FootComment = ""
				kk = &c
			}
			e.startEvent(kk, tail)
			if !first && !trail {
				e.indicator(",", false, false, false)
			}
			e.processHead()
			if e.col == 0 {
				e.writeIndent()
			}
			simple := simpleKey(k)
			if simple {
				e.child(kk, true, nil)
			} else {
				e.indicator("?", true, false, false)
				e.child(kk, false, nil)
			}
			e.startEvent(v, "")
			e.indicator(":", !simple, false, false)
			trail = e.pending() && !isCollection(v)
			e.child(v, false, func() {
				if e.pending() && !isCollection(v) {
					e.indicator(",", false, false, false)
				}
				e.processLine()
				e.processFoot()
			})
			first = false
			tail = foot
		}
		// A foot comment on the mapping makes the emitter write a comma after the
		// last entry, even when the foot comment is written by the parent.
		e.analyze("", n.LineComment, n.FootComment, tail)
		if (e.head != "" || e.foot != "" || e.tail != "") && !first && !trail {
			e.indicator(",", false, false, false)
		}
		if top {
			// The parent writes the comments of the outermost collection.
			e.line, e.foot = "", ""
		}
		e.processHead()
	}
	e.indent = outer
	if seq && e.col == 0 {
		e.writeIndent()
	}
	e.indicator(closeCh, false, false, false)
	e.processLine()
	e.processFoot()
}

// flowIndent is the indentation yaml.v3 gives a flow collection written at p.
func (w *nodeWriter) flowIndent(p npos) int {
	switch {
	case p.m == nItem && !p.key:
		return p.col + 2
	case p.m == nRoot:
		return w.step
	}
	return w.nested(p.col)
}

// flowCommented writes the flow collection n, which has comments below it.
func (w *nodeWriter) flowCommented(n *Node, p npos, tag string) {
	w.begin(p)
	e := &flowEm{w: w, whitespace: true, footIndent: -1, indent: w.flowIndent(p), topOuter: p.col}
	if p.m == nRoot {
		e.topOuter = -1
	} else {
		e.minCol = p.col + 1
	}
	if i := bytes.LastIndexByte(w.buf, '\n'); i >= 0 {
		e.col = utf8.RuneCount(w.buf[i+1:])
	} else {
		e.col = utf8.RuneCount(w.buf)
	}
	if n.Anchor != "" || tag != "" {
		start := len(w.buf)
		w.props(n, tag)
		e.sync(start)
	}
	e.body(n, true)
	if w.err != nil {
		return
	}
	w.lineComment(n, p)
	w.end(p)
}
