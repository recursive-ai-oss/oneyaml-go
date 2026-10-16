package bind

import (
	"math/big"
	"strconv"

	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/resolve"
	"oneyaml.dev/go/internal/scan"
)

// pair is a staged mapping entry. Entries are staged on a shared stack and moved
// into a map of exactly the right size when the mapping ends, so a map is never
// grown while it is filled.
type pair struct {
	ks  string // key when str is true
	ka  any    // key otherwise
	str bool
	val any
	off int // source offset of the key, for error positions
}

type anyFrame struct {
	seq     bool
	allStr  bool // every key so far is a string
	haveKey bool // a key is pending its value
	isMerge bool // the pending key is "<<"
	merged  bool
	base    int // start in vals (sequences) or pairs (mappings)
	key     pair
	merge   []any
	off     int
}

// generic decodes the node that starts with first into the generic form:
// map[string]any (or map[any]any when a key is not a string), []any, string,
// int (int64, uint64 or *big.Int when it does not fit), float64, bool and nil.
// It runs on an explicit stack, so input nesting is never Go recursion.
func (b *Binder) generic(first *parse.Event) (any, error) {
	base := len(b.frames)
	ev := *first
	var err error
	// aliasKey is where the alias that stands for the pending key was written.
	// An error about the key points there, not at the anchored node it replays.
	aliasKey := -1
	for {
		var top *anyFrame
		if len(b.frames) > base {
			top = &b.frames[len(b.frames)-1]
		}
		keyPos := top != nil && !top.seq && !top.haveKey
		var val any
		switch ev.Kind {
		case parse.Alias:
			if keyPos && aliasKey < 0 {
				aliasKey = ev.Start
			}
			b.expand(&ev)
			if ev, err = b.next(); err != nil {
				return nil, b.unwind(base, err)
			}
			continue
		case parse.Scalar:
			if keyPos {
				if err = b.genericKey(top, &ev); err != nil {
					return nil, b.unwind(base, err)
				}
				if aliasKey >= 0 {
					top.key.off = aliasKey
					aliasKey = -1
				}
				if ev, err = b.next(); err != nil {
					return nil, b.unwind(base, err)
				}
				continue
			}
			val, err = b.scalarAny(&ev)
			if err != nil {
				return nil, b.unwind(base, err)
			}
		case parse.SequenceStart, parse.MappingStart:
			if keyPos {
				return nil, b.unwind(base, &Error{Kind: KindType, Offset: ev.Start, GoType: "interface {}",
					Msg: "a collection cannot be the key of a generic mapping (Go map keys must be comparable)"})
			}
			if err = b.checkDepth(len(b.frames)+b.tdepth+1, ev.Start); err != nil {
				return nil, b.unwind(base, err)
			}
			f := anyFrame{seq: ev.Kind == parse.SequenceStart, allStr: true, off: ev.Start}
			if f.seq {
				f.base = len(b.vals)
			} else {
				f.base = len(b.pairs)
			}
			b.frames = append(b.frames, f)
			if ev, err = b.next(); err != nil {
				return nil, b.unwind(base, err)
			}
			continue
		case parse.SequenceEnd:
			val = b.finishSeq(top)
			b.frames = b.frames[:len(b.frames)-1]
		case parse.MappingEnd:
			val, err = b.finishMap(top)
			if err != nil {
				return nil, b.unwind(base, err)
			}
			b.frames = b.frames[:len(b.frames)-1]
		default:
			return nil, b.unwind(base, b.syntax(ev.Start, "syntax", "unexpected "+ev.Kind.String()))
		}
		if len(b.frames) == base {
			return val, nil
		}
		top = &b.frames[len(b.frames)-1]
		if top.seq {
			b.vals = append(b.vals, val)
		} else if top.isMerge {
			top.merge = append(top.merge, val)
			top.isMerge = false
			top.haveKey = false
		} else {
			top.key.val = val
			b.pairs = append(b.pairs, top.key)
			top.key = pair{}
			top.haveKey = false
		}
		if ev, err = b.next(); err != nil {
			return nil, b.unwind(base, err)
		}
	}
}

