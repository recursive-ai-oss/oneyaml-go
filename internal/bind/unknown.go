package bind

import (
	"bytes"
	"hash/maphash"
	"strconv"

	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/resolve"
	"oneyaml.dev/go/internal/scan"
)

// unknownLinear is how many keys a mapping may have before its keys are hashed.
// Most mappings that carry keys the target ignores have a few of them, and
// comparing a few short byte strings costs less than hashing them.
const unknownLinear = 16

var keySeed = maphash.MakeSeed()

// keySet remembers the keys of one mapping that no field of the target holds, so
// that a repeated one can be refused. A key that is read into a field is judged
// by the field; these are the keys that would otherwise be skipped unseen.
//
// The keys are kept back to back in one byte slice and found through a table of
// indexes, so a mapping with thousands of keys costs a few growing slices and
// not an allocation per key.
type keySet struct {
	arena []byte // the keys, back to back
	ends  []int  // end of each key in arena
	table []int  // open addressing: index+1 of a key, 0 for a free slot; nil while few
}

// reset empties the set and keeps the room it grew, for the next mapping.
func (u *keySet) reset() {
	u.arena, u.ends, u.table = u.arena[:0], u.ends[:0], nil
}

func (u *keySet) key(i int) []byte {
	start := 0
	if i > 0 {
		start = u.ends[i-1]
	}
	return u.arena[start:u.ends[i]]
}

// add records k and reports whether it was there already.
func (u *keySet) add(k []byte) bool {
	if u.table == nil {
		for i := range u.ends {
			if bytes.Equal(u.key(i), k) {
				return true
			}
		}
		u.arena = append(u.arena, k...)
		u.ends = append(u.ends, len(u.arena))
		if len(u.ends) > unknownLinear {
			u.rehash(8 * unknownLinear)
		}
		return false
	}
	mask := len(u.table) - 1
	i := int(maphash.Bytes(keySeed, k)) & mask
	for ; u.table[i] != 0; i = (i + 1) & mask {
		if bytes.Equal(u.key(u.table[i]-1), k) {
			return true
		}
	}
	u.arena = append(u.arena, k...)
	u.ends = append(u.ends, len(u.arena))
	u.table[i] = len(u.ends)
	if 2*len(u.ends) > len(u.table) {
		u.rehash(2 * len(u.table))
	}
	return false
}

// rehash rebuilds the table with size slots, a power of two, from the keys held.
func (u *keySet) rehash(size int) {
	u.table = make([]int, size)
	mask := size - 1
	for n := range u.ends {
		i := int(maphash.Bytes(keySeed, u.key(n))) & mask
		for u.table[i] != 0 {
			i = (i + 1) & mask
		}
		u.table[i] = n + 1
	}
}

// getSet returns an empty keySet, reusing one that an earlier mapping gave back.
func (b *Binder) getSet() *keySet {
	if n := len(b.sets); n > 0 {
		u := b.sets[n-1]
		b.sets = b.sets[:n-1]
		return u
	}
	return new(keySet)
}

// putSet gives a keySet back. One that grew large is let go, so a pooled binder
// does not hold on to the keys of a big document.
func (b *Binder) putSet(u *keySet) {
	if u == nil || cap(u.arena) > 64<<10 || cap(u.ends) > 4<<10 || len(b.sets) >= 16 {
		return
	}
	u.reset()
	b.sets = append(b.sets, u)
}

// identity returns the bytes that stand for the key whose text is text, such
// that two keys have equal identities exactly when they are one key after the
// schema has read them: 1 and 0x1, null and ~, true and True. It appends to dst.
// Quoted keys are strings, and so are all keys under the yaml.v3 rules, which
// compare the text of keys. A float is a class of its own, so 1.5 and "1.5" are
// two keys; a huge number is refused here as it is when read into a value.
func (b *Binder) identity(dst []byte, ev *parse.Event, text []byte) ([]byte, error) {
	if b.cfg.Dialect == DialectCore && (ev.Style == scan.Plain || ev.HasTag()) {
		var tag tagClass
		if ev.HasTag() {
			// The copy is for tagOf, which hands its argument to the input through an
			// interface and so makes it escape. Only a tagged key pays for it.
			c := *ev
			var err error
			if tag, err = b.tagOf(&c); err != nil {
				tag = tagNone
			}
		}
		r, ok := classify(text, ev.Style, tag)
		if !ok {
			return append(append(dst, 's'), text...), nil
		}
		if r.Kind == resolve.Int || r.Kind == resolve.Float {
			if err := b.numLimit(&r, text, ev.Start); err != nil {
				return dst, err
			}
		}
		switch r.Kind {
		case resolve.Null:
			return append(dst, 'n'), nil
		case resolve.Bool:
			if r.B {
				return append(dst, 'b', '1'), nil
			}
			return append(dst, 'b', '0'), nil
		case resolve.Int:
			if tag == tagFloat {
				return append(append(dst, 'f'), text...), nil
			}
			switch r.W {
			case resolve.Fits64:
				return strconv.AppendInt(append(dst, 'i'), r.I, 10), nil
			case resolve.FitsU64:
				return strconv.AppendUint(append(dst, 'i'), r.U, 10), nil
			}
			return append(append(dst, 'i'), r.Big().String()...), nil
		case resolve.Float:
			// Every .nan is one key. Other floats are compared as written: telling
			// 1e5 from 1e00005 exactly is the job of the key comparison of a real
			// map, which an ignored key never reaches.
			if r.F != r.F {
				return append(dst, 'f', 'n'), nil
			}
			return append(append(dst, 'f'), text...), nil
		}
	}
	return append(append(dst, 's'), text...), nil
}

