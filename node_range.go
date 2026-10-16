package oneyaml

import (
	"strconv"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/pointer"
)

// A loop over the items or entries of a collection may edit the document it
// walks. The edit replaces the tree, so the node numbers the loop holds mean
// nothing afterwards; to go on, the loop needs the place of its collection as a
// pointer, and that can only be worked out while the tree it belongs to is still
// there. Each running loop therefore has a slot in the stream. An edit notes the
// place of every loop whose collection is still valid before it starts (freeze),
// moves that place along when the edit inserts or removes items of a sequence on
// the way to it (noteEdit), and the loop finds its collection by it afterwards
// (refind).
//
// The first slots are a fixed array, so a loop that never edits costs no
// allocation. A loop that finds them taken gets a slot in a growable list, so
// no loop is left without one. A slot is given back when its loop ends, however
// it ends: a deferred release runs once per loop, not per step, and covers a
// loop that a panic leaves.
const rangeSlots = 3

// rangeMore is a slot past the fixed ones, with the place it notes.
type rangeMore struct {
	rangeSlot
	path rangePath
}

// rangeSlot is one running loop. id is 0 for a free slot; it tells loops apart, so
// that a loop that ends late never frees the slot a later loop reuses.
type rangeSlot struct {
	id     uint32
	epoch  uint32 // of the collection's node
	i, doc int32  // the collection's node and document
	kind   cst.Kind
	st     uint8 // rangeFrozen, rangeOK
}

const (
	rangeFrozen = 1 << iota // the place is noted in rngPath
	rangeOK                 // and it still leads to the collection
)

// rangePath is the place of a collection: the reference tokens from the root, and
// whether each of them indexes a sequence.
type rangePath struct {
	toks []string
	seq  []bool
}

// savedRange is what a snapshot keeps of a slot whose place was already noted, so
// that a patch that fails and is rolled back does not leave the place moved.
type savedRange struct {
	id   uint32
	st   uint8
	path rangePath
}

// watch gives the loop over n a slot. The handle it returns names the slot and the
// loop; it is 0 when no slot is free. The ids have their top bit set, so that the
// counter wrapping never gives a loop the id 0 of a free slot. A place left behind
// in rngPath by an earlier loop is never read: freezeRanges writes it first.
func (s *docStream) watch(n Node, kind cst.Kind) uint64 {
	for i := range s.rng {
		if e := &s.rng[i]; e.id == 0 {
			s.rngID++
			// Field by field: building the slot whole goes through a temporary whose
			// vector copy waits for the narrow stores, which costs more than the loop.
			e.id, e.epoch, e.i, e.doc, e.kind, e.st = s.rngID|1<<31, n.gen, n.i, n.doc, kind, 0
			return uint64(i+1)<<32 | uint64(e.id)
		}
	}
	s.rngID++
	id := s.rngID | 1<<31
	m := rangeMore{rangeSlot: rangeSlot{id: id, epoch: n.gen, i: n.i, doc: n.doc, kind: kind}}
	es := s.es()
	for i := range es.rngMore {
		if es.rngMore[i].id == 0 {
			es.rngMore[i] = m
			return uint64(rangeSlots+i+1)<<32 | uint64(id)
		}
	}
	es.rngMore = append(es.rngMore, m)
	return uint64(rangeSlots+len(es.rngMore))<<32 | uint64(id)
}

// nslots is the number of slots, taken or free.
func (s *docStream) nslots() int {
	if s.ed == nil {
		return rangeSlots
	}
	return rangeSlots + len(s.ed.rngMore)
}

// slotAt returns slot i, counting the fixed ones first.
func (s *docStream) slotAt(i int) *rangeSlot {
	if i < rangeSlots {
		return &s.rng[i]
	}
	return &s.ed.rngMore[i-rangeSlots].rangeSlot
}

// pathAt returns where slot i notes the place of its collection.
func (s *docStream) pathAt(i int) *rangePath {
	if i < rangeSlots {
		return &s.es().rngPath[i]
	}
	return &s.ed.rngMore[i-rangeSlots].path
}

// slot returns the slot a handle names, or nil if the loop no longer has it.
func (s *docStream) slot(h uint64) *rangeSlot {
	if h == 0 {
		return nil
	}
	if i := int(h>>32) - 1; i < s.nslots() && s.slotAt(i).id == uint32(h) {
		return s.slotAt(i)
	}
	return nil
}

