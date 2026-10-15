package yaml

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"unicode"
	"unicode/utf8"
)

// jkind is the kind of a node of the parsed JSON.
type jkind uint8

const (
	jNull jkind = iota
	jBool
	jStr
	jInt
	jUint
	jFloat
	jSeq
	jMap
)

// jnode is one node of the parsed JSON. Nodes live in one slice and refer to
// each other by index, so parsing allocates per document, not per value.
type jnode struct {
	kind jkind
	b    bool
	n    int32  // children: items of a sequence, entries of a map
	off  int32  // string offset in jparser.strs, or first child in kids/pairs
	len  int32  // string length
	num  uint64 // integer or float bits
}

type jpair struct{ k, v int32 }

type jparser struct {
	in     []byte
	pos    int
	nodes  []jnode
	strs   []byte
	kids   []int32
	pairs  []jpair
	kstack []int32
	pstack []jpair
	// negZeroFloat reads the text -0 as a float. The text comes from our own
	// conversion of YAML, where an integer zero is written 0, so a -0 there was
	// a float; in JSON that a caller wrote, -0 is an integer, as sigs reads it.
	negZeroFloat bool
}

var errNotJSON = errors.New("not JSON")

func (p *jparser) str(n *jnode) []byte { return p.strs[n.off : n.off+n.len] }

