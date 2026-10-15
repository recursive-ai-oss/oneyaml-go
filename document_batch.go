package oneyaml

import (
	"bytes"
	"slices"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
)

// batchMin is the number of removals from one mapping from which they may be made
// in one pass; batchWork is how many times the text, summed over the removals, must
// lie after the removed entries for the pass to pay: a removal moves the text and
// the nodes after it, while the pass parses the whole text once, and parsing costs
// about as much as moving the rest of the text sixteen times, measured on runs of
// 9 to 40 removals from mappings of 5,000 and 500,000 keys. Tests set batchMin to 1
// to force the pass.
var (
	batchMin  = 8
	batchWork = 16
)

// removeKeys removes the entries with the given keys from the block mapping at
// parent in one pass: each removal is planned against the tree as it is, the
// planned deletions are made to the text together, and the text is parsed once.
// Removing the entries one at a time moves the whole rest of the text and the
// tree for every entry, which for thousands of entries in a large mapping costs
// minutes.
//
// The result is the one the removals give in turn, or the pass does not run. It
// reports false, with the stream untouched, for every layout it does not handle
// (flow mappings, aliases, an entry that shares its line, a keep-chomped block
// scalar before a removed entry, a missing key when mustExist is set, keys given
// twice, removal of every entry, removals whose lines overlap) and when the text
// it makes does not read back as the entries that were not removed. The caller
// then removes the entries one by one, which reports the errors.
func (s *docStream) removeKeys(doc int, parentToks, keys []string, mustExist bool) bool {
	// The pass pays only if the entries together lie after at least batchWork
	// whole texts, and each lies after less than one: fewer keys cannot.
	if len(keys) < batchMin || (batchMin > 1 && len(keys) <= batchWork) || doc < 0 || doc >= len(s.tree.Docs) {
		return false
	}
	s.es().work.batchTries++
	c := s.ctx(doc)
	pl, err := c.Resolve(parentToks, false)
	if err != nil || pl.Node == 0 {
		return false
	}
	t := &s.tree
	par := pl.Node
	pn := &t.Nodes[par]
	// The cheap refusals come before any key is planned, so that a run the pass
	// declines costs little.
	if pn.Kind != cst.Mapping || pn.Flow || pn.Pair || t.First(par) == 0 || 2*len(keys) >= t.Count(par) {
		return false
	}
	seen := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if _, dup := seen[k]; dup {
			return false
		}
		seen[k] = struct{}{}
	}
	toks := append(slices.Clone(parentToks), "")
	if batchMin > 1 {
		// See whether the pass would pay before any removal is planned: when it
		// would not, this look-up is all that is spent.
		after := 0
		if c.HasKeyIndex(par) {
			// The key index of this mapping is built: each key is a probe.
			for _, k := range keys {
				toks[len(toks)-1] = k
				loc, err := c.Resolve(toks, false)
				if err != nil {
					return false
				}
				if loc.Node != 0 {
					after = addSat(after, len(s.src)-int(t.Nodes[loc.Node].Start))
				}
			}
		} else {
			// One pass over the entries, not one scan of the mapping per key. A key
			// that is not a plain scalar is not counted; planning finds it.
			for k := t.First(par); k != 0; {
				v := t.Nodes[k].Next
				if v == 0 {
					break
				}
				if text, ok := c.KeyBytes(k); ok {
					if _, want := seen[string(text)]; want {
						after = addSat(after, len(s.src)-int(t.Nodes[k].Start))
					}
				}
				k = t.Nodes[v].Next
			}
		}
		if after/batchWork < len(s.src) {
			return false // one by one is cheaper than parsing the text
		}
	}
	removed := make(map[int32]struct{}, len(keys))
	var repls []edit.Repl
	for _, k := range keys {
		toks[len(toks)-1] = k
		loc, err := c.Resolve(toks, true)
		if err != nil {
			return false
		}
		if loc.Node == 0 {
			if mustExist {
				return false
			}
			continue
		}
		if loc.Parent != par || loc.Key == 0 {
			return false
		}
		if _, dup := removed[loc.Key]; dup {
			return false
		}
		if lo := c.EntryLo(loc.Key); c.LineStart(lo) != lo {
			return false // the entry shares its line with an indicator
		}
		rs, err := c.Remove(loc)
		if err != nil || len(rs) != 1 || len(rs[0].Text) != 0 {
			return false
		}
		removed[loc.Key] = struct{}{}
		repls = append(repls, rs[0])
	}
	if len(repls) == 0 {
		return true // nothing to remove
	}
	if 2*len(repls) >= t.Count(par) {
		return false
	}
	// A keep-chomped scalar before a removed entry takes the blank lines that
	// follow it, so what stands before the entry decides what is removed with it,
	// and it changes when the entries before are removed; leave that to the
	// one-by-one path.
	for k := range removed {
		p := t.Prev(k)
		if p != 0 && c.EndsWithKeep(p) {
			return false // the plan of this removal took the blank lines after it
		}
		for p != 0 {
			kk := t.Prev(p)
			if _, gone := removed[kk]; !gone {
				break
			}
			p = t.Prev(kk)
		}
		if p != 0 && c.EndsWithKeep(p) {
			return false
		}
	}
	slices.SortFunc(repls, func(a, b edit.Repl) int { return a.Start - b.Start })
	size := len(s.src)
	pos := 0
	for _, r := range repls {
		if r.Start < pos || r.End < r.Start || r.End > len(s.src) {
			return false
		}
		pos = r.End
		size -= r.End - r.Start
	}
	out := make([]byte, 0, size)
	at := 0
	for _, r := range repls {
		out = append(out, s.src[at:r.Start]...)
		at = r.End
	}
	out = append(out, s.src[at:]...)

	nt := s.takeSpare()
	s.ed.work.wholes++
	if err := s.buildTree(&nt, out, s.depth()); err != nil || !sameEntriesKept(t, s.src, par, removed, &nt, out) {
		s.es().spare = nt
		return false
	}
	s.setSrc(out)
	if !s.shared {
		s.es().spare = s.tree
	}
	s.tree, s.dirty, s.shared = nt, true, false
	s.gen++
	s.ed.work.batches++
	return true
}