// unwatch frees the slot of a loop, if it still has it. The place noted for it, if
// any, is dropped when the slot is next used. Free slots at the end of the list
// are cut off, so the list is as long as the deepest running loop needs.
func (s *docStream) unwatch(h uint64) {
	i := int(h>>32) - 1
	if i < 0 || i >= s.nslots() || s.slotAt(i).id != uint32(h) {
		return
	}
	s.slotAt(i).id = 0
	if i >= rangeSlots {
		m := &s.ed.rngMore
		(*m)[i-rangeSlots].path = rangePath{}
		for n := len(*m); n > 0 && (*m)[n-1].id == 0; n-- {
			*m = (*m)[:n-1]
		}
	}
}

// freezeRanges notes the place of the collection of each running loop, before an
// edit changes the tree. A loop already noted is left alone, and so is one whose
// node is already stale: its place is not known any more, and it ends.
func (s *docStream) freezeRanges() {
	for i, n := 0, s.nslots(); i < n; i++ {
		e := s.slotAt(i)
		if e.id == 0 || e.st&rangeFrozen != 0 {
			continue
		}
		e.st = rangeFrozen
		if e.epoch != s.epoch() {
			continue
		}
		n := Node{s: s, i: e.i, doc: e.doc, gen: e.epoch}
		if toks, seq, err := n.path(true); err == nil {
			*s.pathAt(i) = rangePath{toks, seq}
			e.st |= rangeOK
		}
	}
}

// resume is how a loop goes on after an edit made its collection stale. It finds
// the collection of the loop again, in the edited document, and returns the epoch
// of its nodes now together with the child at child index at, the one the loop
// yielded last, which the loop steps on from. It fails when the loop has no slot,
// had no place noted, or the place no longer leads to a collection of the same kind.
func (s *docStream) resume(h uint64, at int) (gen uint32, child int32, ok bool) {
	coll, ok := s.refind(h)
	if !ok {
		return 0, 0, false
	}
	return s.epoch(), s.childAt(coll, at), true
}

// refind finds the collection of the loop in slot h in the edited document and
// records it in the slot as seen at the current epoch.
func (s *docStream) refind(h uint64) (coll int32, ok bool) {
	e := s.slot(h)
	if e == nil || e.st != rangeFrozen|rangeOK {
		return 0, false
	}
	slot := int(h>>32) - 1
	c := edit.NewCtxLagging(&s.tree, s.src, int(e.doc)) // reads only keys, which settle themselves
	s.attach(c)
	c.MaxAliasHops = s.cfg.limits.MaxAliasNodes
	loc, err := c.Resolve(s.pathAt(slot).toks, false)
	if err != nil || loc.Node == 0 || s.tree.Nodes[loc.Node].Kind != e.kind {
		return 0, false
	}
	e.i, e.epoch, e.st = loc.Node, s.epoch(), 0
	*s.pathAt(slot) = rangePath{}
	return loc.Node, true
}

// ranging reports whether any loop over a collection is running.
func (s *docStream) ranging() bool {
	return s.rng[0].id|s.rng[1].id|s.rng[2].id != 0 || s.ed != nil && len(s.ed.rngMore) > 0
}

// noteEdit follows a successful edit with the places of the running loops. The
// batched removal of keys is not noted: a loop whose key was removed finds
// nothing there, and a key written again goes through edit.
func (s *docStream) noteEdit(doc int, op editKindOp, ptr string) {
	if toks, err := pointer.Parse(ptr); err == nil {
		s.noteEditAt(doc, op, toks)
	} else {
		s.noteEditAt(doc, op, nil)
	}
}

// noteEditAt moves the place of each loop whose collection the edit at toks
// shifts, and drops the place of each loop whose collection it replaces or
// removes. An edit below a collection leaves its place alone.
func (s *docStream) noteEditAt(doc int, op editKindOp, toks []string) {
	for i, n := 0, s.nslots(); i < n; i++ {
		e := s.slotAt(i)
		if e.id == 0 || e.st != rangeFrozen|rangeOK || int(e.doc) != doc {
			continue // not running, no place noted, or in another document
		}
		if !s.pathAt(i).follow(op, toks) {
			e.st &^= rangeOK
		}
	}
}