// skipFrame is one open collection of a node being skipped. The keys of a
// mapping that has few are kept on the binder's shared stack, from aStart and
// eStart on: a child mapping pushes above its parent's keys and takes its own
// off again before the parent reads the next one. Only a mapping with many keys
// takes a hashed set.
type skipFrame struct {
	mapping        bool
	n              int // children seen: in a mapping, even means the next is a key
	aStart, eStart int
	set            *keySet
}

// addSkipKey records the key identity id in the mapping on top of the skip stack
// and reports whether it was there already.
func (b *Binder) addSkipKey(f *skipFrame, id []byte) bool {
	if f.set != nil {
		return f.set.add(id)
	}
	prev := f.aStart
	for i := f.eStart; i < len(b.skipEnds); i++ {
		if bytes.Equal(b.skipArena[prev:b.skipEnds[i]], id) {
			return true
		}
		prev = b.skipEnds[i]
	}
	b.skipArena = append(b.skipArena, id...)
	b.skipEnds = append(b.skipEnds, len(b.skipArena))
	if len(b.skipEnds)-f.eStart > unknownLinear {
		u := b.getSet()
		prev := f.aStart
		for i := f.eStart; i < len(b.skipEnds); i++ {
			u.add(b.skipArena[prev:b.skipEnds[i]])
			prev = b.skipEnds[i]
		}
		b.skipArena, b.skipEnds = b.skipArena[:f.aStart], b.skipEnds[:f.eStart]
		f.set = u
	}
	return false
}

// popSkip closes the frame on top of the skip stack and drops its keys.
func (b *Binder) popSkip() {
	n := len(b.skipFrames) - 1
	f := &b.skipFrames[n]
	b.skipArena, b.skipEnds = b.skipArena[:f.aStart], b.skipEnds[:f.eStart]
	b.putSet(f.set)
	*f = skipFrame{}
	b.skipFrames = b.skipFrames[:n]
}

// unwindSkip closes every frame above base, for an error part way through.
func (b *Binder) unwindSkip(base int) {
	for len(b.skipFrames) > base {
		b.popSkip()
	}
}

// skipChecked consumes the rest of the node that starts with ev, as skip does,
// and refuses a mapping inside it that repeats a key. The value of a key the
// target ignores is never decoded, and without this a duplicate key in it would
// pass unseen where the same document read into a map is refused.
//
// Aliases are not expanded, as in skip: an alias used as a key is not compared.
func (b *Binder) skipChecked(ev *parse.Event) error {
	if ev.Kind != parse.SequenceStart && ev.Kind != parse.MappingStart {
		return nil
	}
	base := len(b.skipFrames)
	b.pushSkip(ev.Kind == parse.MappingStart)
	for len(b.skipFrames) > base {
		e, err := b.next()
		if err != nil {
			b.unwindSkip(base)
			return err
		}
		top := &b.skipFrames[len(b.skipFrames)-1]
		isKey := top.mapping && top.n%2 == 0
		switch e.Kind {
		case parse.SequenceEnd, parse.MappingEnd:
			b.popSkip()
			continue
		case parse.SequenceStart, parse.MappingStart:
			top.n++
			b.pushSkip(e.Kind == parse.MappingStart)
			continue
		case parse.Scalar:
			top.n++
			if isKey {
				// b.kev, not e: scalar passes its argument to the input through an
				// interface, so an event whose address it takes is allocated.
				b.kev = e
				text, err := b.scalar(&b.kev)
				if err == nil {
					b.idbuf, err = b.identity(b.idbuf[:0], &b.kev, text)
				}
				if err != nil {
					b.unwindSkip(base)
					return err
				}
				if b.addSkipKey(top, b.idbuf) {
					b.unwindSkip(base)
					return b.dupKey(e.Start)
				}
			}
		default:
			top.n++
		}
	}
	return nil
}

// pushSkip opens a frame for a collection, reusing the room of a frame that
// closed before.
func (b *Binder) pushSkip(mapping bool) {
	n := len(b.skipFrames)
	if n < cap(b.skipFrames) {
		b.skipFrames = b.skipFrames[:n+1]
	} else {
		b.skipFrames = append(b.skipFrames, skipFrame{})
	}
	b.skipFrames[n] = skipFrame{mapping: mapping, aStart: len(b.skipArena), eStart: len(b.skipEnds)}
}