// unwind drops the frames and staged values of a failed generic decode and
// attaches the path of the failure.
func (b *Binder) unwind(base int, err error) error {
	if be, ok := err.(*Error); ok && be.Kind != KindLimit && be.Path == "" {
		be.Path = b.framePath(base)
	}
	valsAt, pairsAt := len(b.vals), len(b.pairs)
	for i := len(b.frames) - 1; i >= base; i-- {
		if b.frames[i].seq {
			valsAt = b.frames[i].base
		} else {
			pairsAt = b.frames[i].base
		}
		b.frames[i] = anyFrame{}
	}
	clear(b.vals[valsAt:])
	b.vals = b.vals[:valsAt]
	clear(b.pairs[pairsAt:])
	b.pairs = b.pairs[:pairsAt]
	b.frames = b.frames[:base]
	return err
}

// framePath is the RFC 6901 pointer of the pending position in the frames above
// base. It is only used for errors, so it favours clarity over speed.
func (b *Binder) framePath(base int) string {
	var segs []string
	for i := base; i < len(b.frames); i++ {
		f := &b.frames[i]
		switch {
		case f.seq:
			// The values staged by this frame sit below those of any frame above it.
			end := len(b.vals)
			for j := i + 1; j < len(b.frames); j++ {
				if b.frames[j].seq {
					end = b.frames[j].base
					break
				}
			}
			segs = append(segs, strconv.Itoa(end-f.base))
		case f.haveKey && f.key.str:
			segs = append(segs, f.key.ks)
		case f.haveKey && f.isMerge:
			segs = append(segs, "<<")
		}
	}
	return joinPath(segs)
}

func (b *Binder) finishSeq(f *anyFrame) any {
	n := len(b.vals) - f.base
	out := make([]any, n)
	copy(out, b.vals[f.base:])
	clear(b.vals[f.base:])
	b.vals = b.vals[:f.base]
	return out
}

func (b *Binder) dupKey(off int) error {
	return b.syntax(off, "duplicate-key", "mapping key is defined more than once")
}

// firstFloatKey returns the index of the first pair whose key is the float fl.
func firstFloatKey(ps []pair, fl float64) int {
	for j := range ps {
		if !ps[j].str {
			if g, ok := ps[j].ka.(float64); ok && g == fl {
				return j
			}
		}
	}
	return -1
}

// floatKeys reports two keys that are written differently but read as the same
// float64. They are not duplicates; a Go map keyed by float64 cannot hold both.
// Offset is the later key and First the earlier one.
func (b *Binder) floatKeys(first, off int) error {
	return &Error{Kind: KindType, Offset: off, First: first, Category: "float-key", GoType: "map[interface {}]interface {}",
		Msg: "two mapping keys are different numbers that round to the same float64, which a map cannot hold apart"}
}

func (b *Binder) finishMap(f *anyFrame) (any, error) {
	ps := b.pairs[f.base:]
	var out any
	if f.allStr {
		m := make(map[string]any, len(ps))
		for i := range ps {
			n := len(m)
			m[ps[i].ks] = ps[i].val
			if len(m) == n {
				return nil, b.dropPairs(f, b.dupKey(ps[i].off))
			}
		}
		out = m
		if len(f.merge) > 0 {
			var err error
			if out, err = b.applyMerge(m, f); err != nil {
				return nil, b.dropPairs(f, err)
			}
		}
	} else {
		m := make(map[any]any, len(ps))
		nanSeen := false
		for i := range ps {
			k := ps[i].ka
			if ps[i].str {
				k = ps[i].ks
			}
			// A Go map never finds a NaN key again, so two .nan keys would both be
			// kept. They are the same YAML value, so the second is a duplicate.
			if fl, ok := k.(float64); ok && fl != fl && b.cfg.Dialect == DialectCore {
				if nanSeen {
					return nil, b.dropPairs(f, b.dupKey(ps[i].off))
				}
				nanSeen = true
			}
			n := len(m)
			m[k] = ps[i].val
			if len(m) == n {
				if fl, ok := k.(float64); !ok || fl == fl {
					if ok {
						if j := firstFloatKey(ps[:i], fl); j >= 0 {
							return nil, b.dropPairs(f, b.floatKeys(ps[j].off, ps[i].off))
						}
					}
					return nil, b.dropPairs(f, b.dupKey(ps[i].off))
				}
			}
		}
		out = m
		if len(f.merge) > 0 {
			var err error
			if out, err = b.applyMerge(m, f); err != nil {
				return nil, b.dropPairs(f, err)
			}
		}
	}
	clear(b.pairs[f.base:])
	b.pairs = b.pairs[:f.base]
	return out, nil
}

