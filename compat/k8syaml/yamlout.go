package yaml

import (
	"bytes"
	"errors"
	"math"
	"strconv"
	"unicode/utf8"
)

// The YAML writer reproduces the layout that sigs.k8s.io/yaml (through yaml.v2)
// gives JSON data, byte for byte: block style with two-space indentation,
// sequences at the indentation of their key, keys in natural order, strings
// quoted when a 1.1 reader would read them as something else, long plain and
// quoted scalars folded after column 80, and text with line breaks as literal
// blocks.

const (
	bestIndent = 2
	bestWidth  = 80
)

type scalarStyle uint8

const (
	stylePlain scalarStyle = iota
	styleSingle
	styleDouble
	styleLiteral
)

type yamlWriter struct {
	p *jparser

	out        []byte
	column     int
	whitespace bool
	indention  bool
	indent     int
	indents    []int

	// simpleKeyCtx is set while a mapping key that fits on one line is written.
	simpleKeyCtx bool
}

func floatBits(f float64) uint64 { return math.Float64bits(f) }

// writeYAML renders the parsed JSON.
func writeYAML(p *jparser, dst []byte) ([]byte, error) {
	w := &yamlWriter{p: p, out: dst, whitespace: true, indention: true, indent: -1}
	if len(p.nodes) == 0 {
		w.scalar([]byte("null"), stylePlain)
	} else if err := w.node(0); err != nil {
		return nil, err
	}
	w.writeIndent()
	return w.out, nil
}

var errYAMLDepth = errors.New("yaml: exceeded max depth of 10000")

func (w *yamlWriter) node(i int32) error {
	return w.emit(i, false)
}

// yframe is a sequence or mapping being written; the writer keeps these on a stack
// of its own so that the depth of the document is not the depth of the Go stack.
type yframe struct {
	n   *jnode
	i   int32 // next child
	seq bool
}

// emit writes node i. inMap says that it is the value of a mapping key, which
// lets a sequence sit at the key's indentation.
func (w *yamlWriter) emit(i int32, inMap bool) error {
	// Most documents are shallow: the first frames live on the Go stack.
	var small [16]yframe
	stack := small[:0]
	for {
		pushed, err := w.begin(i, inMap)
		if err != nil {
			return err
		}
		if pushed {
			n := &w.p.nodes[i]
			stack = append(stack, yframe{n: n, seq: n.kind == jSeq})
		}
		// Go on with the next child of the innermost open collection, closing the
		// ones that are finished.
		for {
			if len(stack) == 0 {
				return nil
			}
			f := &stack[len(stack)-1]
			if f.i == f.n.n {
				w.popIndent()
				stack = stack[:len(stack)-1]
				continue
			}
			if f.seq {
				i, inMap = w.p.kids[f.n.off+f.i], false
				w.writeIndent()
				w.writeIndicator("-", true, false, true)
			} else {
				pr := w.p.pairs[f.n.off+f.i]
				i, inMap = pr.v, true
				w.writeIndent()
				kn := &w.p.nodes[pr.k]
				key := w.p.str(kn)
				// A key is simple when it fits on one line and is at most 128 bytes.
				if !bytesContainsBreak(key) && len(key) <= 128 && !bytesContains(key, '\n') {
					w.simpleKeyCtx = true
					w.str(key)
					w.writeIndicator(":", false, false, false)
				} else {
					w.writeIndicator("?", true, false, true)
					w.simpleKeyCtx = false
					w.str(key)
					w.writeIndent()
					w.writeIndicator(":", true, false, true)
				}
			}
			f.i++
			break
		}
	}
}

