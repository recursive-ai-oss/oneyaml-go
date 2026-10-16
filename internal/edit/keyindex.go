package edit

import (
	"bytes"
	"hash/maphash"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/scan"
)

// keyIndexMin is the number of entries from which a mapping is worth an index:
// below it a scan of the keys is as cheap as a lookup.
const keyIndexMin = 256

// keyIndexBuildAfter is how many scans of the same large mapping, after the first,
// are made before an index is built for it: one edit scans a mapping once or twice, so an index is
// made only when the mapping is being edited again and again.
const keyIndexBuildAfter = 2

// Cache holds what a caller keeps between the contexts of one stream so that
// repeated work on a large document does not repeat its reading. It is valid for
// one version of the tree, named by Gen; the owner of the stream sets Gen when it
// hands the cache to a context.
type Cache struct {
	Gen     uint32
	Anchors *AnchorIndex
	// Keys is made when a large mapping is scanned a second time in a row.
	Keys *KeyIndex
	// pend is the large mapping scanned last while there was no Keys.
	pend int32
}

// KeyIndex maps the keys of large mappings to their nodes, so that the lookup of
// a key, and with it an add to a mapping, does not read every key. There is a slot
// for a few mappings. A slot is built when its mapping has been scanned a few
// times, and is kept up to date by the edits that only add a key at the end
// (AddKey) or leave every key where it is (KeepKeys); any other edit makes it
// stale, and a stale slot is dropped and built again only after more scans.
//
// Retention: an index lives as long as its stream does, about eight bytes per key
// plus the table, and is not dropped between calls. This is what lets a loop of
// separate edits of one large mapping run in linear time; a one-off edit never
// builds one, because a mapping must be scanned repeatedly first.
type KeyIndex struct {
	slots [4]keySlot
	next  int
	// Compared counts the keys read to look keys up or to build an index; tests use
	// it to show that the work of an add does not grow with the mapping.
	Compared int
}

// Built returns how many mappings have an index.
func (ki *KeyIndex) Built() int {
	n := 0
	for i := range ki.slots {
		if ki.slots[i].tab != nil {
			n++
		}
	}
	return n
}

// keyEntry is a key node and the hash of its text. The hash is kept so that the
// table can grow without reading the keys again.
type keyEntry struct {
	idx  int32 // 0 for an empty place
	hash uint32
}

type keySlot struct {
	cn   int32 // the mapping
	gen  uint32
	n    int32      // entries the index covers
	seen int32      // scans since the slot was reset
	dead int32      // entries of removed keys still in the table
	tab  []keyEntry // open addressing, power of two long; nil when there is no index
	lay  layoutMemo // what discover found for this mapping
}

// layoutMemo is the indentation discover found for new nested collections.
type layoutMemo struct {
	ok           bool
	step, seqOff int
}

var keySeed = maphash.MakeSeed()

func hashKey(b []byte) uint32 { return uint32(maphash.Bytes(keySeed, b)) }

// slot returns the slot for mapping cn, taking the next one in turn if there is
// none.
func (ki *KeyIndex) slot(cn int32) *keySlot {
	for i := range ki.slots {
		if ki.slots[i].cn == cn {
			return &ki.slots[i]
		}
	}
	s := &ki.slots[ki.next]
	ki.next = (ki.next + 1) % len(ki.slots)
	*s = keySlot{cn: cn}
	return s
}

// memoSlot returns the slot of mapping par when it holds a valid index, or nil: the
// slot is where the layout found for the mapping is kept.
func (c *Ctx) memoSlot(par int32) *keySlot {
	if c.Cache == nil || c.Cache.Keys == nil || par == 0 {
		return nil
	}
	for i := range c.Cache.Keys.slots {
		if s := &c.Cache.Keys.slots[i]; s.cn == par && s.tab != nil && s.gen == c.Cache.Gen && int(s.n) == c.T.Count(par)/2 {
			return s
		}
	}
	return nil
}

// HasKeyIndex reports whether a valid key index exists for mapping par, so that a
// key look-up in it is a probe and not a scan.
func (c *Ctx) HasKeyIndex(par int32) bool { return c.memoSlot(par) != nil }

