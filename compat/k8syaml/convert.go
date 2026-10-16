package yaml

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"sync"

	oneyaml "oneyaml.dev/go"
)

// The conversion walks the first document of a parsed stream and writes JSON
// directly, without building an intermediate tree. It follows what the
// yaml.v2 family reads: YAML 1.1 scalars, merge keys, non-string keys turned into
// strings, and the first document of a stream only.

const (
	maxDepth        = 10000
	aliasNodeFactor = 10
	keepBuffer      = 1 << 20
)

// limits are the default limits of the oneyaml package, which this package keeps:
// the alias budget is the larger of the limit and a multiple of the input size,
// as the primary decoder computes it.
var limits = oneyaml.DefaultLimits()

type entry struct {
	ko, kl int32 // key text in walker.kbuf
	prio   int32 // 0 for a key written in the mapping, >0 for merged keys; lower wins
	idx    int32 // position, to let a later duplicate win
	kk     skind // what the key resolved to: keys of different kinds are different keys
	v      oneyaml.Node
}

type anchorDef struct {
	start int
	n     oneyaml.Node
}

type walker struct {
	doc  *oneyaml.Document
	src  []byte
	raw  bool // spans index src
	out  []byte
	tmp  []byte
	ents []entry
	kbuf []byte

	strict  bool
	anchors map[string][]anchorDef
	scanned bool
	root    oneyaml.Node

	depth      int
	aliasDepth int
	aliasNodes int
	aliasBytes int
	nodeBudget int
	byteBudget int
	mseq       int32
	vstack     []vframe
	probes     int // anchor definitions compared while resolving aliases (counted for tests)
}

var walkers = sync.Pool{New: func() any { return new(walker) }}

func getWalker() *walker { return walkers.Get().(*walker) }

func (w *walker) release() {
	if cap(w.out) > keepBuffer || cap(w.kbuf) > keepBuffer || cap(w.ents) > keepBuffer/16 {
		return
	}
	*w = walker{out: w.out[:0], tmp: w.tmp[:0], ents: w.ents[:0], kbuf: w.kbuf[:0], vstack: w.vstack[:0]}
	walkers.Put(w)
}

// convert parses y and writes the first document as JSON into w.out.
func (w *walker) convert(y []byte, strict bool, tt reflect.Type) error {
	// Only the first document is read, so only the first document is parsed.
	y = firstDocument(y)
	doc, err := oneyaml.Parse(y)
	if err != nil {
		return noOption(err)
	}
	w.doc = doc
	w.strict = strict
	w.nodeBudget = max(limits.MaxAliasNodes, aliasNodeFactor*len(y))
	w.byteBudget = max(limits.MaxAliasBytes, aliasNodeFactor*len(y))
	w.out = w.out[:0]
	if doc.Len() == 0 {
		w.out = append(w.out, "null"...)
		return nil
	}
	w.src = doc.Bytes()
	w.raw = rawUsable(y, w.src)
	w.root = doc.Root()
	return w.value(w.root, tt)
}

// firstDocument returns the part of y up to the end of its first document, so that
// a long multi-document stream is not parsed past the document that is read. A
// line that starts with "---" or "..." at column 0 followed by white space or the
// end of the line is a document marker and cannot be content in any context, so
// the first such line after the document has begun ends it. Text that is not
// plain UTF-8 (a byte order mark, UTF-16, UTF-32) is returned whole.
func firstDocument(y []byte) []byte {
	n := min(len(y), 4)
	for _, c := range y[:n] {
		if c == 0 {
			return y
		}
	}
	if len(y) >= 2 && (y[0] == 0xFE && y[1] == 0xFF || y[0] == 0xFF && y[1] == 0xFE || y[0] == 0xEF && y[1] == 0xBB) {
		return y
	}
	begun := false
	for pos := 0; pos < len(y); {
		end := bytes.IndexByte(y[pos:], '\n')
		next := len(y)
		if end >= 0 {
			next = pos + end + 1
		}
		line := y[pos:next]
		switch {
		case isMarker(line, '-'):
			if begun {
				return y[:pos]
			}
			begun = true
		case isMarker(line, '.'):
			return y[:next]
		case !begun && isBlankOrComment(line), !begun && line[0] == '%':
		default:
			begun = true
		}
		pos = next
	}
	return y
}

func isMarker(line []byte, c byte) bool {
	if len(line) < 3 || line[0] != c || line[1] != c || line[2] != c {
		return false
	}
	return len(line) == 3 || line[3] == ' ' || line[3] == '\t' || line[3] == '\n' || line[3] == '\r'
}

