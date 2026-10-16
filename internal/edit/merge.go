package edit

import (
	"fmt"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/scan"
)

// isMergeKey reports whether the key node k is the merge key: a plain, untagged
// "<<", the same test the decoder applies.
func (c *Ctx) isMergeKey(k int32) bool {
	c.T.SettleTo(k) // the offsets of a key may be stale on the lazy path
	nd := &c.T.Nodes[k]
	if nd.Kind != cst.Scalar || nd.Style != scan.Plain || nd.End-nd.Start != 2 {
		return false
	}
	if nd.Prop != 0 && c.T.Props[nd.Prop].HasTag() {
		return false
	}
	return c.Src[nd.Start] == '<' && c.Src[nd.Start+1] == '<'
}

// mergeSources appends to dst the mappings that the merge key of mapping m
// names, in the order the decoder prefers them: an alias or inline mapping, or
// the aliases and mappings of a sequence. Anything else is skipped; the decoder
// reports it as an error, a pointer read just does not find the key there.
func (c *Ctx) mergeSources(dst []int32, m int32, hops *int) []int32 {
	add := func(n int32) {
		if c.T.Nodes[n].Kind == cst.Alias {
			*hops++
			n = c.anchorOf(n)
			if n == 0 {
				return
			}
		}
		if c.T.Nodes[n].Kind == cst.Mapping {
			dst = append(dst, n)
		}
	}
	for k := c.T.First(m); k != 0; {
		v := c.T.Nodes[k].Next
		if v == 0 {
			break
		}
		if c.isMergeKey(k) {
			if c.T.Nodes[v].Kind == cst.Sequence {
				for e := c.T.First(v); e != 0; e = c.T.Nodes[e].Next {
					add(e)
				}
			} else {
				add(v)
			}
			return dst
		}
		k = c.T.Nodes[v].Next
	}
	return dst
}

// MergeBudgetError is a merge search that ran out of its bound: the sources it
// read (their keys and the aliases it followed) went past Max. Max is the
// context's MaxAliasHops. It is not "not found": the answer is unknown.
type MergeBudgetError struct{ Max int }

func (e *MergeBudgetError) Error() string {
	return fmt.Sprintf("merge search read more than %d nodes of merge sources", e.Max)
}

// MergedValue finds the value that key tok has in mapping cn through its merge
// key; the mapping's own keys have already been checked. It reads like the
// decoder: the sources in order, each with its own keys before its own merges,
// so an earlier source wins over a later one. It returns 0 when no source has
// the key. Each source mapping is read once, however many ways it is reached
// (a source searched without a match cannot match later), and the entries and
// aliases read are charged against MaxAliasHops: a search that needs more
// returns a *MergeBudgetError.
func (c *Ctx) MergedValue(cn int32, tok string) (int32, error) {
	max := c.MaxAliasHops
	if max <= 0 {
		max = 1 << 20
	}
	work := 0
	var buf [4]int32
	var tmp []int32
	// A small walk keeps its visited sources in a slice; a wide one switches to a set.
	var seenBuf [8]int32
	seen := seenBuf[:0]
	var seenSet map[int32]struct{}
	visit := func(m int32) bool { // reports whether m was already visited
		if seenSet != nil {
			if _, ok := seenSet[m]; ok {
				return true
			}
			seenSet[m] = struct{}{}
			return false
		}
		for _, x := range seen {
			if x == m {
				return true
			}
		}
		if len(seen) == cap(seen) && len(seen) >= len(seenBuf) {
			seenSet = make(map[int32]struct{}, 2*len(seen))
			for _, x := range seen {
				seenSet[x] = struct{}{}
			}
			seenSet[m] = struct{}{}
			return false
		}
		seen = append(seen, m)
		return false
	}
	visit(cn)
	c.MergeWork += int32(c.T.Count(cn))
	work += c.T.Count(cn)
	if work > max {
		return 0, &MergeBudgetError{Max: max}
	}
	// stack holds the sources still to read, the next one last.
	stack := c.mergeSources(buf[:0], cn, &work)
	for i, j := 0, len(stack)-1; i < j; i, j = i+1, j-1 {
		stack[i], stack[j] = stack[j], stack[i]
	}
	for len(stack) > 0 {
		m := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visit(m) {
			continue
		}
		// Charge the keys before reading them: one scan to find tok and one to find
		// the mapping's own merge key.
		n := c.T.Count(m) + 1
		c.MergeWork += int32(n)
		work += n
		if work > max {
			return 0, &MergeBudgetError{Max: max}
		}
		v, _, amb := c.findKey(m, tok)
		if amb {
			return 0, wrap(ErrAmbiguous, "key %q occurs more than once", tok)
		}
		if v != 0 {
			return v, nil
		}
		tmp = c.mergeSources(tmp[:0], m, &work)
		if work > max {
			return 0, &MergeBudgetError{Max: max}
		}
		for i := len(tmp) - 1; i >= 0; i-- {
			stack = append(stack, tmp[i])
		}
	}
	return 0, nil
}

// MergedKeyError is the refusal to remove or replace a key that only a merge
// source has: the change would be a write into the source.
func MergedKeyError(tok string) error {
	return wrap(ErrAlias, "the key %q comes from a merge; Set adds a key of its own that overrides it", tok)
}