// KeepKeys marks the indexes that were valid at version from as valid at version
// to: the edit between them moved no key.
func (ca *Cache) KeepKeys(from, to uint32) {
	if ca.Keys == nil {
		return
	}
	for i := range ca.Keys.slots {
		if s := &ca.Keys.slots[i]; s.tab != nil && s.gen == from {
			s.gen = to
		}
	}
}

// KeyHash is the hash a key index files the decoded text of a key under.
func KeyHash(text []byte) uint32 { return hashKey(text) }

// KeyHashString is KeyHash for a string.
func KeyHashString(text string) uint32 { return uint32(maphash.String(keySeed, text)) }

// RenameKey records that the edit from version from to version to changed the text
// of key node k of mapping cn from one that hashed to oldHash into one that hashes to newHash, and moved
// no node. The entry is replaced in place, so a loop that renames many keys of one
// mapping keeps its index instead of reading every key again for each rename. The
// indexes of other mappings are untouched by the edit and stay valid.
func (ca *Cache) RenameKey(cn, k int32, oldHash, newHash uint32, from, to uint32) {
	if ca.Keys == nil {
		return
	}
	for i := range ca.Keys.slots {
		s := &ca.Keys.slots[i]
		if s.tab == nil || s.gen != from {
			continue
		}
		if s.cn != cn {
			s.gen = to
			continue
		}
		mask := uint32(len(s.tab) - 1)
		found := false
		for j := oldHash & mask; ; j = (j + 1) & mask {
			e := &s.tab[j]
			if e.idx == 0 {
				break
			}
			if e.idx == k {
				if e.hash == oldHash {
					e.idx = -1 // keeps the probe sequence of the keys behind it
					s.dead++
					found = true
				}
				break
			}
		}
		if !found || int(s.dead)*4 > len(s.tab) {
			s.tab = nil // not where it should be, or mostly renamed keys: build again
			continue
		}
		s.insert(keyEntry{idx: k, hash: newHash})
		s.gen = to
	}
}

// AddKey records that the edit from version from to version to added the key text
// at the end of mapping cn and changed nothing else about the keys. The tree t is
// the one after the edit. The entries just before the new key are looked up again
// first: they are the ones an edit at the end re-reads, so if one has moved the
// index is dropped.
func (ca *Cache) AddKey(c *Ctx, cn int32, text string, from, to uint32) {
	t := c.T
	if ca.Keys == nil {
		return
	}
	for i := range ca.Keys.slots {
		s := &ca.Keys.slots[i]
		if s.cn != cn || s.tab == nil || s.gen != from {
			continue
		}
		if t.Nodes[cn].Kind != cst.Mapping || t.Count(cn)/2 != int(s.n)+1 {
			s.tab = nil
			return
		}
		key := t.Prev(t.Last(cn))
		if key == 0 || t.Nodes[key].Kind != cst.Scalar {
			s.tab = nil
			return
		}
		kt, _ := c.keyBytes(key)
		if string(kt) != text {
			s.tab = nil
			return
		}
		// The two entries before the new one.
		prev := key
		for j := 0; j < 2; j++ {
			if prev = t.Prev(prev); prev == 0 {
				break
			}
			if prev = t.Prev(prev); prev == 0 {
				break
			}
			if !s.has(c, prev) {
				s.tab = nil
				return
			}
		}
		if 2*(int(s.n)+1) > len(s.tab) {
			s.grow()
		}
		s.insert(keyEntry{idx: key, hash: hashKey(kt)})
		s.n++
		s.gen = to
		if v := t.Nodes[key].Next; v != 0 && t.First(v) != 0 {
			s.lay.ok = false // a nested collection at the end may change the layout
		}
		return
	}
}

// RemoveKey records that the edit from version from to version to removed the
// entry of mapping cn whose nodes were lo through hi, and changed nothing else
// about the keys. Every key after the entry moved down by the nodes it held. The
// indexes of other mappings are dropped: their nodes may have moved too.
func (ca *Cache) RemoveKey(cn, lo, hi int32, from, to uint32) {
	if ca.Keys == nil {
		return
	}
	gone := hi - lo + 1
	for i := range ca.Keys.slots {
		s := &ca.Keys.slots[i]
		if s.tab == nil {
			continue
		}
		if s.cn != cn || s.gen != from || s.cn >= lo {
			s.tab = nil
			continue
		}
		for j := range s.tab {
			e := &s.tab[j]
			switch {
			case e.idx > hi:
				e.idx -= gone
			case e.idx >= lo:
				e.idx = -1 // keeps the probe sequence of the keys behind it
				s.dead++
			}
		}
		s.n--
		s.gen = to
		if int(s.dead)*4 > len(s.tab) {
			s.tab = nil // mostly removed keys: build again
		}
	}
}

