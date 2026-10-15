package cst

import "slices"

// SubtreeEnd returns the index of the last node of the subtree of n. Nodes are
// stored in document order, so a subtree is the run of indexes that starts at n
// and ends just before the node that follows it.
func (t *Tree) SubtreeEnd(n int32) int32 {
	for {
		if nx := t.Nodes[n].Next; nx != 0 {
			return nx - 1
		}
		p := t.Nodes[n].Parent
		if p == 0 {
			// The root of a document: the next document's nodes follow.
			for i := range t.Docs {
				if t.Docs[i].Root == n {
					if i+1 < len(t.Docs) {
						return t.Docs[i+1].Root - 1
					}
					break
				}
			}
			return int32(len(t.Nodes) - 1)
		}
		n = p
	}
}

// Splice replaces a run of consecutive children of the collection c, from
// a to b (the last node of the last entry; for a mapping that is a value), by the
// children of the collection at the root of sub, a tree parsed from the edited
// text of the run alone. prev is the child before a, or 0 when a is first. For a
// flow collection sub is a parse of the run's text inside brackets of the same
// kind, and the brackets of c stay where they are.
//
// Offsets in sub are moved by shift to where the text now sits. Offsets of the
// old tree at or after from move by delta, which is how much the text of the run
// grew. Nodes before the run keep their index; nodes after it move by the change
// in node count. The result is the tree a parse of the whole edited text would
// give.
func (t *Tree) Splice(c, prev, a, b int32, sub *Tree, shift, from, delta int32) {
	lo, hi := a, t.SubtreeEnd(b)
	nxt := t.Nodes[b].Next
	oldKids := int32(0)
	for x := a; ; x = t.Nodes[x].Next {
		oldKids++
		if x == b {
			break
		}
	}
	oldEnd := t.Nodes[c].End
	newN := int32(len(sub.Nodes) - 2)
	dn := newN - (hi - lo + 1)

	// The properties of the run are a contiguous run of Props, since nodes and
	// their properties are created in the same order.
	var pLo, pOld int32
	for i := lo; i <= hi; i++ {
		if p := t.Nodes[i].Prop; p != 0 {
			if pOld == 0 {
				pLo = p
			}
			pOld++
		}
	}
	if pOld == 0 {
		pLo = int32(len(t.Props))
		for i := hi + 1; i < int32(len(t.Nodes)); i++ {
			if p := t.Nodes[i].Prop; p != 0 {
				pLo = p
				break
			}
		}
	}
	pHi := pLo + pOld - 1
	newP := int32(len(sub.Props) - 1)
	dp := newP - pOld

	// Make room, moving the tail.
	oldLen := int32(len(t.Nodes))
	if dn > 0 {
		t.Nodes = slices.Grow(t.Nodes, int(dn))[:oldLen+dn]
	}
	copy(t.Nodes[lo+newN:], t.Nodes[hi+1:oldLen])
	if dn < 0 {
		t.Nodes = t.Nodes[:oldLen+dn]
	}
	oldPLen := int32(len(t.Props))
	if dp > 0 {
		t.Props = slices.Grow(t.Props, int(dp))[:oldPLen+dp]
	}
	copy(t.Props[pLo+newP:], t.Props[pHi+1:oldPLen])
	if dp < 0 {
		t.Props = t.Props[:oldPLen+dp]
	}

	newKids := int32(0)

	// Write the new children.
	nxtNew := nxt
	if nxt > hi {
		nxtNew = nxt + dn
	}
	N := t.Nodes
	lastTop := prev
	for j := int32(0); j < newN; j++ {
		n := sub.Nodes[2+j]
		n.Start += shift
		n.End += shift
		top := n.Parent == 1
		if top {
			n.Parent = c
		} else {
			n.Parent += lo - 2
		}
		if n.Next != 0 {
			n.Next += lo - 2
		} else if top {
			n.Next = nxtNew
		}
		if n.Prop != 0 {
			n.Prop += pLo - 1
		}
		N[lo+j] = n
		if top {
			lastTop = lo + j
			newKids++
		}
	}
	for j := int32(0); j < newP; j++ {
		p := sub.Props[1+j]
		if p.AnchorEnd > p.AnchorStart {
			p.AnchorStart += shift
			p.AnchorEnd += shift
		}
		if p.TagEnd > p.TagStart {
			p.TagStart += shift
			p.TagEnd += shift
		}
		t.Props[pLo+j] = p
	}

	N[c].Indent += newKids - oldKids

	// Link the run into its container.
	first := nxtNew
	if newN > 0 {
		first = lo
	}
	// The first child of c, if the run is at its head, sits at c+1 already.
	if prev != 0 {
		N[prev].Next = first
	}

	// The nodes after the run: indexes and offsets.
	for i := lo + newN; i < int32(len(N)); i++ {
		n := &N[i]
		if n.Parent > hi {
			n.Parent += dn
		}
		if n.Next > hi {
			n.Next += dn
		}
		if n.Prop > pHi {
			n.Prop += dp
		}
		if n.Start >= from {
			n.Start += delta
		}
		if n.End >= from {
			n.End += delta
		}
	}
	for i := pLo + newP; i < int32(len(t.Props)); i++ {
		p := &t.Props[i]
		if p.AnchorStart >= from {
			p.AnchorStart += delta
			p.AnchorEnd += delta
		}
		if p.TagStart >= from {
			p.TagStart += delta
			p.TagEnd += delta
		}
	}
	for i := range t.Docs {
		d := &t.Docs[i]
		if d.Root > hi {
			d.Root += dn
		}
		for j := range d.StartMarker {
			if d.StartMarker[j] >= from {
				d.StartMarker[j] += delta
			}
			if d.EndMarker[j] >= from {
				d.EndMarker[j] += delta
			}
		}
	}

	// The container and the nodes above it: links, and the extent. A collection
	// ends where its last child ends, so when the run was the tail of c every
	// ancestor that ended with it now ends with the new tail.
	changed := nxt == 0 && !N[c].Flow // a flow collection ends at its bracket, not at its last child
	newEnd := oldEnd
	if changed {
		newEnd = N[lastTop].End
		if lastTop == 0 {
			changed = false // cannot happen: a run always leaves a child
		}
	}
	for x := c; x != 0; x = N[x].Parent {
		n := &N[x]
		if n.Next > hi {
			n.Next += dn
		}
		switch {
		case changed && n.End == oldEnd:
			n.End = newEnd
		default:
			changed = false
			if n.End >= from {
				n.End += delta
			}
		}
	}
	if prev == 0 && !N[c].Flow {
		N[c].Start = sub.Nodes[1].Start + shift
		// A document without a start marker begins at its first token.
		for i := range t.Docs {
			if d := &t.Docs[i]; d.Root == c && !d.ExplicitStart && N[c].Prop == 0 {
				d.StartMarker = [2]int32{N[c].Start, N[c].Start}
			}
		}
	}
}