func (b *Binder) dropPairs(f *anyFrame, err error) error {
	clear(b.pairs[f.base:])
	b.pairs = b.pairs[:f.base]
	return err
}

// applyMerge adds the entries of the merge sources to the mapping m for keys m
// does not have. Earlier sources win over later ones, and the mapping's own keys
// win over all of them.
func (b *Binder) applyMerge(m any, f *anyFrame) (any, error) {
	var srcs []any
	for _, v := range f.merge {
		switch x := v.(type) {
		case map[string]any, map[any]any:
			srcs = append(srcs, x)
		case []any:
			for _, e := range x {
				switch e.(type) {
				case map[string]any, map[any]any:
					srcs = append(srcs, e)
				default:
					return nil, b.mergeErr(f)
				}
			}
		default:
			return nil, b.mergeErr(f)
		}
	}
	// A source with a non-string key turns a string-keyed target into map[any]any.
	if ms, ok := m.(map[string]any); ok {
		wide := false
		for _, s := range srcs {
			if sa, ok := s.(map[any]any); ok {
				for k := range sa {
					if _, isStr := k.(string); !isStr {
						wide = true
					}
				}
			}
		}
		if wide {
			conv := make(map[any]any, len(ms))
			for k, v := range ms {
				conv[k] = v
			}
			m = conv
		}
	}
	switch t := m.(type) {
	case map[string]any:
		for _, s := range srcs {
			switch x := s.(type) {
			case map[string]any:
				for k, v := range x {
					if _, has := t[k]; !has {
						t[k] = v
					}
				}
			case map[any]any:
				for k, v := range x {
					ks := k.(string)
					if _, has := t[ks]; !has {
						t[ks] = v
					}
				}
			}
		}
	case map[any]any:
		for _, s := range srcs {
			switch x := s.(type) {
			case map[string]any:
				for k, v := range x {
					if _, has := t[k]; !has {
						t[k] = v
					}
				}
			case map[any]any:
				for k, v := range x {
					if _, has := t[k]; !has {
						t[k] = v
					}
				}
			}
		}
	}
	return m, nil
}

func (b *Binder) mergeErr(f *anyFrame) error {
	return &Error{Kind: KindType, Offset: f.off, GoType: "map",
		Msg: "a merge key needs a mapping or a sequence of mappings"}
}

// genericKey stores the scalar key ev in the pending-key slot of the mapping
// frame f.
func (b *Binder) genericKey(f *anyFrame, ev *parse.Event) error {
	if b.cfg.MergeKeys && ev.Style == scan.Plain && !ev.HasTag() && ev.End-ev.Start == 2 &&
		b.src[ev.Start] == '<' && b.src[ev.Start+1] == '<' {
		if f.merged {
			return b.dupKey(ev.Start)
		}
		f.merged = true
		f.isMerge = true
		f.haveKey = true
		return nil
	}
	if b.cfg.Dialect == DialectV3 {
		var sv scalarV3
		if err := b.readV3(ev, &sv); err != nil {
			return err
		}
		f.haveKey = true
		f.key = pair{off: ev.Start}
		if sv.r.Kind == resolve.String {
			if sv.tag == tagNone || sv.tag == tagStr || sv.tag == tagNonSpecific {
				f.key.ks, f.key.str = b.internKey(sv.txt), true
				return nil
			}
			// A key with some other tag (!!binary, a custom tag) makes the mapping a
			// map[any]any in yaml.v3, though the key is still a string.
			f.allStr = false
			f.key.ka = string(sv.txt)
			return nil
		}
		f.allStr = false
		f.key.ka = anyV3(&sv)
		return nil
	}
	tag, err := b.tagOf(ev)
	if err != nil {
		return err
	}
	text, err := b.scalar(ev)
	if err != nil {
		return err
	}
	r, ok := classify(text, ev.Style, tag)
	if !ok {
		return b.tagMismatch(ev, text, tag)
	}
	f.haveKey = true
	f.key = pair{off: ev.Start}
	if r.Kind == resolve.String {
		f.key.ks, f.key.str = b.internKey(text), true
		return nil
	}
	f.allStr = false
	if err := b.numLimit(&r, text, ev.Start); err != nil {
		return err
	}
	f.key.ka = anyOf(text, &r, tag)
	return nil
}

