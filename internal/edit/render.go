package edit

import (
	"strings"
	"unicode/utf8"

	"oneyaml.dev/go/internal/emit"
	"oneyaml.dev/go/internal/resolve"
	"oneyaml.dev/go/internal/scan"
)

// printable reports whether s holds only characters YAML allows in a stream
// (tab and line feed included).
func printable(s string) bool {
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			if b >= 0x20 && b < 0x7f || b == '\t' || b == '\n' {
				i++
				continue
			}
			return false
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n <= 1 {
			return false
		}
		if !(r == 0x85 || r >= 0xA0 && r <= 0xD7FF || r >= 0xE000 && r <= 0xFFFD && r != 0xFEFF || r >= 0x10000) {
			return false
		}
		i += n
	}
	return true
}

// singleOK reports whether s can be written as a single-quoted scalar on one line.
func singleOK(s string) bool {
	return printable(s) && !strings.Contains(s, "\n")
}

// plainOK reports whether s can be written as a plain scalar and read back as
// the same string under the Core schema. flow says the scalar is inside [] or {};
// key says it is a mapping key.
func plainOK(s string, flow, key bool) bool {
	n := len(s)
	if n == 0 || !singleOK(s) {
		return false
	}
	switch s[0] {
	case ' ', '\t', '#', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`', '{', '}', '[', ']', ',':
		return false
	case '-', '?', ':':
		if n == 1 || s[1] == ' ' || s[1] == '\t' {
			return false
		}
		if flow && strings.IndexByte(",[]{}", s[1]) >= 0 {
			return false
		}
	}
	switch s[n-1] {
	case ' ', '\t', ':':
		return false
	}
	if n >= 3 && (s[:3] == "---" || s[:3] == "...") {
		return false
	}
	if strings.Contains(s, ": ") || strings.Contains(s, ":\t") || strings.Contains(s, " #") || strings.Contains(s, "\t#") {
		return false
	}
	if flow && strings.ContainsAny(s, ",[]{}") {
		return false
	}
	if key && (s == "<<" || n > 1000) {
		return false
	}
	return resolve.Core([]byte(s)).Kind == resolve.String
}

// layout carries the choices an inserted value copies from its surroundings.
type layout struct {
	nl     string
	step   int        // indentation of a block mapping under a key
	seqOff int        // indentation of a block sequence under a key, from the key
	gap    int        // spaces after "-"
	hint   scan.Style // preferred style of new strings: Plain, SingleQuoted, DoubleQuoted
	keyH   scan.Style // preferred style of new keys
	flow   bool       // write collections in flow style
	safe   bool       // fallback: strings double-quoted, collections in flow style
}

func (l *layout) spaces(dst []byte, n int) []byte {
	for ; n > 0; n-- {
		dst = append(dst, ' ')
	}
	return dst
}

// stringText writes the string s as an inline scalar.
func (l *layout) stringText(dst []byte, s string, h scan.Style, flow, key bool) []byte {
	switch {
	case l.safe || h == scan.DoubleQuoted || !singleOK(s):
		return emit.AppendDouble(dst, s)
	case h == scan.SingleQuoted:
		return emit.AppendSingle(dst, s)
	case plainOK(s, flow, key):
		return append(dst, s...)
	}
	return emit.AppendSingle(dst, s)
}

// scalarText writes the scalar v on one line.
func (l *layout) scalarText(dst []byte, v *Value, flow, key bool) []byte {
	if v.Tag != "" && v.Kind != Alias {
		dst = append(append(dst, v.Tag...), ' ')
	}
	h := l.hint
	if key {
		h = l.keyH
	}
	switch v.Kind {
	case String:
		return l.stringText(dst, v.Text, h, flow, key)
	case Null:
		if v.Text == "" {
			return append(dst, "null"...)
		}
		return append(dst, v.Text...)
	case Alias:
		return append(append(dst, '*'), v.Text...)
	}
	return append(dst, v.Text...)
}

// flowText writes v in flow style.
func (l *layout) flowText(dst []byte, v *Value) []byte {
	switch v.Kind {
	case Map:
		if len(v.Items) == 0 {
			return append(dst, "{}"...)
		}
		dst = append(dst, '{')
		for i := 0; i < len(v.Items); i += 2 {
			if i > 0 {
				dst = append(dst, ", "...)
			}
			if longKey(&v.Items[i]) {
				// Explicit key: an implicit one is limited to 1024 characters.
				dst = append(dst, "? "...)
				dst = l.stringText(dst, v.Items[i].Text, l.keyH, true, false)
				dst = append(dst, " : "...)
			} else {
				dst = l.scalarOrFlow(dst, &v.Items[i], true)
				dst = append(dst, ": "...)
			}
			dst = l.scalarOrFlow(dst, &v.Items[i+1], false)
		}
		return append(dst, '}')
	case Seq:
		if len(v.Items) == 0 {
			return append(dst, "[]"...)
		}
		dst = append(dst, '[')
		for i := range v.Items {
			if i > 0 {
				dst = append(dst, ", "...)
			}
			dst = l.scalarOrFlow(dst, &v.Items[i], false)
		}
		return append(dst, ']')
	}
	return l.scalarText(dst, v, true, false)
}

func (l *layout) scalarOrFlow(dst []byte, v *Value, key bool) []byte {
	if v.Kind == Map || v.Kind == Seq {
		return l.flowText(dst, v)
	}
	return l.scalarText(dst, v, true, key)
}

// blockString reports whether the string value v is written as a block scalar in
// a block mapping value or sequence item.
func (l *layout) blockString(v *Value) bool {
	return v.Kind == String && v.Tag == "" && !l.safe && l.hint != scan.DoubleQuoted &&
		strings.Contains(v.Text, "\n") && blockOK(v.Text)
}

// blockOK reports whether s can be written as a literal or folded scalar.
func blockOK(s string) bool {
	if !printable(s) || strings.Contains(s, "\r") {
		return false
	}
	return strings.Trim(s, "\n") != ""
}

// blockBody renders the content lines of the block scalar for s. It returns the
// header indicators (chomp: '-', '+' or 0; needInd: an indentation indicator is
// required) and the content lines, each indented by indent and ended with nl.
// oldChomp is the chomping of the scalar being replaced, kept when the new value
// still fits it; pass 0 and haveOld false for a new scalar.
func blockBody(s string, folded bool, oldChomp byte, haveOld bool, indent int, nl string) (chomp byte, needInd bool, body []byte) {
	k := 0
	for k < len(s) && s[len(s)-1-k] == '\n' {
		k++
	}
	core := s[:len(s)-k]
	want := byte(0)
	switch {
	case k == 0:
		want = '-'
	case k >= 2:
		want = '+'
	}
	chomp = want
	if haveOld {
		fits := (oldChomp == '-' && k == 0) || (oldChomp == 0 && k == 1) || (oldChomp == '+' && k >= 1)
		if fits {
			chomp = oldChomp
		}
	}
	if core == "" {
		return chomp, false, nil
	}
	lines := strings.Split(core, "\n")
	for _, ln := range lines {
		if ln != "" {
			needInd = ln[0] == ' '
			break
		}
	}
	pad := strings.Repeat(" ", indent)
	var b []byte
	prev := -1 // index of the previous non-empty line
	for i, ln := range lines {
		if ln == "" {
			if folded && prev >= 0 {
				// Blank lines between text lines are written when the next text
				// line comes, since how many are needed depends on both neighbours.
				continue
			}
			b = append(b, nl...)
			continue
		}
		if folded && prev >= 0 {
			m := i - prev
			if !(ln[0] == ' ' || ln[0] == '\t' || lines[prev][0] == ' ' || lines[prev][0] == '\t') {
				// Two text lines with one break fold into a space; a blank
				// line is needed to keep the newline.
			} else {
				m--
			}
			for ; m > 0; m-- {
				b = append(b, nl...)
			}
		}
		b = append(b, pad...)
		b = append(b, ln...)
		b = append(b, nl...)
		prev = i
	}
	if chomp == '+' {
		for j := 1; j < k; j++ {
			b = append(b, nl...)
		}
	}
	return chomp, needInd, b
}

// trimNL removes one trailing line break nl.
func trimNL(b []byte, nl string) []byte {
	if len(b) >= len(nl) && string(b[len(b)-len(nl):]) == nl {
		return b[:len(b)-len(nl)]
	}
	return b
}

// block rendering --------------------------------------------------------

// entry writes the mapping entry key: val at column col, ending with a line break.
func (l *layout) entry(dst []byte, key, val *Value, col int, indentFirst bool) []byte {
	if indentFirst {
		dst = l.spaces(dst, col)
	}
	if longKey(key) && !l.flow && !l.safe {
		// An implicit key is one line of at most 1024 characters (YAML 1.2.2, 7.4.2);
		// a longer key is written as an explicit key.
		dst = append(dst, "? "...)
		dst = l.stringText(dst, key.Text, l.keyH, false, false)
		dst = append(dst, l.nl...)
		dst = l.spaces(dst, col)
		dst = append(dst, ':')
		return l.entryValue(dst, val, col)
	}
	dst = l.scalarText(dst, key, false, true)
	dst = append(dst, ':')
	return l.entryValue(dst, val, col)
}

// longKey reports whether key is too long to be written as an implicit key. The
// bound is in bytes and a little under the limit of 1024 characters, which is
// safe for any text.
func longKey(key *Value) bool { return key.Kind == String && len(key.Text) > 1000 }

func (l *layout) entryValue(dst []byte, val *Value, col int) []byte {
	switch {
	case l.blockString(val):
		return l.blockScalar(dst, val.Text, col+l.step)
	case val.Kind == Map || val.Kind == Seq:
		if len(val.Items) == 0 || l.flow || l.safe {
			dst = append(dst, ' ')
			dst = l.flowText(dst, val)
			return append(dst, l.nl...)
		}
		dst = append(dst, l.nl...)
		if val.Kind == Map {
			return l.mapping(dst, val.Items, col+l.step, true)
		}
		return l.sequence(dst, val.Items, col+l.seqOff, true)
	}
	dst = append(dst, ' ')
	dst = l.scalarText(dst, val, false, false)
	return append(dst, l.nl...)
}

// blockScalar writes " |" and the lines of s indented by indent.
func (l *layout) blockScalar(dst []byte, s string, indent int) []byte {
	chomp, needInd, body := blockBody(s, false, 0, false, indent, l.nl)
	dst = append(dst, ' ', '|')
	if needInd {
		dst = append(dst, byte('0'+max(1, min(9, l.step))))
	}
	if chomp != 0 {
		dst = append(dst, chomp)
	}
	dst = append(dst, l.nl...)
	return append(dst, body...)
}

func (l *layout) mapping(dst []byte, items []Value, col int, indentFirst bool) []byte {
	for i := 0; i < len(items); i += 2 {
		dst = l.entry(dst, &items[i], &items[i+1], col, indentFirst || i > 0)
	}
	return dst
}

// item writes one sequence item at column col, ending with a line break.
func (l *layout) item(dst []byte, v *Value, col int, indentFirst bool) []byte {
	if indentFirst {
		dst = l.spaces(dst, col)
	}
	dst = append(dst, '-')
	gap := max(l.gap, 1)
	switch {
	case l.blockString(v):
		// Content must be indented more than the dash.
		dst = l.blockScalar(dst, v.Text, col+max(l.step, 2))
		return dst
	case (v.Kind == Map || v.Kind == Seq) && len(v.Items) > 0 && !l.flow && !l.safe:
		dst = l.spaces(dst, gap)
		if v.Kind == Map {
			return l.mapping(dst, v.Items, col+1+gap, false)
		}
		return l.sequence(dst, v.Items, col+1+gap, false)
	case v.Kind == Map || v.Kind == Seq:
		dst = l.spaces(dst, gap)
		dst = l.flowText(dst, v)
	default:
		dst = l.spaces(dst, gap)
		dst = l.scalarText(dst, v, false, false)
	}
	return append(dst, l.nl...)
}

func (l *layout) sequence(dst []byte, items []Value, col int, indentFirst bool) []byte {
	for i := range items {
		dst = l.item(dst, &items[i], col, indentFirst || i > 0)
	}
	return dst
}

// valueDepth returns an error if v nests deeper than MaxValueDepth.
func valueDepth(v *Value) error {
	type fr struct {
		v *Value
		d int
	}
	st := []fr{{v, 1}}
	for len(st) > 0 {
		f := st[len(st)-1]
		st = st[:len(st)-1]
		if f.d > MaxValueDepth {
			return ErrDepth
		}
		for i := range f.v.Items {
			c := &f.v.Items[i]
			if c.Kind == Map || c.Kind == Seq {
				st = append(st, fr{c, f.d + 1})
			}
		}
	}
	return nil
}

// RenderRoot writes v as the content of a document, ending with a line break nl.
// It is used to give a stream without any document its first one.
func RenderRoot(v *Value, nl string, safe bool) []byte {
	l := &layout{nl: nl, step: 2, seqOff: 2, gap: 1, safe: safe}
	switch {
	case (v.Kind == Map || v.Kind == Seq) && len(v.Items) > 0 && !safe:
		if v.Kind == Map {
			return l.mapping(nil, v.Items, 0, true)
		}
		return l.sequence(nil, v.Items, 0, true)
	case v.Kind == Map || v.Kind == Seq:
		return append(l.flowText(nil, v), nl...)
	}
	return append(l.scalarText(nil, v, false, false), nl...)
}
