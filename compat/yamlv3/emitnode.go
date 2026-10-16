package yaml

import (
	"bytes"
	"errors"
	"strings"
	"unicode/utf8"

	"oneyaml.dev/go/internal/emit"
)

func nodeOf(v interface{}) (*Node, bool) {
	switch n := v.(type) {
	case *Node:
		return n, n != nil
	case Node:
		return &n, true
	}
	return nil, false
}

// marshalNode writes n as a document.
func marshalNode(n *Node) ([]byte, error) { return renderNode(n, 4, false) }

func (e *Encoder) encodeNode(n *Node) error {
	if e.closed {
		return errors.New("yaml: expected STREAM-START")
	}
	indent := e.indent
	switch {
	case indent == 0:
		indent = 4
	case indent < 2 || indent > 9:
		indent = 2
	}
	out, err := renderNode(n, indent, e.compact)
	if err != nil {
		return err
	}
	if e.docs > 0 {
		out = append([]byte("---\n"), out...)
	}
	e.docs++
	_, err = e.w.Write(out)
	return err
}

// maxNodeDepth bounds the nesting of a node that is written.
const maxNodeDepth = 10000

func renderNode(n *Node, step int, compact bool) ([]byte, error) {
	w := &nodeWriter{step: step, compact: compact, footCol: -1}
	if n == nil || n.Kind == 0 && n.IsZero() {
		return []byte("null\n"), nil
	}
	if err := checkTree(n); err != nil {
		return nil, err
	}
	if n.Kind == DocumentNode {
		w.document(n)
	} else {
		w.node(n, npos{m: nRoot}, "")
		w.endLine()
	}
	if w.err != nil {
		return nil, w.err
	}
	return w.buf, nil
}

type nmode uint8

const (
	nRoot nmode = iota
	nKey        // after "key:"
	nItem       // after "- ", "? " or ": "
	nFlow
	nKeyText // an implicit key in a block mapping: text only
)

type npos struct {
	m   nmode
	col int
	key bool // after "? " or the ": " of an explicit entry
	ik  bool // an implicit key
}

func (p npos) inline() bool { return p.m == nFlow || p.m == nKeyText }

type nodeWriter struct {
	buf     []byte
	step    int
	compact bool
	depth   int
	err     error
	// footCol is the column of the foot comment just written, or -1: the next
	// entry written at that column is set off from it by a blank line.
	footCol int
	// emptyNext is set while an empty plain scalar is written: it has no text, so
	// it gets no space before it.
	emptyNext bool
	// keepEnd is set when the last thing written is a block scalar with keep
	// chomping.
	keepEnd bool
}

func (w *nodeWriter) fail(msg string) {
	if w.err == nil {
		w.err = errors.New("yaml: " + msg)
	}
}

func (w *nodeWriter) nested(col int) int { return w.step * ((col + w.step) / w.step) }

// entryIndent starts a line for an entry at col.
func (w *nodeWriter) entryIndent(col int) {
	if w.footCol == col && !w.keepEnd {
		w.buf = append(w.buf, '\n')
	}
	w.footCol = -1
	w.indent(col)
}

// footComment writes a foot comment at col.
func (w *nodeWriter) footComment(col int, text string) {
	w.comment(col, text)
	w.footCol = col
}

func (w *nodeWriter) indent(col int) {
	for i := 0; i < col; i++ {
		w.buf = append(w.buf, ' ')
	}
}

// atLineStart reports whether the buffer ends with a line break (or is empty).
func (w *nodeWriter) atLineStart() bool { return len(w.buf) == 0 || w.buf[len(w.buf)-1] == '\n' }

func (w *nodeWriter) endLine() {
	if !w.atLineStart() {
		w.buf = append(w.buf, '\n')
	}
}