func isBlankOrComment(line []byte) bool {
	for _, c := range line {
		switch c {
		case ' ', '\t', '\r', '\n':
		case '#':
			return true
		default:
			return false
		}
	}
	return true
}

// rawUsable reports whether node offsets index the caller's bytes. They do for
// UTF-8 text; a UTF-16 or UTF-32 stream is transcoded and its offsets refer to
// the transcoded text, so the slow path reads values through the Node API.
func rawUsable(y, src []byte) bool {
	if len(y) != len(src) || len(y) == 0 {
		return len(y) == 0
	}
	n := min(len(y), 4)
	for _, c := range y[:n] {
		if c == 0 {
			return false
		}
	}
	if y[0] == 0xFE || y[0] == 0xFF || (y[0] == 0xEF && len(y) > 1 && y[1] == 0xBB) {
		return false
	}
	return true
}

func (w *walker) enter() error {
	w.depth++
	if w.depth > limits.MaxDepth {
		return &oneyaml.LimitError{Limit: oneyaml.LimitMaxDepth, What: "depth", Max: limits.MaxDepth}
	}
	return nil
}

// text returns the decoded text of scalar n. The result is valid until the next
// call.
func (w *walker) text(n oneyaml.Node) []byte {
	if w.raw {
		start, end := n.Span()
		if start >= 0 && end <= len(w.src) && start <= end {
			t := w.src[start:end]
			switch n.Style() {
			case oneyaml.PlainStyle:
				if bytes.IndexByte(t, '\n') < 0 && bytes.IndexByte(t, '\r') < 0 {
					return t
				}
			case oneyaml.DoubleQuotedStyle:
				if len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"' {
					t = t[1 : len(t)-1]
					if bytes.IndexByte(t, '\\') < 0 && bytes.IndexByte(t, '\n') < 0 && bytes.IndexByte(t, '\r') < 0 {
						return t
					}
				}
			case oneyaml.SingleQuotedStyle:
				if len(t) >= 2 && t[0] == '\'' && t[len(t)-1] == '\'' {
					t = t[1 : len(t)-1]
					if bytes.IndexByte(t, '\'') < 0 && bytes.IndexByte(t, '\n') < 0 && bytes.IndexByte(t, '\r') < 0 {
						return t
					}
				}
			}
		}
	}
	w.tmp = append(w.tmp[:0], n.Value()...)
	return w.tmp
}

// scan registers every anchor of the document in order, so that an alias finds
// the nearest definition before it.
func (w *walker) scan() {
	w.scanned = true
	w.anchors = map[string][]anchorDef{}
	// A stack of (node, next child) pairs replaces recursion: the depth of the
	// document is not the depth of the Go stack. Children come in document order,
	// so the definitions of a name are sorted by start.
	type frame struct {
		n    oneyaml.Node
		i, c int // next child, and the number of children (two per mapping entry)
	}
	var stack []frame
	open := func(n oneyaml.Node) {
		if a := n.Anchor(); a != "" {
			s, _ := n.Span()
			w.anchors[a] = append(w.anchors[a], anchorDef{s, n})
		}
		switch n.Kind() {
		case oneyaml.SequenceNode:
			stack = append(stack, frame{n: n, c: n.Len()})
		case oneyaml.MappingNode:
			stack = append(stack, frame{n: n, c: 2 * n.Len()})
		}
	}
	open(w.root)
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.i == f.c {
			stack = stack[:len(stack)-1]
			continue
		}
		var child oneyaml.Node
		if f.n.Kind() == oneyaml.SequenceNode {
			child = f.n.Item(f.i)
		} else if k, v := f.n.Entry(f.i / 2); f.i%2 == 0 {
			child = k
		} else {
			child = v
		}
		f.i++
		open(child)
	}
}

func (w *walker) target(alias oneyaml.Node) (oneyaml.Node, error) {
	if !w.scanned {
		w.scan()
	}
	name := alias.AliasName()
	at, _ := alias.Span()
	defs := w.anchors[name]
	// The definitions of a name are in document order, so the latest one before the
	// alias is found by binary search: a document that redefines one name many times
	// stays linear.
	if i := sort.Search(len(defs), func(i int) bool { w.probes++; return defs[i].start >= at }); i > 0 {
		return defs[i-1].n, nil
	}
	return oneyaml.Node{}, fmt.Errorf("yaml: unknown anchor '%s' referenced", name)
}