// begin writes node i if it is a scalar or an empty collection, or opens it and
// reports true if it holds items.
func (w *yamlWriter) begin(i int32, inMap bool) (bool, error) {
	n := &w.p.nodes[i]
	w.simpleKeyCtx = false
	switch n.kind {
	case jNull:
		w.scalar([]byte("null"), stylePlain)
	case jBool:
		if n.b {
			w.scalar([]byte("true"), stylePlain)
		} else {
			w.scalar([]byte("false"), stylePlain)
		}
	case jInt:
		w.scalar(strconv.AppendInt(nil, int64(n.num), 10), stylePlain)
	case jUint:
		w.scalar(strconv.AppendUint(nil, n.num, 10), stylePlain)
	case jFloat:
		w.scalar(floatText(math.Float64frombits(n.num)), stylePlain)
	case jStr:
		w.str(w.p.str(n))
	case jSeq, jMap:
		if len(w.indents) >= maxDepth {
			return false, errYAMLDepth
		}
		if n.n == 0 {
			if n.kind == jSeq {
				w.flowEmpty('[', ']')
			} else {
				w.flowEmpty('{', '}')
			}
			return false, nil
		}
		if n.kind == jSeq {
			w.increaseIndent(false, inMap && !w.indention)
		} else {
			w.increaseIndent(false, false)
		}
		return true, nil
	}
	return false, nil
}

func floatText(f float64) []byte {
	s := strconv.AppendFloat(nil, f, 'g', -1, 64)
	switch string(s) {
	case "+Inf":
		return []byte(".inf")
	case "-Inf":
		return []byte("-.inf")
	case "NaN":
		return []byte(".nan")
	}
	return s
}

// str writes a string value. A string is plain unless a reader would take it for
// something else, and a literal block when it has a line break.
func (w *yamlWriter) str(s []byte) {
	style := stylePlain
	canPlain := true
	if v, rk := resolveText(s, tagNone); v.kind != skStr || rk != rkOther || isBase60Float(s) || isMergeKey(s) {
		canPlain = false
	}
	switch {
	case bytesContains(s, '\n'):
		style = styleLiteral
	case canPlain:
		style = stylePlain
	default:
		style = styleDouble
	}
	w.scalar(s, style)
}

func isMergeKey(s []byte) bool { return len(s) == 2 && s[0] == '<' && s[1] == '<' }

func bytesContains(s []byte, c byte) bool {
	for _, b := range s {
		if b == c {
			return true
		}
	}
	return false
}

// bytesContainsBreak reports a line break of any kind.
func bytesContainsBreak(s []byte) bool {
	for i := 0; i < len(s); i++ {
		if isBreakAt(s, i) {
			return true
		}
	}
	return false
}

func (w *yamlWriter) flowEmpty(open, closeB byte) {
	w.writeIndicator(string(open), true, true, false)
	w.writeIndicator(string(closeB), false, false, false)
}

func (w *yamlWriter) increaseIndent(flow, indentless bool) {
	w.indents = append(w.indents, w.indent)
	if w.indent < 0 {
		if flow {
			w.indent = bestIndent
		} else {
			w.indent = 0
		}
	} else if !indentless {
		w.indent += bestIndent
	}
}

func (w *yamlWriter) popIndent() {
	w.indent = w.indents[len(w.indents)-1]
	w.indents = w.indents[:len(w.indents)-1]
}

func (w *yamlWriter) putBreak() {
	w.out = append(w.out, '\n')
	w.column = 0
}

func (w *yamlWriter) put(c byte) {
	w.out = append(w.out, c)
	w.column++
}

// writeChar copies the character at s[*i] and counts one column.
func (w *yamlWriter) writeChar(s []byte, i *int) {
	n := charWidth(s[*i])
	w.out = append(w.out, s[*i:*i+n]...)
	w.column++
	*i += n
}

func charWidth(b byte) int {
	switch {
	case b&0x80 == 0:
		return 1
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	case b&0xF8 == 0xF0:
		return 4
	}
	return 1
}

// writeBreak copies a line break: "\n" becomes the break, any other break
// character is copied as it is and resets the column.
func (w *yamlWriter) writeBreak(s []byte, i *int) {
	if s[*i] == '\n' {
		w.putBreak()
		*i++
		return
	}
	w.writeChar(s, i)
	w.column = 0
}

