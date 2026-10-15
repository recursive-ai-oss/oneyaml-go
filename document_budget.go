package oneyaml

import (
	"errors"
	"fmt"
	"math"

	"oneyaml.dev/go/internal/patch"
)

// ErrUndefinedAlias means a value to be written holds an alias whose anchor is not
// in the document it was read from, so there is nothing to write in its place.
var ErrUndefinedAlias = errors.New("oneyaml: alias has no anchor in its document")

// aliasBounds returns the bounds on the nodes and scalar bytes that writing
// aliases out may produce for a document of ownNodes nodes and ownBytes bytes: the
// limits, raised to the configured multiple of the document's own size, as the
// decoder does. A zero bound is none; the node bound then falls back to a fixed
// cap inside the patch package, so a self-referencing anchor cannot loop forever.
func (c *config) aliasBounds(ownNodes, ownBytes int) (nodes, bytes int) {
	nodes, bytes = c.limits.MaxAliasNodes, c.limits.MaxAliasBytes
	if r := c.aliasRatio; r > 0 {
		if nodes > 0 && !c.exactNodes && r*ownNodes > nodes {
			nodes = r * ownNodes
		}
		if bytes > 0 && !c.exactBytes && r*ownBytes > bytes {
			bytes = r * ownBytes
		}
	}
	return nodes, bytes
}

// copier returns the settings for writing out values read from the stream s, at
// the bounds cfg gives for a document of s's size.
func (s *docStream) copier(cfg *config) patch.Copy {
	n, b := cfg.aliasBounds(len(s.tree.Nodes), len(s.src))
	return patch.Copy{MaxNodes: n, MaxBytes: b}
}

// aliasError turns the errors of writing a value out into the public ones; other
// errors pass through. path names where the value was read.
func aliasError(err error, path string) error {
	if err == nil {
		return nil
	}
	var be *patch.BudgetError
	if errors.As(err, &be) {
		opt := "Limits (MaxAliasNodes)"
		if be.What == "bytes" {
			opt = "Limits (MaxAliasBytes)"
		}
		return &LimitError{Limit: "alias expansion " + be.What, Max: be.Max, Option: opt, Path: path}
	}
	var ua *patch.UndefinedAliasError
	if errors.As(err, &ua) {
		return fmt.Errorf("%w: *%s", ErrUndefinedAlias, ua.Name)
	}
	return err
}

// patchWork guards the time one patch may spend: the text its edits parse again.
// Edits that touch a small region read little; an edit that re-reads the whole
// document costs its size, and a patch of many of them costs the square of it.
type patchWork struct {
	s     *docStream
	start editWork
	max   int // 0: unbounded
}

// newPatchWork starts counting for a patch whose text is patchLen bytes.
func (s *docStream) newPatchWork(patchLen int) patchWork {
	w := patchWork{s: s, start: s.es().work}
	w.max = workBound(s.cfg.limits.MaxPatchWorkRatio, len(s.src), patchLen)
	return w
}

// workBound returns the work a patch may cost: ratio times the size of the
// document and the patch, and 16 MiB more. It is 0 (no bound) when the ratio is
// off. A bound that does not fit in an int is held at the largest int, which the
// saturating cost can still reach.
func workBound(ratio, srcLen, patchLen int) int {
	return int(workBoundIn(uint64(math.MaxInt), ratio, srcLen, patchLen))
}

// workBoundIn is workBound for an int that holds at most maxInt, so that the
// arithmetic of a 32-bit build can be tested on any host.
func workBoundIn(maxInt uint64, ratio, srcLen, patchLen int) uint64 {
	if ratio <= 0 {
		return 0
	}
	const floor = 16 << 20
	size := uint64(srcLen) + uint64(patchLen)
	if size > maxInt/uint64(ratio) || uint64(ratio)*size > maxInt-floor {
		return maxInt
	}
	return uint64(ratio)*size + floor
}

// addSat adds non-negative a and b, stopping at the largest int.
func addSat(a, b int) int {
	if a > math.MaxInt-b {
		return math.MaxInt
	}
	return a + b
}

// mulSat multiplies non-negative a and b, stopping at the largest int.
func mulSat(a, b int) int {
	if a != 0 && b > math.MaxInt/a {
		return math.MaxInt
	}
	return a * b
}

// check fails once the edits so far have cost more than the bound allows. The
// cost is the text parsed, with a node counting for eight bytes, and for each edit
// made through a region what it moves: a unit per node after the edited run and
// an eighth of a unit per byte of text after it, since closing the gap an edit
// leaves moves both. An append at the end of a collection moves none.
func (w *patchWork) check(path string) error {
	if w.max == 0 {
		return nil
	}
	if cost := w.cost(); cost >= w.max {
		return &LimitError{Limit: "patch work", Max: w.max, Option: "Limits (MaxPatchWorkRatio)", Path: path}
	}
	return nil
}

// cost is the work done since the patch began; it stops at the largest int
// instead of wrapping.
func (w *patchWork) cost() int {
	cur := w.s.es().work
	c := cur.bytes - w.start.bytes
	c = addSat(c, mulSat(cur.nodes-w.start.nodes, 8))
	c = addSat(c, cur.shifted-w.start.shifted)
	return addSat(c, (cur.moved-w.start.moved)/8)
}

// checkPatchSize refuses a patch text larger than the input limit.
func (s *docStream) checkPatchSize(n int) error {
	if m := s.cfg.limits.MaxInputBytes; m > 0 && n > m {
		return &LimitError{Limit: "patch size", Max: m, Option: "Limits (MaxInputBytes)"}
	}
	return nil
}