// has reports whether the table holds key node k at the place its text hashes to.
func (s *keySlot) has(c *Ctx, k int32) bool {
	kt, ok := c.keyBytes(k)
	if !ok {
		return true // not a scalar: never indexed
	}
	h := hashKey(kt)
	mask := uint32(len(s.tab) - 1)
	for i := h & mask; ; i = (i + 1) & mask {
		e := s.tab[i]
		if e.idx == 0 {
			return false
		}
		if e.idx == k {
			return e.hash == h
		}
	}
}

func (s *keySlot) insert(e keyEntry) {
	mask := uint32(len(s.tab) - 1)
	i := e.hash & mask
	for s.tab[i].idx != 0 {
		i = (i + 1) & mask
	}
	s.tab[i] = e
}

// grow doubles the table, from the hashes it holds.
func (s *keySlot) grow() {
	old := s.tab
	s.tab = make([]keyEntry, 2*len(old))
	s.dead = 0
	for _, e := range old {
		if e.idx > 0 {
			s.insert(e)
		}
	}
}

// KeyBytes is keyBytes for callers outside the package: the decoded text of key
// node k, valid until the next call; ok is false for a key that is not a scalar.
func (c *Ctx) KeyBytes(k int32) (text []byte, ok bool) { return c.keyBytes(k) }

// keyBytes returns the decoded text of key node k, which is valid until the next
// call; ok is false for a key that is not a scalar. Plain and quoted keys without
// escapes or line breaks are returned as they stand in the source.
func (c *Ctx) keyBytes(k int32) (text []byte, ok bool) {
	c.T.SettleTo(k)
	nd := &c.T.Nodes[k]
	if nd.Kind != cst.Scalar {
		return nil, false
	}
	raw := c.Src[nd.Start:nd.End]
	switch nd.Style {
	case scan.Plain:
		if bytes.IndexByte(raw, '\n') < 0 && bytes.IndexByte(raw, '\r') < 0 {
			return raw, true
		}
	case scan.SingleQuoted:
		in := raw[1 : len(raw)-1]
		if bytes.IndexByte(in, '\'') < 0 && bytes.IndexByte(in, '\n') < 0 && bytes.IndexByte(in, '\r') < 0 {
			return in, true
		}
	case scan.DoubleQuoted:
		in := raw[1 : len(raw)-1]
		if bytes.IndexByte(in, '\\') < 0 && bytes.IndexByte(in, '\n') < 0 && bytes.IndexByte(in, '\r') < 0 {
			return in, true
		}
	}
	c.scratch = DecodeScalar(c.scratch[:0], c.T, c.Src, k)
	return c.scratch, true
}

// findKey looks for the key tok among the keys of mapping cn. It returns the value
// node and the key node, both 0 if there is no such key, and ambiguous if the key
// occurs more than once.
func (c *Ctx) findKey(cn int32, tok string) (value, key int32, ambiguous bool) {
	return c.findKeyFrom(cn, tok, 2*keyIndexMin)
}

// findKeyFrom is findKey for a mapping of at least min nodes getting an index.
func (c *Ctx) findKeyFrom(cn int32, tok string, min int) (value, key int32, ambiguous bool) {
	big := c.Cache != nil && c.T.Count(cn) >= min
	if big {
		if c.Cache.Keys == nil {
			if c.Cache.pend != cn {
				// The first scan of a mapping costs no memory: one edit scans once.
				c.Cache.pend = cn
				return c.scanKey(cn, tok, false)
			}
			c.Cache.Keys = new(KeyIndex)
		}
		s := c.Cache.Keys.slot(cn)
		if s.tab != nil && (s.gen != c.Cache.Gen || int(s.n) != c.T.Count(cn)/2) {
			s.tab, s.seen = nil, 0 // stale
		}
		if s.tab == nil {
			if s.seen++; s.seen >= keyIndexBuildAfter {
				c.buildKeys(s, cn)
			}
		}
		if s.tab != nil {
			if v, k, amb, ok := c.probeKey(s, cn, tok); ok {
				return v, k, amb
			}
			s.tab, s.seen = nil, 0 // an entry that no longer holds: scan instead
		}
	}
	return c.scanKey(cn, tok, big)
}