func (w *yamlWriter) writeIndent() {
	indent := max(w.indent, 0)
	if !w.indention || w.column > indent || (w.column == indent && !w.whitespace) {
		w.putBreak()
	}
	for w.column < indent {
		w.put(' ')
	}
	w.whitespace = true
	w.indention = true
}

func (w *yamlWriter) writeIndicator(ind string, needWS, isWS, isIndention bool) {
	if needWS && !w.whitespace {
		w.put(' ')
	}
	w.out = append(w.out, ind...)
	w.column += len(ind)
	w.whitespace = isWS
	w.indention = w.indention && isIndention
}

// ---- character classes (on UTF-8 bytes) ----

func isBreakAt(b []byte, i int) bool {
	c := b[i]
	if c == '\r' || c == '\n' {
		return true
	}
	if c == 0xC2 && i+1 < len(b) && b[i+1] == 0x85 {
		return true
	}
	return c == 0xE2 && i+2 < len(b) && b[i+1] == 0x80 && (b[i+2] == 0xA8 || b[i+2] == 0xA9)
}

func isBlankAt(b []byte, i int) bool { return b[i] == ' ' || b[i] == '\t' }

func isBlankzAt(b []byte, i int) bool {
	return i >= len(b) || isBlankAt(b, i) || isBreakAt(b, i) || b[i] == 0
}

// isPrintableAt reports whether the character at b[i] may be written unescaped.
func isPrintableAt(b []byte, i int) bool {
	c := b[i]
	at := func(k int) byte {
		if i+k < len(b) {
			return b[i+k]
		}
		return 0
	}
	return c == 0x0A ||
		(c >= 0x20 && c <= 0x7E) ||
		(c == 0xC2 && at(1) >= 0xA0) ||
		(c > 0xC2 && c < 0xED) ||
		(c == 0xED && at(1) < 0xA0) ||
		c == 0xEE ||
		(c == 0xEF &&
			!(at(1) == 0xBB && at(2) == 0xBF) &&
			!(at(1) == 0xBF && (at(2) == 0xBE || at(2) == 0xBF)))
}

type scalarInfo struct {
	multiline  bool
	blockPlain bool
	single     bool
	block      bool
	// oddBreak is set when the text has a line break other than "\n" (U+2028,
	// U+2029, U+0085 or a carriage return). Only the escaped form carries those
	// through a reader that does not treat them as line breaks, or does and folds
	// them.
	oddBreak bool
}