// vframe is an open collection or alias while a value is written. The walker
// keeps these on a stack of its own, so the depth of the document is not the depth
// of the Go stack.
type vframe struct {
	kind  uint8
	n     oneyaml.Node
	i, c  int          // next child, number of children
	et    reflect.Type // element type of a sequence
	tg    *target      // target of a mapping
	base  int          // mapping: where its entries begin in ents
	kbase int          // mapping: where its keys begin in kbuf
	start int          // alias: length of out when it began
}

const (
	vSeq uint8 = iota
	vMap
	vAlias
)

func (w *walker) value(n oneyaml.Node, tt reflect.Type) error {
	depth0 := w.depth
	stack := w.vstack[:0]
	err := w.walk(n, tt, &stack)
	if err != nil {
		// Undo what the open frames hold.
		for i := len(stack) - 1; i >= 0; i-- {
			switch f := &stack[i]; f.kind {
			case vMap:
				w.ents, w.kbuf = w.ents[:f.base], w.kbuf[:f.kbase]
			case vAlias:
				w.aliasDepth--
			}
		}
		w.depth = depth0
	}
	clear(stack)
	w.vstack = stack[:0]
	return err
}

// walk writes n and everything below it as JSON.
func (w *walker) walk(n oneyaml.Node, tt reflect.Type, sp *[]vframe) error {
	stack := *sp
	defer func() { *sp = stack }()
	for {
		// Begin node n.
		if err := w.enter(); err != nil {
			return err
		}
		var tg *target
		if tt != nil {
			tg = targetOf(tt)
		}
		if w.aliasDepth > 0 {
			w.aliasNodes++
			if w.aliasNodes > w.nodeBudget {
				return &oneyaml.LimitError{Limit: oneyaml.LimitMaxAliasNodes, What: "alias expansion nodes", Max: w.nodeBudget}
			}
		}
		opened := false
		switch n.Kind() {
		case oneyaml.ScalarNode:
			if err := w.scalar(n, tg); err != nil {
				return err
			}
		case oneyaml.MappingNode:
			base, kbase := len(w.ents), len(w.kbuf)
			f := vframe{kind: vMap, n: n, tg: tg, base: base, kbase: kbase}
			// The frame is on the stack before the entries are collected, so a
			// failure there restores ents and kbuf.
			stack = append(stack, f)
			w.mseq = 0
			if err := w.collect(n, 0); err != nil {
				return err
			}
			if len(w.ents)-base > 1 {
				if err := w.order(base); err != nil {
					return err
				}
			}
			stack[len(stack)-1].c = len(w.ents) - base
			w.out = append(w.out, '{')
			opened = true
		case oneyaml.SequenceNode:
			w.out = append(w.out, '[')
			f := vframe{kind: vSeq, n: n, c: n.Len()}
			if tg != nil && tg.kind == tgSlice {
				f.et = tg.elem
			}
			stack = append(stack, f)
			opened = true
		case oneyaml.AliasNode:
			t, err := w.target(n)
			if err != nil {
				return err
			}
			// The alias is charged to the budget once it has been written.
			w.aliasDepth++
			stack = append(stack, vframe{kind: vAlias, start: len(w.out)})
			n = t
			continue
		default:
			return fmt.Errorf("yaml: unsupported node")
		}
		if !opened {
			w.depth--
		}
		// Go on with the next child of the innermost open frame, closing the ones
		// that are finished.
		for {
			if len(stack) == 0 {
				return nil
			}
			f := &stack[len(stack)-1]
			if f.kind == vAlias {
				w.aliasDepth--
				if w.aliasDepth == 0 {
					w.aliasBytes += len(w.out) - f.start
					if w.aliasBytes > w.byteBudget {
						stack = stack[:len(stack)-1]
						w.depth--
						return &oneyaml.LimitError{Limit: oneyaml.LimitMaxAliasBytes, What: "alias expansion bytes", Max: w.byteBudget}
					}
				}
				stack = stack[:len(stack)-1]
				w.depth--
				continue
			}
			if f.i == f.c {
				if f.kind == vSeq {
					w.out = append(w.out, ']')
				} else {
					w.out = append(w.out, '}')
					w.ents, w.kbuf = w.ents[:f.base], w.kbuf[:f.kbase]
				}
				stack = stack[:len(stack)-1]
				w.depth--
				continue
			}
			if f.kind == vSeq {
				if f.i > 0 {
					w.out = append(w.out, ',')
				}
				n, tt = f.n.Item(f.i), f.et
			} else {
				e := w.ents[f.base+f.i]
				if f.i > 0 {
					w.out = append(w.out, ',')
				}
				key := w.kbuf[e.ko : e.ko+e.kl]
				w.out = appendJSONString(w.out, key)
				w.out = append(w.out, ':')
				tt = nil
				if f.tg != nil {
					switch f.tg.kind {
					case tgStruct:
						tt = f.tg.field(key)
					case tgMap:
						tt = f.tg.elem
					}
				}
				n = e.v
			}
			f.i++
			break
		}
	}
}