func (p *jparser) ws() {
	for p.pos < len(p.in) {
		switch p.in[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// parseJSON reads one JSON value that fills the whole input. Anything it does
// not accept, including text that is YAML but not JSON, is reported as
// errNotJSON so that the caller can read it as YAML instead.
func parseJSON(in []byte) (*jparser, error) { return parseJSONMode(in, false) }

func parseJSONMode(in []byte, negZeroFloat bool) (*jparser, error) {
	if !utf8.Valid(in) {
		return nil, errNotJSON
	}
	p := &jparser{in: in, negZeroFloat: negZeroFloat}
	p.ws()
	if p.pos == len(in) {
		return p, nil // empty input is null
	}
	if _, err := p.value(0); err != nil {
		return nil, err
	}
	p.ws()
	if p.pos != len(in) {
		return nil, errNotJSON
	}
	return p, nil
}

func (p *jparser) add(n jnode) int32 {
	p.nodes = append(p.nodes, n)
	return int32(len(p.nodes) - 1)
}

// jframe is an open array or object while it is read. The reader keeps these on a
// stack of its own, so the depth of the input never becomes the depth of the Go
// stack.
type jframe struct {
	idx  int32 // the node of the collection
	key  int32 // the key node of the entry being read (objects)
	base int   // where this collection's items begin on kstack or pstack
	obj  bool
}

// value reads one JSON value, whatever it holds, without recursion.
func (p *jparser) value(int) (int32, error) {
	var small [16]jframe
	stack := small[:0]
	for {
		if len(stack) > maxDepth {
			return 0, errors.New("yaml: exceeded max depth of 10000")
		}
		p.ws()
		if p.pos >= len(p.in) {
			return 0, errNotJSON
		}
		var v int32
		switch c := p.in[p.pos]; {
		case c == '{' || c == '[':
			p.pos++
			obj := c == '{'
			closer := byte(']')
			kind := jSeq
			base := len(p.kstack)
			if obj {
				closer, kind, base = '}', jMap, len(p.pstack)
			}
			idx := p.add(jnode{kind: kind})
			p.ws()
			if p.pos < len(p.in) && p.in[p.pos] == closer {
				p.pos++
				v = p.finish(jframe{idx: idx, base: base, obj: obj})
				break
			}
			f := jframe{idx: idx, base: base, obj: obj}
			if obj {
				if err := p.readKey(&f); err != nil {
					return 0, err
				}
			}
			stack = append(stack, f)
			continue
		case c == '"':
			off := len(p.strs)
			if err := p.string(); err != nil {
				return 0, err
			}
			v = p.add(jnode{kind: jStr, off: int32(off), len: int32(len(p.strs) - off)})
		case c == '-' || c >= '0' && c <= '9':
			var err error
			if v, err = p.number(); err != nil {
				return 0, err
			}
		case c == 't' && bytes.HasPrefix(p.in[p.pos:], []byte("true")):
			p.pos += 4
			v = p.add(jnode{kind: jBool, b: true})
		case c == 'f' && bytes.HasPrefix(p.in[p.pos:], []byte("false")):
			p.pos += 5
			v = p.add(jnode{kind: jBool})
		case c == 'n' && bytes.HasPrefix(p.in[p.pos:], []byte("null")):
			p.pos += 4
			v = p.add(jnode{kind: jNull})
		default:
			return 0, errNotJSON
		}
		// v is complete: add it to the collection it belongs to, and close every
		// collection it was the last item of.
		for {
			if len(stack) == 0 {
				return v, nil
			}
			f := &stack[len(stack)-1]
			if f.obj {
				p.pstack = append(p.pstack, jpair{f.key, v})
			} else {
				p.kstack = append(p.kstack, v)
			}
			p.ws()
			if p.pos >= len(p.in) {
				return 0, errNotJSON
			}
			c := p.in[p.pos]
			p.pos++
			if c == ',' {
				if f.obj {
					if err := p.readKey(f); err != nil {
						return 0, err
					}
				}
				break
			}
			if c != ']' && !f.obj || c != '}' && f.obj {
				return 0, errNotJSON
			}
			v = p.finish(*f)
			stack = stack[:len(stack)-1]
		}
	}
}

// readKey reads an object key and its colon into f.
func (p *jparser) readKey(f *jframe) error {
	p.ws()
	if p.pos >= len(p.in) || p.in[p.pos] != '"' {
		return errNotJSON
	}
	off := len(p.strs)
	if err := p.string(); err != nil {
		return err
	}
	f.key = p.add(jnode{kind: jStr, off: int32(off), len: int32(len(p.strs) - off)})
	p.ws()
	if p.pos >= len(p.in) || p.in[p.pos] != ':' {
		return errNotJSON
	}
	p.pos++
	return nil
}

// finish stores the items of a closed collection and returns its node.
func (p *jparser) finish(f jframe) int32 {
	if !f.obj {
		off := len(p.kids)
		p.kids = append(p.kids, p.kstack[f.base:]...)
		n := len(p.kstack) - f.base
		p.kstack = p.kstack[:f.base]
		p.nodes[f.idx].n, p.nodes[f.idx].off = int32(n), int32(off)
		return f.idx
	}
	win := p.pstack[f.base:]
	win = p.dedupe(win)
	sortNatural(win, p)
	off := len(p.pairs)
	p.pairs = append(p.pairs, win...)
	p.pstack = p.pstack[:f.base]
	p.nodes[f.idx].n, p.nodes[f.idx].off = int32(len(win)), int32(off)
	return f.idx
}

// dedupe keeps the last of equal keys.
func (p *jparser) dedupe(win []jpair) []jpair {
	if len(win) < 2 {
		return win
	}
	asc := true
	for i := 1; i < len(win); i++ {
		if bytes.Compare(p.str(&p.nodes[win[i-1].k]), p.str(&p.nodes[win[i].k])) >= 0 {
			asc = false
			break
		}
	}
	if asc {
		return win
	}
	slices.SortStableFunc(win, func(a, b jpair) int {
		return bytes.Compare(p.str(&p.nodes[a.k]), p.str(&p.nodes[b.k]))
	})
	out := 0
	for i := range win {
		if i+1 < len(win) && bytes.Equal(p.str(&p.nodes[win[i].k]), p.str(&p.nodes[win[i+1].k])) {
			continue
		}
		win[out] = win[i]
		out++
	}
	return win[:out]
}

// sortNatural orders map entries the way yaml.v2 writes a map: letters by code
// point, digit runs by value.
func sortNatural(win []jpair, p *jparser) {
	if len(win) < 2 {
		return
	}
	less := func(a, b jpair) bool { return keyLess(p.str(&p.nodes[a.k]), p.str(&p.nodes[b.k])) }
	ok := true
	for i := 1; i < len(win); i++ {
		if less(win[i], win[i-1]) {
			ok = false
			break
		}
	}
	if ok {
		return
	}
	slices.SortFunc(win, func(a, b jpair) int {
		switch {
		case less(a, b):
			return -1
		case less(b, a):
			return 1
		}
		return 0
	})
}

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func keyLess(a, b []byte) bool {
	if isASCII(a) && isASCII(b) {
		return natLess(a, b)
	}
	return natLess([]rune(string(a)), []rune(string(b)))
}

// natLess is the key order of yaml.v2: the first differing character decides;
// a letter sorts after a non-letter; two non-letters compare as numbers when the
// digit runs they start differ.
func natLess[T byte | rune](ar, br []T) bool {
	for i := 0; i < len(ar) && i < len(br); i++ {
		if ar[i] == br[i] {
			continue
		}
		al := unicode.IsLetter(rune(ar[i]))
		bl := unicode.IsLetter(rune(br[i]))
		if al && bl {
			return ar[i] < br[i]
		}
		if al || bl {
			return bl
		}
		var ai, bi int
		var an, bn int64
		if ar[i] == '0' || br[i] == '0' {
			for j := i - 1; j >= 0 && unicode.IsDigit(rune(ar[j])); j-- {
				if ar[j] != '0' {
					an = 1
					bn = 1
					break
				}
			}
		}
		for ai = i; ai < len(ar) && unicode.IsDigit(rune(ar[ai])); ai++ {
			an = an*10 + int64(ar[ai]-'0')
		}
		for bi = i; bi < len(br) && unicode.IsDigit(rune(br[bi])); bi++ {
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

// string reads a JSON string at p.pos into p.strs.
func (p *jparser) string() error {
	p.pos++
	for {
		start := p.pos
		for p.pos < len(p.in) {
			c := p.in[p.pos]
			if c == '"' || c == '\\' || c < 0x20 {
				break
			}
			p.pos++
		}
		p.strs = append(p.strs, p.in[start:p.pos]...)
		if p.pos >= len(p.in) {
			return errNotJSON
		}
		c := p.in[p.pos]
		if c == '"' {
			p.pos++
			return nil
		}
		if c < 0x20 {
			return errNotJSON
		}
		p.pos++ // backslash
		if p.pos >= len(p.in) {
			return errNotJSON
		}
		e := p.in[p.pos]
		p.pos++
		switch e {
		case '"', '\\', '/':
			p.strs = append(p.strs, e)
		case 'b':
			p.strs = append(p.strs, '\b')
		case 'f':
			p.strs = append(p.strs, '\f')
		case 'n':
			p.strs = append(p.strs, '\n')
		case 'r':
			p.strs = append(p.strs, '\r')
		case 't':
			p.strs = append(p.strs, '\t')
		case 'u':
			r, ok := p.hex4()
			if !ok {
				return errNotJSON
			}
			if r >= 0xD800 && r < 0xDC00 {
				if p.pos+1 < len(p.in) && p.in[p.pos] == '\\' && p.in[p.pos+1] == 'u' {
					p.pos += 2
					lo, ok := p.hex4()
					if !ok || lo < 0xDC00 || lo > 0xDFFF {
						return errNotJSON
					}
					r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
				} else {
					return errNotJSON
				}
			} else if r >= 0xDC00 && r <= 0xDFFF {
				return errNotJSON
			}
			p.strs = utf8.AppendRune(p.strs, r)
		default:
			return errNotJSON
		}
	}
}

func (p *jparser) hex4() (rune, bool) {
	if p.pos+4 > len(p.in) {
		return 0, false
	}
	var r rune
	for _, c := range p.in[p.pos : p.pos+4] {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	p.pos += 4
	return r, true
}

// number reads a JSON number and classifies it as a YAML 1.1 reader would:
// integers stay integers, and a number that fits no numeric type stays text.
func (p *jparser) number() (int32, error) {
	start := p.pos
	i := p.pos
	if p.in[i] == '-' {
		i++
	}
	digits := func() int {
		s := i
		for i < len(p.in) && p.in[i] >= '0' && p.in[i] <= '9' {
			i++
		}
		return i - s
	}
	if i < len(p.in) && p.in[i] == '0' {
		i++
	} else if digits() == 0 {
		return 0, errNotJSON
	}
	if i < len(p.in) && p.in[i] == '.' {
		i++
		if digits() == 0 {
			return 0, errNotJSON
		}
	}
	if i < len(p.in) && (p.in[i] == 'e' || p.in[i] == 'E') {
		i++
		if i < len(p.in) && (p.in[i] == '+' || p.in[i] == '-') {
			i++
		}
		if digits() == 0 {
			return 0, errNotJSON
		}
	}
	p.pos = i
	text := p.in[start:i]
	if p.negZeroFloat && len(text) == 2 && text[0] == '-' && text[1] == '0' {
		return p.add(jnode{kind: jFloat, num: floatBits(math.Copysign(0, -1))}), nil
	}
	sv, _ := resolveText(text, tagNone)
	switch sv.kind {
	case skInt:
		return p.add(jnode{kind: jInt, num: uint64(sv.i)}), nil
	case skUint:
		return p.add(jnode{kind: jUint, num: sv.u}), nil
	case skFloat:
		return p.add(jnode{kind: jFloat, num: floatBits(sv.f)}), nil
	}
	off := len(p.strs)
	p.strs = append(p.strs, text...)
	return p.add(jnode{kind: jStr, off: int32(off), len: int32(len(text))}), nil
}
