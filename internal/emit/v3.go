package emit

import (
	"bytes"
	"encoding/base64"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"oneyaml.dev/go/internal/resolve"
)

// This file holds the writing rules of DialectV3: how yaml.v3 (through libyaml)
// chooses a style for a string, escapes it, orders keys and formats floats.

// printableV3 reports whether libyaml writes r unescaped: line feed, printable
// ASCII, and the BMP outside the C1 controls, surrogates and the three
// non-characters it excludes. Anything beyond the BMP is escaped.
func printableV3(r rune) bool {
	switch {
	case r == 0x0A, r >= 0x20 && r <= 0x7E:
		return true
	case r >= 0xA0 && r <= 0xD7FF:
		return true
	case r >= 0xE000 && r <= 0xFFFD && r != 0xFEFF:
		return true
	}
	return false
}

func isBreakV3(r rune) bool {
	return r == '\n' || r == '\r' || r == 0x85 || r == 0x2028 || r == 0x2029
}

// scalarFacts is what libyaml learns about a scalar before choosing its style.
type scalarFacts struct {
	multiline                         bool
	flowPlain, blockPlain, single, bl bool
}

// analyzeV3 mirrors libyaml's scalar analysis; s is valid UTF-8.
func analyzeV3(s string) scalarFacts {
	if s == "" {
		return scalarFacts{multiline: false, flowPlain: false, blockPlain: true, single: true, bl: false}
	}
	var blockInd, flowInd, lineBreaks, special, tabs bool
	var leadSpace, leadBreak, trailSpace, trailBreak, breakSpace, spaceBreak bool
	var prevSpace, prevBreak bool
	if len(s) >= 3 && (s[:3] == "---" || s[:3] == "...") {
		blockInd, flowInd = true, true
	}
	preceded := true
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		followed := i+w >= len(s)
		if !followed {
			n, _ := utf8.DecodeRuneInString(s[i+w:])
			followed = n == ' ' || n == '\t' || isBreakV3(n)
		}
		if i == 0 {
			switch r {
			case '#', ',', '[', ']', '{', '}', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`':
				flowInd, blockInd = true, true
			case '?', ':':
				flowInd = true
				if followed {
					blockInd = true
				}
			case '-':
				if followed {
					flowInd, blockInd = true, true
				}
			}
		} else {
			switch r {
			case ',', '?', '[', ']', '{', '}':
				flowInd = true
			case ':':
				flowInd = true
				if followed {
					blockInd = true
				}
			case '#':
				if preceded {
					flowInd, blockInd = true, true
				}
			}
		}
		if r == '\t' {
			tabs = true
		} else if !printableV3(r) {
			special = true
		}
		switch {
		case r == ' ':
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
		case isBreakV3(r):
			lineBreaks = true
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
		preceded = r == ' ' || r == '\t' || isBreakV3(r)
		i += w
	}
	f := scalarFacts{multiline: lineBreaks, flowPlain: true, blockPlain: true, single: true, bl: true}
	if leadSpace || leadBreak || trailSpace || trailBreak {
		f.flowPlain, f.blockPlain = false, false
	}
	if trailSpace {
		f.bl = false
	}
	if breakSpace {
		f.flowPlain, f.blockPlain, f.single = false, false, false
	}
	if spaceBreak || tabs || special {
		f.flowPlain, f.blockPlain, f.single = false, false, false
	}
	if spaceBreak || special {
		f.bl = false
	}
	if lineBreaks {
		f.flowPlain, f.blockPlain = false, false
	}
	if flowInd {
		f.flowPlain = false
	}
	if blockInd {
		f.blockPlain = false
	}
	return f
}

// plainOKV3 reports whether yaml.v3 may leave s unquoted: it must read back as a
// string, and not look like a YAML 1.1 boolean or a base-60 number.
func plainOKV3(s string) bool {
	var buf [64]byte
	var kind resolve.Kind
	if len(s) <= len(buf) {
		kind = resolve.V3(append(buf[:0], s...)).Kind
	} else {
		kind = resolve.V3([]byte(s)).Kind
	}
	if kind != resolve.String {
		return false
	}
	switch s {
	case "y", "Y", "yes", "Yes", "YES", "on", "On", "ON", "n", "N", "no", "No", "NO", "off", "Off", "OFF":
		return false
	}
	return !isBase60V3(s)
}

// isBase60V3 matches ^[-+]?[0-9][0-9_]*(:[0-5]?[0-9])+(\.[0-9_]*)?$
func isBase60V3(s string) bool {
	if s == "" || strings.IndexByte(s, ':') < 0 {
		return false
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i++
	}
	if i >= len(s) || s[i] < '0' || s[i] > '9' {
		return false
	}
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '_') {
		i++
	}
	groups := 0
	for i < len(s) && s[i] == ':' {
		i++
		n := 0
		if i < len(s) && s[i] >= '0' && s[i] <= '5' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
			i += 2
			n = 2
		} else if i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			n = 1
		}
		if n == 0 {
			return false
		}
		groups++
	}
	if groups == 0 {
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '_') {
			i++
		}
	}
	return i == len(s)
}

// styleV3 picks the style yaml.v3 writes s in. flow is true inside [] or {}; key
// is true for an implicit key (which is never multi-line). The second result is
// true when s is not valid UTF-8 and must be written as !!binary.
func styleV3(s string, flow, key bool) (st style, binary bool) {
	st, binary, _ = styleV3m(s, flow, key)
	return
}

// styleV3m is styleV3 that also reports whether s holds a line break of any
// kind, which keeps it from being an implicit key.
func styleV3m(s string, flow, key bool) (st style, binary, multiline bool) {
	if !utf8.ValidString(s) {
		return stBinary, true, false
	}
	req := stDouble
	switch {
	case strings.IndexByte(s, '\n') >= 0:
		if !flow {
			req = stLiteral
		}
	case plainOKV3(s):
		req = stPlain
	}
	f := analyzeV3(s)
	multiline = f.multiline
	if key && f.multiline {
		req = stDouble
	}
	if req == stPlain {
		if flow && !f.flowPlain || !flow && !f.blockPlain {
			req = stSingle
		}
		if s == "" && (flow || key) {
			req = stSingle
		}
	}
	if req == stSingle && !f.single {
		req = stDouble
	}
	if req == stLiteral && (!f.bl || flow || key) {
		req = stDouble
	}
	return req, false, multiline
}

func appendDoubleV3(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	// libyaml tests for a byte order mark at the start of the buffer rather than at
	// the current character, so in a string that starts with one it escapes every
	// character. The result reads back the same, and is kept for identical output.
	bom := strings.HasPrefix(s, "\uFEFF")
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		if !bom && printableV3(r) && r != '\n' && r != '"' && r != '\\' {
			i += w
			continue
		}
		dst = append(dst, s[start:i]...)
		dst = append(dst, '\\')
		switch r {
		case 0:
			dst = append(dst, '0')
		case 7:
			dst = append(dst, 'a')
		case 8:
			dst = append(dst, 'b')
		case 9:
			dst = append(dst, 't')
		case 10:
			dst = append(dst, 'n')
		case 0x0b:
			dst = append(dst, 'v')
		case 0x0c:
			dst = append(dst, 'f')
		case 0x0d:
			dst = append(dst, 'r')
		case 0x1b:
			dst = append(dst, 'e')
		case '"':
			dst = append(dst, '"')
		case '\\':
			dst = append(dst, '\\')
		case 0x85:
			dst = append(dst, 'N')
		case 0xA0:
			dst = append(dst, '_')
		case 0x2028:
			dst = append(dst, 'L')
		case 0x2029:
			dst = append(dst, 'P')
		default:
			n := 8
			switch {
			case r <= 0xFF:
				dst = append(dst, 'x')
				n = 2
			case r <= 0xFFFF:
				dst = append(dst, 'u')
				n = 4
			default:
				dst = append(dst, 'U')
			}
			for k := (n - 1) * 4; k >= 0; k -= 4 {
				dst = append(dst, hexDigits[(r>>uint(k))&15])
			}
		}
		i += w
		start = i
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// strV3 writes a string scalar the way yaml.v3 does.
func (e *Encoder) strV3(p pos, s string) {
	st, bin := styleV3(s, p.m == mFlow, false)
	if st == stLiteral && p.m == mRoot && s != "" && (s[0] == ' ' || s[0] == '\n' || s[0] == '\r') {
		// libyaml reads the indicator of a top-level block scalar from column 0;
		// the specification reads it from -1. No digit suits both, so quote it.
		st = stDouble
	}
	e.writeStrV3(p, s, st, bin)
}

func (e *Encoder) writeStrV3(p pos, s string, st style, bin bool) {
	e.begin(p)
	switch {
	case bin:
		e.buf = append(e.buf, "!!binary "...)
		enc := encodeBase64V3(s)
		if strings.IndexByte(enc, '\n') >= 0 {
			e.literalV3(p, enc)
			return
		}
		e.buf = append(e.buf, enc...)
	case st == stPlain:
		e.buf = append(e.buf, s...)
	case st == stSingle:
		e.buf = appendSingle(e.buf, s)
	case st == stDouble:
		e.buf = appendDoubleV3(e.buf, s)
	case st == stLiteral:
		e.literalV3(p, s)
		return
	}
	e.end(p)
}

// encodeBase64V3 breaks the base64 text into lines of 70 characters, as yaml.v3
// does for long binary data.
func encodeBase64V3(s string) string {
	const lineLen = 70
	enc := base64.StdEncoding.EncodeToString([]byte(s))
	if len(enc) < lineLen {
		return enc
	}
	var b strings.Builder
	for i := 0; i < len(enc); i += lineLen {
		j := min(i+lineLen, len(enc))
		b.WriteString(enc[i:j])
		b.WriteByte('\n')
	}
	return b.String()
}

// literalV3 writes s as a literal block scalar with libyaml's header hints. The
// indentation indicator, when s starts with a space or a break, is the real
// distance from the parent's indent to the text: libyaml always writes the
// indent step there, which is wrong (the output cannot be read back) wherever
// the text is not exactly one step in.
func (e *Encoder) literalV3(p pos, s string) {
	col := e.nested(p.col)
	parent := p.col
	if p.m == mItem && !p.key {
		col = p.col + 2
	}
	if p.m == mRoot {
		parent = -1
	}
	e.buf = AppendBlockV3(e.buf, s, false, col, parent)
}

// AppendBlockV3 appends s as a literal (or, with folded set, folded) block
// scalar whose text is indented to column col under a parent indented at parent
// (-1 at the top level). The header carries libyaml's chomping indicator and,
// when s starts with a space or a break, the real indentation distance.
func AppendBlockV3(dst []byte, s string, folded bool, col, parent int) []byte {
	if folded {
		dst = append(dst, '>')
	} else {
		dst = append(dst, '|')
	}
	if s != "" && (s[0] == ' ' || s[0] == '\n' || s[0] == '\r') {
		if h := col - parent; h >= 1 && h <= 9 {
			dst = append(dst, byte('0'+h))
		}
	}
	switch n := len(s); {
	case n == 0 || s[n-1] != '\n':
		dst = append(dst, '-')
	case n == 1 || s[n-2] == '\n':
		dst = append(dst, '+')
	}
	dst = append(dst, '\n')
	indent := func() {
		for i := 0; i < col; i++ {
			dst = append(dst, ' ')
		}
	}
	atLineStart := true
	leadingSpaces := true
	// libyaml looks at the start of the whole value rather than after the break
	// it writes. For a value that starts with text it therefore also writes a
	// blank line before the final break of a clipped value. Clip chomping drops
	// trailing blank lines, so the text still reads back; the same bytes are kept.
	lead := false
	if folded {
		k := 0
		for k < len(s) && (s[k] == '\n' || s[k] == '\r') {
			k++
		}
		lead = k < len(s) && s[k] != ' ' && s[k] != '\t'
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' {
			clipEnd := lead && i == len(s)-1
			if folded && !atLineStart && !leadingSpaces && (clipEnd || foldNeedsBlank(s, i)) {
				// Folding turns a single break into a space, so a break that
				// stays one is written as a blank line.
				dst = append(dst, '\n')
			}
			dst = append(dst, '\n')
			atLineStart = true
			continue
		}
		if atLineStart {
			indent()
			leadingSpaces = c == ' ' || c == '\t'
			atLineStart = false
		}
		dst = append(dst, c)
	}
	if !atLineStart {
		dst = append(dst, '\n')
	}
	return dst
}

// foldNeedsBlank reports whether the break at s[i], which ends a line that does
// not start with white space, needs an extra blank line in a folded scalar: it
// does when the text after the run of breaks starts with a character that is
// neither white space nor the end of the value. Breaks before an indented line
// are not folded, and a trailing run is kept by the chomping indicator.
func foldNeedsBlank(s string, i int) bool {
	k := i
	for k < len(s) && (s[k] == '\n' || s[k] == '\r') {
		k++
	}
	return k < len(s) && s[k] != ' ' && s[k] != '\t'
}

// appendKeyV3 appends s as an implicit key: a single line of at most 128 bytes.
func appendKeyV3(dst []byte, s string, flow bool) ([]byte, bool) {
	if len(s) > 128 {
		return dst, false
	}
	if s == "<<" {
		// Plain, yaml.v3 itself reads this key as a merge key and refuses the result.
		return append(dst, "'<<'"...), true
	}
	st, bin, multi := styleV3m(s, flow, true)
	if bin {
		// A binary key is simple when its tag and text are short and on one line.
		enc := encodeBase64V3(s)
		if len(enc)+8 > 128 || strings.IndexByte(enc, '\n') >= 0 {
			return dst, false
		}
		dst = append(dst, "!!binary "...)
		return append(dst, enc...), true
	}
	if multi {
		return dst, false
	}
	switch st {
	case stPlain:
		return append(dst, s...), true
	case stSingle:
		return appendSingle(dst, s), true
	}
	return appendDoubleV3(dst, s), true
}

// appendFloatV3 writes f as yaml.v3 does: Go's shortest 'g' form, with infinity
// and not-a-number spelled the YAML way.
func appendFloatV3(dst []byte, f float64, bits int) []byte {
	switch {
	case math.IsNaN(f):
		return append(dst, ".nan"...)
	case math.IsInf(f, 1):
		return append(dst, ".inf"...)
	case math.IsInf(f, -1):
		return append(dst, "-.inf"...)
	}
	return strconv.AppendFloat(dst, f, 'g', -1, bits)
}

// nested returns the column of a block collection or block scalar that belongs
// to a key written at col.
func (e *Encoder) nested(col int) int {
	if e.v3 {
		// libyaml rounds the indent to a multiple of the step.
		return e.step * ((col + e.step) / e.step)
	}
	return col + e.step
}

// lessV3 orders two map keys as yaml.v3 does: numbers and booleans by value,
// strings in natural order (runs of digits compare as numbers), and other kinds
// after them by kind.
func lessV3(a, b *mapEnt) bool {
	af, aok := a.num()
	bf, bok := b.num()
	if aok && bok {
		if af != bf {
			return af < bf
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.numLess(b)
	}
	if a.kind != reflect.String || b.kind != reflect.String {
		return a.kind < b.kind
	}
	return naturalLess(a.s, b.s)
}

func (m *mapEnt) num() (float64, bool) {
	switch m.rank {
	case rankBool:
		return float64(m.i), true
	case rankInt:
		return float64(m.i), true
	case rankUint:
		return float64(m.u), true
	case rankFloat:
		return m.f, true
	}
	return 0, false
}

func (m *mapEnt) numLess(o *mapEnt) bool {
	switch m.rank {
	case rankBool, rankInt:
		return m.i < o.i
	case rankUint:
		return m.u < o.u
	case rankFloat:
		return m.f < o.f
	}
	return false
}

// naturalLess compares two strings the way yaml.v3 sorts string keys.
func naturalLess(a, b string) bool {
	var abuf, bbuf [64]rune
	ar, br := toRunes(abuf[:0], a), toRunes(bbuf[:0], b)
	digits := false
	for i := 0; i < len(ar) && i < len(br); i++ {
		if ar[i] == br[i] {
			digits = unicode.IsDigit(ar[i])
			continue
		}
		al, bl := unicode.IsLetter(ar[i]), unicode.IsLetter(br[i])
		if al && bl {
			return ar[i] < br[i]
		}
		if al || bl {
			if digits {
				return al
			}
			return bl
		}
		var an, bn int64
		if ar[i] == '0' || br[i] == '0' {
			for j := i - 1; j >= 0 && unicode.IsDigit(ar[j]); j-- {
				if ar[j] != '0' {
					an, bn = 1, 1
					break
				}
			}
		}
		ai, bi := i, i
		for ; ai < len(ar) && unicode.IsDigit(ar[ai]); ai++ {
			an = an*10 + int64(ar[ai]-'0')
		}
		for ; bi < len(br) && unicode.IsDigit(br[bi]); bi++ {
			bn = bn*10 + int64(br[bi]-'0')
		}
		if an != bn {
			return an < bn
		}
		if ai != bi {
			return ai < bi
		}
		return ar[i] < br[i]
	}
	return len(ar) < len(br)
}

func toRunes(dst []rune, s string) []rune {
	for _, r := range s {
		dst = append(dst, r)
	}
	return dst
}

// isZeroV3 is yaml.v3's test for omitempty: an IsZero method decides when there
// is one; otherwise empty strings, nil pointers and interfaces, empty slices and
// maps, zero numbers and booleans, and structs whose exported fields are all
// zero. Arrays are never omitted.
func isZeroV3(v reflect.Value) bool {
	kind := v.Kind()
	if t := v.Type(); t.PkgPath() != "" || kind == reflect.Struct || kind == reflect.Pointer || kind == reflect.Interface {
		if z, ok := v.Interface().(interface{ IsZero() bool }); ok {
			if (kind == reflect.Pointer || kind == reflect.Interface) && v.IsNil() {
				return true
			}
			return z.IsZero()
		}
	}
	switch kind {
	case reflect.String:
		return v.Len() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Struct:
		vt := v.Type()
		for i := v.NumField() - 1; i >= 0; i-- {
			if vt.Field(i).PkgPath != "" {
				continue
			}
			if !isZeroV3(v.Field(i)) {
				return false
			}
		}
		return true
	}
	return false
}

// ScalarStyle is a requested style for a scalar written by the Node writer of
// the yaml.v3 compat package.
type ScalarStyle uint8

// The scalar styles.
const (
	ScalarPlain ScalarStyle = iota
	ScalarSingle
	ScalarDouble
	ScalarLiteral
	ScalarFolded
)

// SelectScalarV3 applies libyaml's rules to the style req asked for s: a style
// that cannot hold the text falls back to the next one that can. flow is true
// inside [] or {}, key for an implicit key. s must be valid UTF-8.
func SelectScalarV3(s string, req ScalarStyle, flow, key bool) ScalarStyle {
	f := analyzeV3(s)
	if key && f.multiline {
		req = ScalarDouble
	}
	if req == ScalarPlain {
		if flow && !f.flowPlain || !flow && !f.blockPlain {
			req = ScalarSingle
		}
		if s == "" && (flow || key) {
			req = ScalarSingle
		}
	}
	if req == ScalarSingle && !f.single {
		req = ScalarDouble
	}
	if (req == ScalarLiteral || req == ScalarFolded) && (!f.bl || flow || key) {
		req = ScalarDouble
	}
	return req
}

// MultilineV3 reports whether s contains a line break of any kind, which keeps
// it from being an implicit key.
func MultilineV3(s string) bool { return analyzeV3(s).multiline }

// AppendSingleV3 appends s in single quotes.
func AppendSingleV3(dst []byte, s string) []byte { return appendSingle(dst, s) }

// AppendDoubleV3 appends s in double quotes with libyaml's escapes.
func AppendDoubleV3(dst []byte, s string) []byte { return appendDoubleV3(dst, s) }

// PlainOKV3 reports whether yaml.v3 may leave the string s unquoted.
func PlainOKV3(s string) bool { return plainOKV3(s) }

var (
	v3NodeMatch  func(reflect.Type) bool
	v3NodeRender func(v reflect.Value, step int, compact bool) ([]byte, error)
)

// SetV3Node registers the node type of the yaml.v3 compat package: match says
// which types are nodes (the type and a pointer to it), and render writes one as
// a standalone block document ending in a newline. It must run from an init
// function.
func SetV3Node(match func(reflect.Type) bool, render func(v reflect.Value, step int, compact bool) ([]byte, error)) {
	v3NodeMatch, v3NodeRender = match, render
}

// node writes a compat node value at p by splicing in its standalone text.
func (e *Encoder) node(v reflect.Value, p pos) error {
	text, err := v3NodeRender(v, e.step, e.compact)
	if err != nil {
		return &Error{Kind: KindHook, Type: v.Type(), Detail: err.Error(), Err: err}
	}
	text = bytes.TrimSuffix(text, []byte("\n"))
	if p.m == mFlow || !bytes.Contains(text, []byte("\n")) {
		e.begin(p)
		e.buf = append(e.buf, text...)
		e.end(p)
		return nil
	}
	lines := bytes.Split(text, []byte("\n"))
	first := lines[0]
	block := len(first) > 0 && (first[0] == '|' || first[0] == '>')
	var col int
	switch {
	case block, p.m == mKey && !isCollectionStart(first):
		e.begin(p)
		col = e.nested(p.col)
		if p.m == mItem && !p.key {
			col = p.col + 2
		}
		if p.m == mRoot {
			col = 0
		}
		e.buf = append(e.buf, first...)
		e.buf = append(e.buf, '\n')
		for _, l := range lines[1:] {
			if len(l) > 0 {
				e.indent(col)
			}
			e.buf = append(e.buf, l...)
			e.buf = append(e.buf, '\n')
		}
		return nil
	case p.m == mKey:
		col = e.nested(p.col)
		e.buf = append(e.buf, '\n')
		for _, l := range lines {
			e.indent(col)
			e.buf = append(e.buf, l...)
			e.buf = append(e.buf, '\n')
		}
	case p.m == mItem:
		col = p.col + 2
		e.buf = append(e.buf, first...)
		e.buf = append(e.buf, '\n')
		for _, l := range lines[1:] {
			e.indent(col)
			e.buf = append(e.buf, l...)
			e.buf = append(e.buf, '\n')
		}
	default:
		e.buf = append(e.buf, text...)
		e.buf = append(e.buf, '\n')
	}
	return nil
}

// isCollectionStart reports whether a node's first line starts a block
// sequence or mapping rather than a scalar with properties.
func isCollectionStart(first []byte) bool {
	return bytes.HasPrefix(first, []byte("- ")) || bytes.HasPrefix(first, []byte("? ")) || bytes.Contains(first, []byte(": ")) || bytes.HasSuffix(first, []byte(":"))
}