// comment writes comment text as comment lines at column col, each starting on
// a line of its own; a line that does not start with "#" gets "# " in front.
func (w *nodeWriter) comment(col int, text string) {
	w.endLine()
	// Like an entry, a comment at the column of a foot comment just written is
	// set off from it by a blank line.
	if w.footCol == col && !w.keepEnd {
		w.buf = append(w.buf, '\n')
	}
	w.footCol = -1
	breaks, pound, wrote := true, false, false
	for i := 0; i < len(text); {
		r, sz := utf8.DecodeRuneInString(text[i:])
		if isBreak(r) {
			if w.keepEnd && !wrote {
				// A blank line before the first comment line would be part of a
				// keep-chomped scalar's text.
				i += sz
				if r == '\r' && i < len(text) && text[i] == '\n' {
					i++
				}
				continue
			}
			w.buf = append(w.buf, '\n')
			breaks, pound = true, false
			i += sz
			if r == '\r' && i < len(text) && text[i] == '\n' {
				i++
			}
			continue
		}
		if breaks {
			w.indent(col)
			breaks = false
		}
		wrote = true
		if !pound {
			if text[i] != '#' {
				w.buf = append(w.buf, '#', ' ')
			}
			pound = true
		}
		w.buf = append(w.buf, text[i:i+sz]...)
		i += sz
	}
	if wrote {
		w.keepEnd = false
	}
	w.endLine()
}

// inlineHead writes the head comment of the first entry of a collection that
// starts on the line of its parent's "- " (the buffer ends with it): the comment
// follows the dash and the entry starts on a line of its own at col, as yaml.v3
// writes it. Writing the comment before the dash, as for any other entry, would
// cut the parent's dash off from the collection.
func (w *nodeWriter) inlineHead(col int, text string) {
	for i, l := range splitComment(text) {
		if i > 0 {
			w.buf = append(w.buf, '\n')
			w.indent(col)
		}
		if l != "" {
			w.buf = appendHashed(w.buf, l)
		}
	}
	w.buf = append(w.buf, '\n')
	w.indent(col)
	w.footCol = -1
}

func (w *nodeWriter) document(d *Node) {
	if d.HeadComment != "" {
		w.comment(0, d.HeadComment)
		w.buf = append(w.buf, '\n')
	}
	for _, c := range d.Content {
		w.node(c, npos{m: nRoot}, "")
		w.endLine()
	}
	if d.FootComment != "" {
		if !w.keepEnd && w.footCol != 0 {
			// A blank line sets the comment off from the content, unless the
			// content is a keep-chomped block scalar, which would take it. After a
			// foot comment at column 0, comment writes the blank line itself.
			w.buf = append(w.buf, '\n')
		}
		w.comment(0, d.FootComment)
	}
}

// props computes what the writer says about the node's tag, as yaml.v3 does:
// a tag that the value implies anyway is dropped, a string that would read as
// something else is forced into quotes.
func nodeTag(n *Node) (tag string, forceQuoting bool) {
	tag = n.Tag
	stag := shortTag(tag)
	if tag != "" && n.Style&TaggedStyle == 0 {
		if n.Kind == ScalarNode {
			if stag == strTag && n.Style&(SingleQuotedStyle|DoubleQuotedStyle|LiteralStyle|FoldedStyle) != 0 {
				tag = ""
			} else if rtag := resolveTag(n.Value); rtag == stag {
				tag = ""
			} else if stag == strTag {
				tag, forceQuoting = "", true
			}
		} else {
			rtag := ""
			switch n.Kind {
			case MappingNode:
				rtag = mapTag
			case SequenceNode:
				rtag = seqTag
			}
			if rtag == stag {
				tag = ""
			}
		}
	}
	return tag, forceQuoting
}

// tagText renders a tag for the output: "!!str" for the core tags, "!local" as
// it is, and a verbatim "!<...>" when no shorthand can say it. Characters outside
// the URI set are percent-escaped, as libyaml does; so are the flow indicators in
// a shorthand, which would end the tag when it is read again.
func tagText(tag string) string {
	long := longTag(tag)
	switch {
	case strings.HasPrefix(long, longTagPrefix) && len(long) > len(longTagPrefix):
		return "!!" + escapeTag(long[len(longTagPrefix):], true)
	case long == "!":
		return "!"
	case strings.HasPrefix(long, "!") && len(long) > 1:
		return "!" + escapeTag(long[1:], true)
	}
	// A shorthand needs a suffix, so the empty suffix of the core prefix, and any
	// tag that is not local, is written in full.
	return "!<" + escapeTag(long, false) + ">"
}

func escapeTag(s string, shorthand bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == '-',
			strings.IndexByte(";/?:@&=+$.~*'()", c) >= 0,
			!shorthand && strings.IndexByte(",[]", c) >= 0:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte("0123456789ABCDEF"[c>>4])
			b.WriteByte("0123456789ABCDEF"[c&15])
		}
	}
	return b.String()
}