// FindKey is findKey for a reader outside the package: the value and key nodes
// of tok in mapping cn (0 when absent), and whether tok occurs more than once.
// A reader that looks keys up one after the other and must see every key to spot
// a repeat passes a lower indexMin (a node count) than edits use.
func (c *Ctx) FindKey(cn int32, tok string, indexMin int) (value, key int32, ambiguous bool) {
	return c.findKeyFrom(cn, tok, indexMin)
}

// findKeyLight is findKey for a look-up that comes with another on the same
// mapping in one edit: it uses an index that exists and reads the keys otherwise,
// without counting towards the scans that make an index. One rename looks up two
// keys, and an index built for that would cost more than the edit.
func (c *Ctx) findKeyLight(cn int32, tok string) (value, key int32, ambiguous bool) {
	if s := c.memoSlot(cn); s != nil {
		if v, k, amb, ok := c.probeKey(s, cn, tok); ok {
			return v, k, amb
		}
	}
	return c.scanKey(cn, tok, false)
}

// scanKey looks for key tok by reading the keys of mapping cn in turn. counted
// says whether the keys read are added to the count.
func (c *Ctx) scanKey(cn int32, tok string, counted bool) (value, key int32, ambiguous bool) {
	var found, fkey int32
	read := 0
	for k := c.T.First(cn); k != 0; {
		v := c.T.Nodes[k].Next
		read++
		if c.keyIs(k, tok) {
			if found != 0 {
				if counted {
					c.Cache.Keys.Compared += read
				}
				return 0, 0, true
			}
			found, fkey = v, k
		}
		if v == 0 {
			break
		}
		k = c.T.Nodes[v].Next
	}
	if counted {
		c.Cache.Keys.Compared += read
	}
	return found, fkey, false
}

// probeKey looks tok up in the table of slot s. ok is false when the table does
// not agree with the tree (an entry that is not a key of cn, or whose hash matches
// but whose text does not), in which case the caller scans.
func (c *Ctx) probeKey(s *keySlot, cn int32, tok string) (value, key int32, ambiguous, ok bool) {
	h := uint32(maphash.String(keySeed, tok))
	mask := uint32(len(s.tab) - 1)
	var found, fkey int32
	for i := h & mask; ; i = (i + 1) & mask {
		e := s.tab[i]
		if e.idx == 0 {
			break
		}
		if e.idx < 0 || e.hash != h { // a removed key, or another key
			continue
		}
		c.Cache.Keys.Compared++
		if e.idx < 0 || int(e.idx) >= len(c.T.Nodes) {
			return 0, 0, false, false
		}
		kn := &c.T.Nodes[e.idx]
		if kn.Parent != cn || kn.Next == 0 || !c.keyIs(e.idx, tok) {
			return 0, 0, false, false
		}
		if found != 0 {
			return 0, 0, true, true
		}
		found, fkey = kn.Next, e.idx
	}
	return found, fkey, false, true
}

// buildKeys reads the keys of mapping cn into slot s.
func (c *Ctx) buildKeys(s *keySlot, cn int32) {
	n := c.T.Count(cn) / 2
	size := 16
	for size < 2*n+2 {
		size *= 2
	}
	s.tab = make([]keyEntry, size)
	s.dead = 0 // a new table holds no removed keys
	for k := c.T.First(cn); k != 0; {
		v := c.T.Nodes[k].Next
		if v == 0 {
			break
		}
		if kt, ok := c.keyBytes(k); ok {
			s.insert(keyEntry{idx: k, hash: hashKey(kt)})
		}
		k = c.T.Nodes[v].Next
	}
	c.Cache.Keys.Compared += n
	s.gen, s.n = c.Cache.Gen, int32(n)
}