// scalarAny decodes a scalar event into its generic value.
func (b *Binder) scalarAny(ev *parse.Event) (any, error) {
	if b.cfg.Dialect == DialectV3 {
		return b.scalarAnyV3(ev)
	}
	var tag tagClass
	if ev.HasTag() {
		var err error
		if tag, err = b.tagOf(ev); err != nil {
			return nil, err
		}
	}
	text, err := b.scalar(ev)
	if err != nil {
		return nil, err
	}
	r, ok := classify(text, ev.Style, tag)
	if !ok {
		return nil, b.tagMismatch(ev, text, tag)
	}
	if r.Kind == resolve.String {
		return string(text), nil
	}
	if err := b.numLimit(&r, text, ev.Start); err != nil {
		return nil, err
	}
	if b.jsonNums && tag == tagNone && ev.Style == scan.Plain && (r.Kind == resolve.Int || r.Kind == resolve.Float) && isJSONNumber(text) {
		return jsonNum(text), nil
	}
	return anyOf(text, &r, tag), nil
}

func (b *Binder) tagMismatch(ev *parse.Event, text []byte, tag tagClass) error {
	return &Error{Kind: KindType, Offset: ev.Start, GoType: "interface {}",
		Msg: "cannot construct " + tagNames[tag] + " from `" + abbrev(text) + "`"}
}

var tagNames = [...]string{tagNone: "!!str", tagStr: "!!str", tagInt: "!!int", tagFloat: "!!float", tagBool: "!!bool",
	tagNull: "!!null", tagBinary: "!!binary", tagMap: "!!map", tagSeq: "!!seq", tagSet: "!!set", tagOmap: "!!omap",
	tagTimestamp: "!!timestamp", tagNonSpecific: "!", tagOther: "!!str"}

func abbrev(b []byte) string {
	if len(b) > 40 {
		return string(b[:40]) + "..."
	}
	return string(b)
}

// anyOf builds the generic value of a classified scalar that is not a string.
func anyOf(text []byte, r *resolve.Result, tag tagClass) any {
	switch r.Kind {
	case resolve.Null:
		return nil
	case resolve.Bool:
		return r.B
	case resolve.Int:
		if tag == tagFloat {
			return intToFloat(r)
		}
		switch r.W {
		case resolve.Fits64:
			if int64(int(r.I)) == r.I {
				return int(r.I)
			}
			return r.I
		case resolve.FitsU64:
			return r.U
		}
		return r.Big()
	case resolve.Float:
		return r.F
	}
	return string(text)
}

func intToFloat(r *resolve.Result) float64 {
	switch r.W {
	case resolve.Fits64:
		return float64(r.I)
	case resolve.FitsU64:
		return float64(r.U)
	}
	f, _ := new(big.Float).SetInt(r.Big()).Float64()
	return f
}

// internKey returns a string for key text, sharing storage between equal short
// keys: records in a list repeat the same few keys, and each copy would be an
// allocation.
func (b *Binder) internKey(text []byte) string {
	if len(text) > 40 {
		return string(text)
	}
	if s, ok := b.intern[string(text)]; ok {
		return s
	}
	s := string(text)
	if b.intern == nil {
		b.intern = make(map[string]string, 64)
	}
	if len(b.intern) < 4096 {
		b.intern[s] = s
	}
	return s
}