// isBreak reports whether r ends a line in YAML text as libyaml reads it, which is
// more than the line feed and carriage return of YAML 1.2.
func isBreak(r rune) bool {
	return r == '\n' || r == '\r' || r == 0x85 || r == 0x2028 || r == 0x2029
}

// validAnchor reports whether s can be written as an anchor or alias name: the
// letters, digits, "_" and "-" that yaml.v3's writer accepts.
func validAnchor(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func (w *nodeWriter) props(n *Node, tag string) {
	if n.Anchor != "" {
		w.buf = append(w.buf, '&')
		w.buf = append(w.buf, n.Anchor...)
		w.buf = append(w.buf, ' ')
	}
	if tag != "" {
		w.buf = append(w.buf, tagText(tag)...)
		w.buf = append(w.buf, ' ')
	}
}

// begin writes the space between "key:" and a scalar, when there is one.
func (w *nodeWriter) begin(p npos) {
	if p.m == nKey && !w.emptyNext {
		w.buf = append(w.buf, ' ')
	}
}

func (w *nodeWriter) end(p npos) {
	if !p.inline() {
		w.buf = append(w.buf, '\n')
	}
}

// node writes n at position p. tail is a comment that belongs before it.
func (w *nodeWriter) node(n *Node, p npos, tail string) {
	if w.err != nil {
		return
	}
	w.depth++
	defer func() { w.depth-- }()
	if w.depth > maxNodeDepth {
		w.fail("node is nested too deeply to write")
		return
	}
	if n.Kind == 0 && n.IsZero() {
		w.begin(p)
		w.buf = append(w.buf, "null"...)
		w.end(p)
		return
	}
	if n.Anchor != "" && !validAnchor(n.Anchor) {
		w.fail("anchor value must contain alphanumerical characters only")
		return
	}
	if n.Kind == AliasNode {
		switch {
		case n.Value == "":
			w.fail("alias value must not be empty")
			return
		case !validAnchor(n.Value):
			w.fail("alias value must contain alphanumerical characters only")
			return
		}
	}
	if p.m == nRoot && n.HeadComment != "" {
		w.comment(0, n.HeadComment)
	}
	tag, force := nodeTag(n)
	switch n.Kind {
	case AliasNode:
		w.begin(p)
		w.buf = append(w.buf, '*')
		w.buf = append(w.buf, n.Value...)
		w.lineComment(n, p)
		w.end(p)
	case ScalarNode:
		w.scalar(n, p, tag, force)
	case SequenceNode, MappingNode:
		w.collection(n, p, tag)
	default:
		w.fail("cannot encode node with unknown kind " + itoa(int(n.Kind)))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	neg := i < 0
	if neg {
		i = -i
	}
	for ; i > 0; i /= 10 {
		n--
		b[n] = byte('0' + i%10)
	}
	if neg {
		n--
		b[n] = '-'
	}
	return string(b[n:])
}

// lineComment writes the node's line comment after what is on the line.
func (w *nodeWriter) lineComment(n *Node, p npos) {
	if n.LineComment == "" || p.inline() {
		return
	}
	w.buf = appendLineComment(w.buf, n.LineComment, p.col)
}

// appendLineComment writes a line comment after what is on the line. The first
// line of the comment stays there; each further line is a comment line of its own
// at column col (after a blank line when that is the left margin), as yaml.v3
// writes it. Every line starts with "#", so no line of the text can be read as
// anything but a comment.
func appendLineComment(dst []byte, text string, col int) []byte {
	lines := splitComment(text)
	if len(lines) == 0 {
		return dst
	}
	if lines[0] != "" {
		dst = append(dst, ' ')
		dst = appendHashed(dst, lines[0])
	}
	for i, l := range lines[1:] {
		dst = append(dst, '\n')
		if i == 0 && col == 0 {
			dst = append(dst, '\n')
		}
		if l == "" {
			continue
		}
		for k := 0; k < col; k++ {
			dst = append(dst, ' ')
		}
		dst = appendHashed(dst, l)
	}
	return dst
}

func appendHashed(dst []byte, line string) []byte {
	if line[0] != '#' {
		dst = append(dst, '#', ' ')
	}
	return append(dst, line...)
}

// splitComment splits comment text at its line breaks and drops the empty lines
// at the end.
func splitComment(text string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(text); {
		r, sz := utf8.DecodeRuneInString(text[i:])
		if !isBreak(r) {
			i += sz
			continue
		}
		lines = append(lines, text[start:i])
		i += sz
		if r == '\r' && i < len(text) && text[i] == '\n' {
			i++
		}
		start = i
	}
	lines = append(lines, text[start:])
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func (w *nodeWriter) scalar(n *Node, p npos, tag string, force bool) {
	w.keepEnd = false
	value := n.Value
	binary := false
	if !utf8.ValidString(value) {
		if shortTag(tag) == binaryTag || shortTag(n.Tag) == binaryTag {
			w.fail("explicitly tagged !!binary data must be base64-encoded")
			return
		}
		if shortTag(tag) != "" {
			w.fail("cannot marshal invalid UTF-8 data as " + shortTag(tag))
			return
		}
		tag, value, binary = binaryTag, encodeBase64(value), true
	}
	req := emit.ScalarPlain
	switch {
	case n.Style&DoubleQuotedStyle != 0:
		req = emit.ScalarDouble
	case n.Style&SingleQuotedStyle != 0:
		req = emit.ScalarSingle
	case n.Style&LiteralStyle != 0:
		req = emit.ScalarLiteral
	case n.Style&FoldedStyle != 0:
		req = emit.ScalarFolded
	case strings.Contains(value, "\n"):
		req = emit.ScalarLiteral
	case force:
		req = emit.ScalarDouble
	}
	w.emptyNext = value == "" && tag == "" && n.Anchor == "" && req == emit.ScalarPlain && !p.ik && p.m != nFlow
	w.begin(p)
	w.emptyNext = false
	w.props(n, tag)
	style := emit.SelectScalarV3(value, req, p.m == nFlow, p.ik)
	if value == "" && req == emit.ScalarPlain && !p.ik && p.m != nFlow {
		// Nothing follows the properties of an empty plain scalar.
		style = emit.ScalarPlain
		if (n.Anchor != "" || tag != "") && w.buf[len(w.buf)-1] == ' ' {
			w.buf = w.buf[:len(w.buf)-1]
		}
	}
	_ = binary
	switch style {
	case emit.ScalarPlain:
		w.buf = append(w.buf, value...)
	case emit.ScalarSingle:
		w.buf = w.appendSingle(value, w.scalarCol(p))
	case emit.ScalarDouble:
		w.buf = emit.AppendDoubleV3(w.buf, value)
	default:
		col, parent := w.scalarCol(p), p.col
		if p.m == nRoot {
			parent = -1
		}
		start := len(w.buf)
		w.buf = emit.AppendBlockV3(w.buf, value, style == emit.ScalarFolded, col, parent)
		// With keep chomping ("|+") every blank line after the text belongs to the
		// scalar, so nothing may add one before what follows.
		hdr := w.buf[start:]
		if i := bytes.IndexByte(hdr, '\n'); i >= 0 {
			hdr = hdr[:i]
		}
		w.keepEnd = bytes.IndexByte(hdr, '+') >= 0
		w.lineCommentBlock(n, start, max(parent, 0))
		return
	}
	w.lineComment(n, p)
	w.end(p)
}

// scalarCol is the column that the continuation lines of a scalar written at p
// start in.
func (w *nodeWriter) scalarCol(p npos) int {
	switch {
	case p.m == nItem && !p.key:
		return p.col + 2
	case p.m == nRoot:
		return w.nested(0)
	}
	return w.nested(p.col)
}

// appendSingle writes s in single quotes. A line break in the text is written as
// a blank line followed by the break, the way libyaml folds a quoted scalar, and
// continuation lines start at col.
func (w *nodeWriter) appendSingle(s string, col int) []byte {
	dst := append(w.buf, '\'')
	breaks := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' {
			if !breaks {
				dst = append(dst, '\n')
			}
			dst = append(dst, '\n')
			breaks = true
			continue
		}
		if breaks {
			for k := 0; k < col; k++ {
				dst = append(dst, ' ')
			}
			breaks = false
		}
		if c == '\'' {
			dst = append(dst, '\'')
		}
		dst = append(dst, c)
	}
	if breaks {
		// The closing quote is a continuation line too: it must be indented, as
		// libyaml does.
		for k := 0; k < col; k++ {
			dst = append(dst, ' ')
		}
	}
	return append(dst, '\'')
}

// lineCommentBlock puts the line comment of a block scalar after its header: the
// scalar was just appended to the buffer from offset start. parentCol is the column
// of the entry that holds the scalar.
func (w *nodeWriter) lineCommentBlock(n *Node, start, parentCol int) {
	if n.LineComment == "" {
		return
	}
	i := start
	for i < len(w.buf) && w.buf[i] != '\n' {
		i++
	}
	lines := splitComment(n.LineComment)
	if len(lines) == 0 {
		return
	}
	// Only the first line can stand on the header line. Comment lines between the
	// header and the text would be part of the text, so the others follow the
	// scalar, at the column of the line that holds its key, where they end it.
	var head []byte
	if lines[0] != "" {
		head = appendHashed([]byte{' '}, lines[0])
	}
	w.buf = append(w.buf[:i], append(head, w.buf[i:]...)...)
	w.endLine()
	for _, l := range lines[1:] {
		if l == "" && w.keepEnd {
			// A blank line after a keep-chomped scalar would be part of its text.
			continue
		}
		if l != "" {
			w.indent(parentCol)
			w.buf = appendHashed(w.buf, l)
		}
		w.buf = append(w.buf, '\n')
	}
}

func (w *nodeWriter) collection(n *Node, p npos, tag string) {
	seq := n.Kind == SequenceNode
	if len(n.Content) == 0 {
		w.begin(p)
		w.props(n, tag)
		if seq {
			w.buf = append(w.buf, "[]"...)
		} else {
			w.buf = append(w.buf, "{}"...)
		}
		w.lineComment(n, p)
		w.end(p)
		return
	}
	flow := n.Style&FlowStyle != 0 || p.m == nFlow
	if flow {
		w.flowCollection(n, p, tag, seq)
		return
	}
	// A block collection: after "key:" it starts on the next line, after "- " on
	// the same line.
	hasProps := n.Anchor != "" || tag != ""
	switch p.m {
	case nKey:
		if hasProps {
			w.begin(p)
			w.props(n, tag)
			w.buf = w.buf[:len(w.buf)-1]
		}
		w.lineComment(n, p)
		w.buf = append(w.buf, '\n')
	case nItem:
		if hasProps {
			w.props(n, tag)
			w.buf = w.buf[:len(w.buf)-1]
			w.buf = append(w.buf, '\n')
		}
	}
	var col int
	inline := false
	switch {
	case p.m == nKey:
		col = w.nested(p.col)
		if seq && w.compact {
			col -= 2
		}
	case p.m == nItem && !hasProps:
		col, inline = p.col+2, true
		if p.key {
			col = w.nested(p.col)
			for k := col - (p.col + 2); k > 0; k-- {
				w.buf = append(w.buf, ' ')
			}
		}
	case p.m == nItem:
		col = p.col + 2
	default:
		if hasProps {
			w.props(n, tag)
			w.buf = w.buf[:len(w.buf)-1]
			w.buf = append(w.buf, '\n')
		}
		col = 0
	}
	if seq {
		w.blockSeq(n, col, inline)
	} else {
		w.blockMap(n, col, inline)
	}
	if n.FootComment != "" {
		w.footComment(col, n.FootComment)
	}
}

func (w *nodeWriter) blockSeq(n *Node, col int, inline bool) {
	for i, it := range n.Content {
		if w.err != nil {
			return
		}
		if it.HeadComment != "" {
			if i == 0 && inline {
				w.inlineHead(col, it.HeadComment)
			} else {
				w.comment(col, it.HeadComment)
			}
		}
		if !(i == 0 && inline) {
			w.entryIndent(col)
		}
		w.buf = append(w.buf, '-')
		if !emptyPlain(it) {
			w.buf = append(w.buf, ' ')
		}
		head := it.HeadComment
		it.HeadComment = ""
		w.node(it, npos{m: nItem, col: col}, "")
		it.HeadComment = head
		if it.FootComment != "" {
			w.footComment(col, it.FootComment)
		}
	}
}

// simpleKey reports whether a key can be written inline: a scalar or alias on
// one line of at most 128 bytes.
func simpleKey(k *Node) bool {
	// The tag is part of the key's text; an implicit key must fit in 128 bytes
	// (libyaml's rule, well inside the reader's limit of 1024 characters).
	if tag, _ := nodeTag(k); tag != "" && len(tagText(tag))+1+len(k.Anchor) > 128 {
		return false
	}
	switch k.Kind {
	case ScalarNode:
		if !utf8.ValidString(k.Value) {
			return len(encodeBase64(k.Value))+8 <= 128 && !strings.Contains(encodeBase64(k.Value), "\n")
		}
		return len(k.Value)+len(k.Anchor) <= 128 && !emit.MultilineV3(k.Value) &&
			k.Style&(LiteralStyle|FoldedStyle) == 0
	case AliasNode:
		return len(k.Value) <= 128
	case SequenceNode, MappingNode:
		return len(k.Content) == 0 && len(k.Anchor) <= 128
	}
	return false
}

func (w *nodeWriter) blockMap(n *Node, col int, inline bool) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if w.err != nil {
			return
		}
		k, v := n.Content[i], n.Content[i+1]
		if k.HeadComment != "" {
			if i == 0 && inline {
				w.inlineHead(col, k.HeadComment)
			} else {
				w.comment(col, k.HeadComment)
			}
		}
		if !(i == 0 && inline) {
			w.entryIndent(col)
		}
		head := k.HeadComment
		k.HeadComment = ""
		var vp npos
		if simpleKey(k) {
			w.node(k, npos{m: nKeyText, ik: true}, "")
			w.buf = append(w.buf, ':')
			vp = npos{m: nKey, col: col}
		} else {
			w.buf = append(w.buf, '?', ' ')
			w.node(k, npos{m: nItem, col: col, key: true}, "")
			w.endLine()
			w.indent(col)
			w.buf = append(w.buf, ':')
			if !emptyPlain(v) {
				w.buf = append(w.buf, ' ')
			}
			vp = npos{m: nItem, col: col, key: true}
		}
		k.HeadComment = head
		// A line comment on a key is written at the end of the line the key is on,
		// which is after the value when the value is a scalar, and after the colon
		// when it starts on a later line.
		if k.LineComment != "" && v.LineComment == "" && vp.m == nKey {
			vv := *v
			vv.LineComment = k.LineComment
			w.node(&vv, vp, "")
		} else {
			w.node(v, vp, "")
		}
		if v.Kind == ScalarNode && v.HeadComment != "" {
			// yaml.v3 has no place before a value for its head comment: it writes it
			// below the line, at the column of the key.
			w.comment(col, v.HeadComment)
		}
		if k.FootComment != "" {
			w.footComment(col, k.FootComment)
		}
		if v.FootComment != "" && v.Kind != ScalarNode {
			w.footComment(col, v.FootComment)
		}
	}
}