func (w *walker) scalar(n oneyaml.Node, tg *target) error {
	text := w.text(n)
	var sv sval
	if tag, ok := n.ExplicitTag(); ok {
		var err error
		sv, err = resolve(text, classifyTag(tag))
		if err != nil {
			return err
		}
	} else if n.Style() == oneyaml.PlainStyle {
		sv, _ = resolve(text, tagNone)
	}
	return w.writeScalar(sv, text, tg != nil && tg.kind == tgString)
}

func (w *walker) writeScalar(sv sval, text []byte, asString bool) error {
	switch sv.kind {
	case skNull:
		w.out = append(w.out, "null"...)
	case skBool:
		s := "false"
		if sv.b {
			s = "true"
		}
		if asString {
			w.out = append(w.out, '"')
			w.out = append(w.out, s...)
			w.out = append(w.out, '"')
		} else {
			w.out = append(w.out, s...)
		}
	case skInt:
		if asString {
			w.out = append(w.out, '"')
		}
		w.out = strconv.AppendInt(w.out, sv.i, 10)
		if asString {
			w.out = append(w.out, '"')
		}
	case skUint:
		if asString {
			w.out = append(w.out, '"')
		}
		w.out = strconv.AppendUint(w.out, sv.u, 10)
		if asString {
			w.out = append(w.out, '"')
		}
	case skFloat:
		if asString {
			w.out = append(w.out, '"')
			w.out = strconv.AppendFloat(w.out, sv.f, 'g', -1, 64)
			w.out = append(w.out, '"')
			break
		}
		if math.IsNaN(sv.f) || math.IsInf(sv.f, 0) {
			return fmt.Errorf("json: unsupported value: %s", strconv.FormatFloat(sv.f, 'g', -1, 64))
		}
		w.out = appendJSONFloat(w.out, sv.f)
	default:
		if sv.own {
			text = sv.str
		}
		w.out = appendJSONString(w.out, text)
	}
	return nil
}

// collect appends the entries of mapping n, expanding merge keys. prio says how
// strongly the entries bind: keys written in the mapping win over merged keys,
// and the first merged mapping wins over the ones after it.
func (w *walker) collect(n oneyaml.Node, prio int32) error {
	if err := w.enter(); err != nil {
		return err
	}
	defer func() { w.depth-- }()
	for k, v := range n.Entries() {
		if w.isMerge(k) {
			if err := w.merge(v); err != nil {
				return err
			}
			continue
		}
		ko := len(w.kbuf)
		kk, err := w.key(k)
		if err != nil {
			return err
		}
		w.ents = append(w.ents, entry{ko: int32(ko), kl: int32(len(w.kbuf) - ko), prio: prio, idx: int32(len(w.ents)), kk: kk, v: v})
	}
	return nil
}

func (w *walker) isMerge(k oneyaml.Node) bool {
	if k.Kind() != oneyaml.ScalarNode || k.Style() != oneyaml.PlainStyle {
		return false
	}
	start, end := k.Span()
	if end-start != 2 {
		return false
	}
	if _, ok := k.ExplicitTag(); ok {
		return false
	}
	if w.raw {
		return w.src[start] == '<' && w.src[start+1] == '<'
	}
	return k.Value() == "<<"
}

func (w *walker) merge(v oneyaml.Node) error {
	one := func(m oneyaml.Node) error {
		if m.Kind() == oneyaml.AliasNode {
			t, err := w.target(m)
			if err != nil {
				return err
			}
			m = t
			w.aliasNodes += m.Len()
			if w.aliasNodes > w.nodeBudget {
				return &oneyaml.LimitError{Limit: oneyaml.LimitMaxAliasNodes, What: "alias expansion nodes", Max: w.nodeBudget}
			}
		}
		if m.Kind() != oneyaml.MappingNode {
			return fmt.Errorf("map merge requires map or sequence of maps as the value")
		}
		w.mseq++
		return w.collect(m, w.mseq)
	}
	switch v.Kind() {
	case oneyaml.SequenceNode:
		for item := range v.Items() {
			if err := one(item); err != nil {
				return err
			}
		}
		return nil
	case oneyaml.MappingNode, oneyaml.AliasNode:
		return one(v)
	}
	return fmt.Errorf("map merge requires map or sequence of maps as the value")
}