// sameEntriesKept reports whether the new tree is the old one with the removed
// entries cut out: every other node has the same kind, style, flow flag, tag and
// anchor text, has the same parent, and has the same text (scalars and
// aliases) or the same indentation (block scalars). Offsets are not compared, they
// move; the text of every node is, so a line that the removal left behind and that
// the new parse reads as part of a surviving scalar is caught. One linear walk.
func sameEntriesKept(t *cst.Tree, src []byte, par int32, removed map[int32]struct{}, nt *cst.Tree, out []byte) bool {
	if len(nt.Docs) != len(t.Docs) {
		return false
	}
	// The old node ranges that are gone: a removed key and the subtree of its value.
	gone := make([][2]int32, 0, len(removed))
	for k := range removed {
		v := t.Nodes[k].Next
		if v == 0 {
			return false
		}
		gone = append(gone, [2]int32{k, t.SubtreeEnd(v)})
	}
	slices.SortFunc(gone, func(a, b [2]int32) int { return int(a[0] - b[0]) })
	newIdx := make([]int32, len(t.Nodes))
	next, g := int32(1), 0
	for i := int32(1); int(i) < len(t.Nodes); i++ {
		for g < len(gone) && i > gone[g][1] {
			g++
		}
		if g < len(gone) && i >= gone[g][0] {
			continue // inside a removed entry
		}
		newIdx[i] = next
		next++
	}
	for g := 1; g < len(gone); g++ {
		if gone[g][0] <= gone[g-1][1] {
			return false // ranges overlap
		}
	}
	if int(next) != len(nt.Nodes) {
		return false
	}
	remap := func(o int32) int32 {
		if o == 0 {
			return 0
		}
		return newIdx[o]
	}
	for i := int32(1); int(i) < len(t.Nodes); i++ {
		j := newIdx[i]
		if j == 0 {
			continue
		}
		p, q := &t.Nodes[i], &nt.Nodes[j]
		if p.Kind != q.Kind || p.Style != q.Style || p.Flow != q.Flow || p.Pair != q.Pair {
			return false
		}
		// Nodes are in document order in both trees, so equal parents give equal
		// children in equal order.
		if remap(p.Parent) != q.Parent {
			return false
		}
		if (p.Prop == 0) != (q.Prop == 0) {
			return false
		}
		if p.Prop != 0 {
			a, b := &t.Props[p.Prop], &nt.Props[q.Prop]
			if a.HasAnchor() != b.HasAnchor() || a.HasTag() != b.HasTag() ||
				!bytes.Equal(src[a.AnchorStart:a.AnchorEnd], out[b.AnchorStart:b.AnchorEnd]) ||
				!bytes.Equal(src[a.TagStart:a.TagEnd], out[b.TagStart:b.TagEnd]) {
				return false
			}
		}
		switch p.Kind {
		case cst.Scalar:
			if p.End-p.Start != q.End-q.Start || p.Indent != q.Indent || !bytes.Equal(src[p.Start:p.End], out[q.Start:q.End]) {
				return false
			}
		case cst.Alias:
			if !bytes.Equal(src[p.Start:p.End], out[q.Start:q.End]) {
				return false
			}
		default:
			want := p.Indent
			if i == par {
				want -= int32(2 * len(removed))
			}
			if want != q.Indent {
				return false
			}
		}
	}
	d1, d2 := t.Docs, nt.Docs
	for i := range d1 {
		if remap(d1[i].Root) != d2[i].Root || d1[i].ExplicitStart != d2[i].ExplicitStart || d1[i].ExplicitEnd != d2[i].ExplicitEnd ||
			d1[i].VersionMajor != d2[i].VersionMajor || d1[i].VersionMinor != d2[i].VersionMinor {
			return false
		}
	}
	return true
}