func (w *nodeWriter) flowCollection(n *Node, p npos, tag string, seq bool) {
	w.begin(p)
	w.props(n, tag)
	open, closeCh := byte('{'), byte('}')
	if seq {
		open, closeCh = '[', ']'
	}
	w.buf = append(w.buf, open)
	fp := npos{m: nFlow}
	if seq {
		for i, it := range n.Content {
			if i > 0 {
				w.buf = append(w.buf, ',', ' ')
			}
			w.node(it, fp, "")
		}
	} else {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if i > 0 {
				w.buf = append(w.buf, ',', ' ')
			}
			k, v := n.Content[i], n.Content[i+1]
			if simpleKey(k) {
				w.node(k, npos{m: nFlow, ik: true}, "")
				w.buf = append(w.buf, ':', ' ')
			} else {
				w.buf = append(w.buf, '?', ' ')
				w.node(k, fp, "")
				w.buf = append(w.buf, " : "...)
			}
			w.node(v, fp, "")
		}
	}
	w.buf = append(w.buf, closeCh)
	w.lineComment(n, p)
	w.end(p)
}

// Encode encodes value v and stores its representation in n.
//
// See the documentation for Marshal for details about the conversion of Go
// values into YAML.
func (n *Node) Encode(v interface{}) error {
	out, err := Marshal(v)
	if err != nil {
		return err
	}
	var doc Node
	if err := Unmarshal(out, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 {
		return errors.New("yaml: no content")
	}
	root := doc.Content[0]
	clearPositions(root)
	*n = *root
	return nil
}

// clearPositions removes the line and column numbers and comments from a tree
// built from text that was just written, which have no meaning.
func clearPositions(root *Node) {
	seen := map[*Node]bool{}
	stack := []*Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		n.Line, n.Column = 0, 0
		stack = append(stack, n.Content...)
	}
}