// follow moves the place p for the edit op at toks. It reports false when p no
// longer names the collection it did, or when it cannot tell.
func (p *rangePath) follow(op editKindOp, toks []string) bool {
	if len(toks) == 0 {
		return false // the root is replaced
	}
	if len(toks) > len(p.toks) {
		return true // below the collection
	}
	d := len(toks) - 1
	for j := 0; j < d; j++ {
		if toks[j] != p.toks[j] {
			return true // another branch
		}
	}
	t := toks[d]
	if !p.seq[d] {
		return t != p.toks[d] // a mapping: the same key is replaced or removed
	}
	at, _ := strconv.Atoi(p.toks[d])
	switch op {
	case opAdd:
		if t == "-" {
			return true
		}
		idx, ok := pointer.Index(t)
		if !ok {
			return false
		}
		if idx <= at {
			p.toks[d] = strconv.Itoa(at + 1)
		}
	case opRemove:
		idx, ok := pointer.Index(t)
		if !ok || idx == at {
			return false
		}
		if idx < at {
			p.toks[d] = strconv.Itoa(at - 1)
		}
	default: // set and replace put a value at an index; past the end it appends
		if t == "-" {
			return true
		}
		idx, ok := pointer.Index(t)
		if !ok || idx == at {
			return false
		}
	}
	return true
}

// saveRanges keeps the noted places for a snapshot; it is nil when none is noted.
func (s *docStream) saveRanges() []savedRange {
	var out []savedRange
	for i, n := 0, s.nslots(); i < n; i++ {
		if e := s.slotAt(i); e.id != 0 && e.st&rangeFrozen != 0 {
			src := s.pathAt(i)
			p := rangePath{seq: src.seq} // never changed in place
			p.toks = append(p.toks, src.toks...)
			out = append(out, savedRange{e.id, e.st, p})
		}
	}
	return out
}

// restoreRanges follows a rollback. A loop whose place was noted before the
// snapshot gets it back as it was; one whose collection is valid again was only
// noted by the edits that were undone, and is unfrozen.
func (s *docStream) restoreRanges(sn docSnapshot) {
	for i, n := 0, s.nslots(); i < n; i++ {
		e := s.slotAt(i)
		if e.id == 0 {
			continue
		}
		done := false
		for _, sv := range sn.rng {
			if sv.id == e.id {
				e.st = sv.st
				*s.pathAt(i) = sv.path
				done = true
				break
			}
		}
		if !done && e.epoch == s.epoch() {
			e.st = 0
			if i >= rangeSlots || s.ed != nil {
				*s.pathAt(i) = rangePath{}
			}
		}
	}
}

// keepCursor keeps the child cursor (see childAt) valid across an edit, so that a
// loop that edits as it goes, and asks for the child at its place after every edit,
// does not walk the siblings from the first again each time, which would make a loop
// over a large collection quadratic. gen is the stream's generation before the edit.
//
// An edit that replaced the nodes lo..hi, children of collection cn, by dn more
// nodes (and dkids more children) leaves the nodes before lo as they were and moves
// those after hi by dn. A cursor inside the run goes back to the start of the run,
// whose child position is pos; one in a collection inside it is dropped. An edit
// that changed no node number passes lo = hi = 0 and cn = 0.
func (s *docStream) keepCursor(gen uint32, lo, hi, dn, cn int32, pos, dkids int) {
	cu := &s.dec.cur
	if cu.gen != gen || cu.node == 0 {
		return
	}
	if lo == 0 { // no node moved
		cu.gen = s.gen
		return
	}
	switch {
	case cu.node < lo && cu.parent < lo:
		cu.gen = s.gen
	case cu.node > hi && cu.parent < lo:
		cu.node += dn
		if cu.parent == cn {
			cu.pos += dkids
		}
		cu.gen = s.gen
	case cu.node > hi && cu.parent > hi:
		cu.node += dn
		cu.parent += dn
		cu.gen = s.gen
	case cu.parent == cn && cu.node >= lo && cu.node <= hi:
		*cu = childCursor{parent: cn, gen: s.gen, pos: pos, node: lo}
	}
}