// analyze decides which styles can hold s.
func analyze(s []byte) scalarInfo {
	var si scalarInfo
	if len(s) == 0 {
		si.blockPlain, si.single = true, true
		return si
	}
	var (
		blockInd, lineBreaks, special      bool
		leadSpace, leadBreak, trailSpace   bool
		trailBreak, breakSpace, spaceBreak bool
		precededByWS                       = true
		prevSpace, prevBreak               bool
	)
	if len(s) >= 3 && (s[0] == '-' && s[1] == '-' && s[2] == '-' || s[0] == '.' && s[1] == '.' && s[2] == '.') {
		blockInd = true
	}
	for i := 0; i < len(s); {
		w := charWidth(s[i])
		followedByWS := i+w >= len(s) || isBlankAt(s, i+w)
		if i == 0 {
			switch s[i] {
			case '#', ',', '[', ']', '{', '}', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`':
				blockInd = true
			case '?', ':', '-':
				if followedByWS {
					blockInd = true
				}
			}
		} else {
			switch s[i] {
			case ':':
				if followedByWS {
					blockInd = true
				}
			case '#':
				if precededByWS {
					blockInd = true
				}
			}
		}
		if !isPrintableAt(s, i) {
			special = true
		}
		switch {
		case s[i] == ' ':
			if i == 0 {
				leadSpace = true
			}
			if i+w == len(s) {
				trailSpace = true
			}
			if prevBreak {
				breakSpace = true
			}
			prevSpace, prevBreak = true, false
		case isBreakAt(s, i):
			lineBreaks = true
			if s[i] != '\n' {
				si.oddBreak = true
			}
			if i == 0 {
				leadBreak = true
			}
			if i+w == len(s) {
				trailBreak = true
			}
			if prevSpace {
				spaceBreak = true
			}
			prevSpace, prevBreak = false, true
		default:
			prevSpace, prevBreak = false, false
		}
		precededByWS = isBlankzAt(s, i)
		i += w
	}
	si.multiline = lineBreaks
	si.blockPlain, si.single, si.block = true, true, true
	if leadSpace || leadBreak || trailSpace || trailBreak {
		si.blockPlain = false
	}
	if trailSpace {
		si.block = false
	}
	if breakSpace {
		si.blockPlain, si.single = false, false
	}
	if spaceBreak || special {
		si.blockPlain, si.single, si.block = false, false, false
	}
	if lineBreaks {
		si.blockPlain = false
	}
	if blockInd {
		si.blockPlain = false
	}
	return si
}

// scalar writes s in the requested style, or the nearest style that can hold it.
func (w *yamlWriter) scalar(s []byte, style scalarStyle) {
	si := analyze(s)
	if w.simpleKeyCtx && si.multiline || si.oddBreak {
		style = styleDouble
	}
	if style == stylePlain {
		if !si.blockPlain {
			style = styleSingle
		}
		if len(s) == 0 && w.simpleKeyCtx {
			style = styleSingle
		}
	}
	if style == styleSingle && !si.single {
		style = styleDouble
	}
	if style == styleLiteral && (!si.block || w.simpleKeyCtx) {
		style = styleDouble
	}
	// A block scalar at the root that starts with a space or a line break needs an
	// indentation indicator, and readers disagree on what it counts from at the
	// root (the specification counts from -1, libyaml from 0). The quoted form is
	// read the same everywhere.
	if style == styleLiteral && w.indent < 0 && len(s) > 0 && (s[0] == ' ' || isBreakAt(s, 0)) && len(bytes.Trim(s, "\n")) > 0 {
		style = styleDouble
	}
	w.increaseIndent(true, false)
	allowBreaks := !w.simpleKeyCtx
	switch style {
	case stylePlain:
		w.writePlain(s, allowBreaks)
	case styleSingle:
		w.writeSingle(s, allowBreaks)
	case styleDouble:
		w.writeDouble(s, allowBreaks)
	case styleLiteral:
		w.writeLiteral(s)
	}
	w.popIndent()
}

func (w *yamlWriter) writePlain(s []byte, allowBreaks bool) {
	if !w.whitespace {
		w.put(' ')
	}
	spaces, breaks := false, false
	for i := 0; i < len(s); {
		switch {
		case s[i] == ' ':
			if allowBreaks && !spaces && w.column > bestWidth && !(i+1 < len(s) && s[i+1] == ' ') {
				w.writeIndent()
				i++
			} else {
				w.writeChar(s, &i)
			}
			spaces = true
		case isBreakAt(s, i):
			if !breaks && s[i] == '\n' {
				w.putBreak()
			}
			w.writeBreak(s, &i)
			w.indention = true
			breaks = true
		default:
			if breaks {
				w.writeIndent()
			}
			w.writeChar(s, &i)
			w.indention = false
			spaces, breaks = false, false
		}
	}
	w.whitespace = false
	w.indention = false
}

func (w *yamlWriter) writeSingle(s []byte, allowBreaks bool) {
	w.writeIndicator("'", true, false, false)
	spaces, breaks := false, false
	for i := 0; i < len(s); {
		switch {
		case s[i] == ' ':
			if allowBreaks && !spaces && w.column > bestWidth && i > 0 && i < len(s)-1 && s[i+1] != ' ' {
				w.writeIndent()
				i++
			} else {
				w.writeChar(s, &i)
			}
			spaces = true
		case isBreakAt(s, i):
			if !breaks && s[i] == '\n' {
				w.putBreak()
			}
			w.writeBreak(s, &i)
			w.indention = true
			breaks = true
		default:
			if breaks {
				w.writeIndent()
			}
			if s[i] == '\'' {
				w.put('\'')
			}
			w.writeChar(s, &i)
			w.indention = false
			spaces, breaks = false, false
		}
	}
	w.writeIndicator("'", false, false, false)
	w.whitespace = false
	w.indention = false
}

func (w *yamlWriter) writeDouble(s []byte, allowBreaks bool) {
	// A text that starts with a byte order mark is escaped throughout.
	bomStart := len(s) >= 3 && s[0] == 0xEF && s[1] == 0xBB && s[2] == 0xBF
	spaces := false
	w.writeIndicator(`"`, true, false, false)
	for i := 0; i < len(s); {
		if !isPrintableAt(s, i) || bomStart || isBreakAt(s, i) || s[i] == '"' || s[i] == '\\' {
			r, size := utf8.DecodeRune(s[i:])
			if r == utf8.RuneError && size <= 1 {
				r, size = rune(s[i]), 1
			}
			i += size
			w.put('\\')
			switch r {
			case 0x00:
				w.put('0')
			case 0x07:
				w.put('a')
			case 0x08:
				w.put('b')
			case 0x09:
				w.put('t')
			case 0x0A:
				w.put('n')
			case 0x0b:
				w.put('v')
			case 0x0c:
				w.put('f')
			case 0x0d:
				w.put('r')
			case 0x1b:
				w.put('e')
			case 0x22:
				w.put('"')
			case 0x5c:
				w.put('\\')
			case 0x85:
				w.put('N')
			case 0xA0:
				w.put('_')
			case 0x2028:
				w.put('L')
			case 0x2029:
				w.put('P')
			default:
				var digits int
				switch {
				case r <= 0xFF:
					w.put('x')
					digits = 2
				case r <= 0xFFFF:
					w.put('u')
					digits = 4
				default:
					w.put('U')
					digits = 8
				}
				for k := (digits - 1) * 4; k >= 0; k -= 4 {
					d := byte((r >> uint(k)) & 0x0F)
					if d < 10 {
						w.put(d + '0')
					} else {
						w.put(d + 'A' - 10)
					}
				}
			}
			spaces = false
		} else if s[i] == ' ' {
			if allowBreaks && !spaces && w.column > bestWidth && i > 0 && i < len(s)-1 {
				w.writeIndent()
				if i+1 < len(s) && s[i+1] == ' ' {
					w.put('\\')
				}
				i++
			} else {
				w.writeChar(s, &i)
			}
			spaces = true
		} else {
			w.writeChar(s, &i)
			spaces = false
		}
	}
	w.writeIndicator(`"`, false, false, false)
	w.whitespace = false
	w.indention = false
}

func (w *yamlWriter) writeLiteral(s []byte) {
	w.writeIndicator("|", true, false, false)
	// Hints: an indentation indicator when the text starts with a space or a
	// break, and a chomping indicator for the text's final line breaks.
	if len(s) > 0 && (s[0] == ' ' || isBreakAt(s, 0)) {
		w.writeIndicator(string(rune('0'+bestIndent)), false, false, false)
	}
	chomp := byte(0)
	if len(s) == 0 {
		chomp = '-'
	} else {
		i := len(s) - 1
		for i > 0 && s[i]&0xC0 == 0x80 {
			i--
		}
		if !isBreakAt(s, i) {
			chomp = '-'
		} else if i == 0 {
			chomp = '+'
		} else {
			i--
			for i > 0 && s[i]&0xC0 == 0x80 {
				i--
			}
			if isBreakAt(s, i) {
				chomp = '+'
			}
		}
	}
	if chomp != 0 {
		w.writeIndicator(string(rune(chomp)), false, false, false)
	}
	w.putBreak()
	w.indention = true
	w.whitespace = true
	breaks := true
	for i := 0; i < len(s); {
		if isBreakAt(s, i) {
			w.writeBreak(s, &i)
			w.indention = true
			breaks = true
		} else {
			if breaks {
				w.writeIndent()
			}
			w.writeChar(s, &i)
			w.indention = false
			breaks = false
		}
	}
}