// emptyPlain reports whether n is an empty plain scalar, which is written as
// nothing at all.
func emptyPlain(n *Node) bool {
	if n.Kind != ScalarNode || n.Value != "" || n.Anchor != "" || n.Style&(DoubleQuotedStyle|SingleQuotedStyle|LiteralStyle|FoldedStyle|TaggedStyle) != 0 {
		return false
	}
	tag, force := nodeTag(n)
	return tag == "" && !force
}

// maxWrittenNodes bounds the nodes a tree writes out, counting a node once for
// every place it appears: a tree that shares nodes can be small and still write an
// enormous document.
const maxWrittenNodes = 1 << 26

// maxWrittenBytes bounds the text a tree writes out in the same way, counted by the
// bytes of the scalars, anchors, tags and comments of every place a node appears.
// It is the alias-bytes limit of the core, raised to ten times the bytes of the
// tree's own nodes so that a large document that shares little is not refused.
const maxWrittenBytes = 64 << 20

type checkFrame struct {
	n     *Node
	i     int
	size  int
	bytes int
}

type checkSize struct{ nodes, bytes int }

// nodeBytes is the text a node itself contributes to the output.
func nodeBytes(n *Node) int {
	return len(n.Value) + len(n.Anchor) + len(n.Tag) + len(n.HeadComment) + len(n.LineComment) + len(n.FootComment)
}