// key appends the JSON object key for key node k to kbuf.
func (w *walker) key(k oneyaml.Node) (skind, error) {
	if k.Kind() == oneyaml.AliasNode {
		t, err := w.target(k)
		if err != nil {
			return 0, err
		}
		k = t
	}
	if k.Kind() != oneyaml.ScalarNode {
		return 0, fmt.Errorf("invalid map key: a %s cannot be a key", k.Kind())
	}
	text := w.text(k)
	var sv sval
	if tag, ok := k.ExplicitTag(); ok {
		var err error
		sv, err = resolve(text, classifyTag(tag))
		if err != nil {
			return 0, err
		}
	} else if k.Style() == oneyaml.PlainStyle {
		sv, _ = resolve(text, tagNone)
	}
	switch sv.kind {
	case skStr:
		if sv.own {
			text = sv.str
		}
		w.kbuf = append(w.kbuf, text...)
	case skInt:
		w.kbuf = strconv.AppendInt(w.kbuf, sv.i, 10)
	case skUint:
		w.kbuf = strconv.AppendUint(w.kbuf, sv.u, 10)
	case skBool:
		if sv.b {
			w.kbuf = append(w.kbuf, "true"...)
		} else {
			w.kbuf = append(w.kbuf, "false"...)
		}
	case skFloat:
		switch {
		case math.IsNaN(sv.f):
			w.kbuf = append(w.kbuf, ".nan"...)
		case math.IsInf(sv.f, 1):
			w.kbuf = append(w.kbuf, ".inf"...)
		case math.IsInf(sv.f, -1):
			w.kbuf = append(w.kbuf, "-.inf"...)
		default:
			w.kbuf = strconv.AppendFloat(w.kbuf, sv.f, 'g', -1, 64)
		}
	default:
		return 0, fmt.Errorf("unsupported map key of type: <nil>, key: null")
	}
	kk := sv.kind
	if kk == skUint {
		kk = skInt
	}
	return kk, nil
}

// order sorts the entries from base on by key, as encoding/json writes a map,
// and keeps one entry per key: a key written in the mapping beats a merged key,
// an earlier merged mapping beats a later one, and of equal keys in one mapping
// the last wins.
func (w *walker) order(base int) error {
	ents := w.ents[base:]
	kb := w.kbuf
	key := func(e *entry) []byte { return kb[e.ko : e.ko+e.kl] }
	sorted := true
	for i := 1; i < len(ents); i++ {
		if ents[i].prio != 0 || bytes.Compare(key(&ents[i-1]), key(&ents[i])) >= 0 {
			sorted = false
			break
		}
	}
	if sorted && ents[0].prio == 0 {
		return nil
	}
	slices.SortFunc(ents, func(a, b entry) int {
		if c := bytes.Compare(kb[a.ko:a.ko+a.kl], kb[b.ko:b.ko+b.kl]); c != 0 {
			return c
		}
		if a.prio != b.prio {
			return int(a.prio - b.prio)
		}
		return int(b.idx - a.idx)
	})
	out := 0
	group := 0 // first entry of the run of equal keys
	for i := range ents {
		if i > 0 && bytes.Equal(key(&ents[i]), key(&ents[out-1])) {
			if w.strict && ents[i].prio == 0 {
				// Only keys of one kind are the same key: 1 and "1" are two keys
				// that happen to print alike.
				for j := group; j < i; j++ {
					if ents[j].prio == 0 && ents[j].kk == ents[i].kk {
						line, _ := ents[i].v.Position()
						return fmt.Errorf("yaml: unmarshal errors:\n  line %d: key %q already set in map", line, key(&ents[i]))
					}
				}
			}
			continue
		}
		group = i
		ents[out] = ents[i]
		out++
	}
	w.ents = w.ents[:base+out]
	return nil
}

// noOption removes the advice to raise a limit with an option from a limit error
// of the core: this package has no limit options.
func noOption(err error) error {
	var le *oneyaml.LimitError
	if errors.As(err, &le) {
		le.Option = ""
	}
	return err
}