// badComment reports the first character a comment cannot hold: one the YAML
// reader refuses (control characters other than tab, DEL, the C1 range except
// NEL, the byte order mark, U+FFFE and U+FFFF) or a byte that is not UTF-8.
func badComment(text string) bool {
	for i := 0; i < len(text); {
		r, sz := utf8.DecodeRuneInString(text[i:])
		i += sz
		switch {
		case r == utf8.RuneError && sz <= 1:
			return true
		case r < 0x20 && r != '\t' && r != '\n' && r != '\r':
			return true
		case r == 0x7f, r >= 0x80 && r < 0xa0 && r != 0x85, r == 0xfeff, r == 0xfffe, r == 0xffff:
			return true
		}
	}
	return false
}

// checkTree finds what writing a tree cannot survive: a nil node, a node that
// contains itself, a comment that would not read back, and a tree that shares
// nodes so much that it writes more than maxWrittenNodes nodes or
// maxWrittenBytes bytes. It walks without recursion.
func checkTree(root *Node) error {
	// A size of -1 marks a node whose children are being walked.
	sizes := map[*Node]checkSize{}
	unique := 0
	enter := func(n *Node) (checkFrame, error) {
		if badComment(n.HeadComment) || badComment(n.LineComment) || badComment(n.FootComment) {
			return checkFrame{}, errors.New("yaml: a comment holds a character that cannot be written in a YAML comment")
		}
		b := nodeBytes(n)
		unique += b
		return checkFrame{n: n, i: -1, bytes: b}, nil
	}
	limitBytes := func(total int) bool { return total > maxWrittenBytes && total > 10*unique }
	f0, err := enter(root)
	if err != nil {
		return err
	}
	stack := []checkFrame{f0}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.i < 0 {
			f.i = 0
			sizes[f.n] = checkSize{nodes: -1}
			f.size = 1
		}
		if f.i < len(f.n.Content) {
			c := f.n.Content[f.i]
			f.i++
			if c == nil {
				return errors.New("yaml: cannot encode a nil node")
			}
			switch sz, seen := sizes[c]; {
			case seen && sz.nodes < 0:
				return errors.New("yaml: cannot encode a node that contains itself")
			case seen:
				f.size += sz.nodes
				f.bytes += sz.bytes
			default:
				nf, err := enter(c)
				if err != nil {
					return err
				}
				stack = append(stack, nf)
			}
			if f.size > maxWrittenNodes || limitBytes(f.bytes) {
				return errors.New("yaml: node tree is too large to write")
			}
			continue
		}
		sizes[f.n] = checkSize{f.size, f.bytes}
		done, doneBytes := f.size, f.bytes
		stack = stack[:len(stack)-1]
		if len(stack) > 0 {
			stack[len(stack)-1].size += done
			stack[len(stack)-1].bytes += doneBytes
		} else if limitBytes(doneBytes) {
			return errors.New("yaml: node tree is too large to write")
		}
	}
	return nil
}
