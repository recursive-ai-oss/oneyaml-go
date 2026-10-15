package patch

import (
	"strconv"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
)

// Copy describes how a value is lifted out of a document so it can be written at
// another place in the same document.
type Copy struct {
	// Old is the document the value is read from. New is the document the value
	// is written into: the same one for a copy, the document after the source was
	// removed for a move.
	Old, New *edit.Ctx
	// Dest is the offset in New's text where the value will start. DestNode is
	// the node of New that is replaced there, or the container the value is added
	// to; an alias to it, or to a node around it, would refer to itself.
	Dest     int
	DestNode int32
	// Moved is the node whose subtree is being moved (0 for a copy). Anchors
	// inside it are gone from New.
	Moved int32
	// MaxNodes bounds the nodes produced by writing aliases out (0: a large
	// bound), and MaxBytes the scalar bytes they hold (0: none).
	MaxNodes, MaxBytes int
	// MaxWritten bounds the text that writing aliases out adds to the value (0:
	// none). The bound is counted in bytes of the anchored values, so it covers
	// what is written and not only the scalars inside. Written receives that
	// total once Value has run, for the caller to charge to a patch-wide bound.
	MaxWritten int
	Written    int
}

// BudgetError reports which bound writing aliases out went past. It matches
// ErrBudget.
type BudgetError struct {
	What string // "nodes" or "bytes"
	Max  int
}

func (e *BudgetError) Error() string {
	return "alias expansion went past " + strconv.Itoa(e.Max) + " " + e.What
}

// Unwrap lets errors.Is match ErrBudget.
func (e *BudgetError) Unwrap() error { return ErrBudget }

// UndefinedAliasError reports an alias in the value with no anchor before it in
// the document it is read from; there is nothing to write out in its place, and
// writing the alias as it is would point at whatever anchor of that name the
// destination happens to have.
type UndefinedAliasError struct{ Name string }

func (e *UndefinedAliasError) Error() string {
	return "alias *" + e.Name + " has no anchor in its document"
}

// Value returns node n of Old as a value to write at Dest. The value has no
// anchor of its own: a second definition of an anchor would capture every alias
// after it. An alias inside it stays an alias when its anchor is still defined
// exactly once and before Dest; otherwise it is written out as a copy of what the
// anchor holds. An alias with no anchor in Old is refused (UndefinedAliasError).
func (c *Copy) Value(n int32) (edit.Value, error) {
	t, src := c.Old.T, c.Old.Src
	v, err := edit.FromTree(t, src, n)
	if err != nil {
		return v, err
	}
	var movedLo, movedHi int32
	if c.Moved != 0 {
		movedLo, movedHi = c.Moved, c.Old.LastDescendant(c.Moved)
	}
	var defs map[string]def
	// keep reports whether the alias stays an alias: its anchor is still defined
	// exactly once in New, before Dest, and is not part of what moves.
	keep := func(name string, anchor int32) bool {
		if defs == nil {
			defs = anchorDefs(c.New)
		}
		d, ok := defs[name]
		return ok && d.count == 1 &&
			!(c.Moved != 0 && anchor >= movedLo && anchor <= movedHi) &&
			int(c.New.T.Nodes[d.node].End) <= c.Dest && !c.around(d.node)
	}
	if err := c.charge(n, keep); err != nil {
		return v, err
	}
	type job struct {
		dst   *edit.Value
		alias int32
	}
	var work []job
	push := func(root int32, rv *edit.Value) {
		an := aliasNodes(t, root)
		if len(an) == 0 {
			return
		}
		av := aliasValues(rv)
		for i := range an {
			if i < len(av) {
				work = append(work, job{av[i], an[i]})
			}
		}
	}
	push(n, &v)
	for len(work) > 0 {
		j := work[len(work)-1]
		work = work[:len(work)-1]
		anchor := c.Old.AnchorOf(j.alias)
		if anchor == 0 {
			return v, &UndefinedAliasError{Name: j.dst.Text}
		}
		if keep(j.dst.Text, anchor) {
			continue
		}
		sub, err := edit.FromTree(t, src, anchor)
		if err != nil {
			return v, err
		}
		*j.dst = sub
		push(anchor, j.dst)
	}
	return v, nil
}

// around reports whether node n of New is DestNode or one of its ancestors.
func (c *Copy) around(n int32) bool {
	for x := c.DestNode; x != 0; x = c.New.T.Nodes[x].Parent {
		if x == n {
			return true
		}
	}
	return false
}

type def struct {
	count int
	node  int32
}

// anchorDefs maps each anchor name of the document to how often it is defined
// and the first such node.
func anchorDefs(c *edit.Ctx) map[string]def {
	m := make(map[string]def)
	t := c.T
	for i := c.Root(); i < c.DocEnd(); i++ {
		p := t.Nodes[i].Prop
		if p == 0 || !t.Props[p].HasAnchor() {
			continue
		}
		name := string(c.Src[t.Props[p].AnchorStart:t.Props[p].AnchorEnd])
		d := m[name]
		if d.count == 0 {
			d.node = i
		}
		d.count++
		m[name] = d
	}
	return m
}

// aliasNodes lists the alias nodes in the subtree of root in document order.
func aliasNodes(t *cst.Tree, root int32) []int32 {
	var out []int32
	stack := []int32{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nd := &t.Nodes[n]
		if nd.Kind == cst.Alias {
			out = append(out, n)
			continue
		}
		// Children go on the stack in reverse so they pop in order.
		start := len(stack)
		for ch := t.First(n); ch != 0; ch = t.Nodes[ch].Next {
			stack = append(stack, ch)
		}
		for a, b := start, len(stack)-1; a < b; a, b = a+1, b-1 {
			stack[a], stack[b] = stack[b], stack[a]
		}
	}
	return out
}

// aliasValues lists the alias values in v in document order.
func aliasValues(v *edit.Value) []*edit.Value {
	var out []*edit.Value
	stack := []*edit.Value{v}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x.Kind == edit.Alias {
			out = append(out, x)
			continue
		}
		for i := len(x.Items) - 1; i >= 0; i-- {
			stack = append(stack, &x.Items[i])
		}
	}
	return out
}

// charge walks the aliases that have to be written out, building nothing, and
// fails as soon as they would produce more than the bounds allow. It runs before
// the value is built so that a hostile document costs no memory past the bound.
// It also refuses an alias with no anchor.
func (c *Copy) charge(n int32, keep func(string, int32) bool) error {
	t, src := c.Old.T, c.Old.Src
	budget := c.MaxNodes
	if budget <= 0 {
		budget = hardCap
	}
	produced, bytes, written := 0, 0, 0
	work := aliasNodes(t, n)
	for len(work) > 0 {
		a := work[len(work)-1]
		work = work[:len(work)-1]
		anchor := c.Old.AnchorOf(a)
		name := string(src[t.Nodes[a].Start+1 : t.Nodes[a].End])
		if anchor == 0 {
			return &UndefinedAliasError{Name: name}
		}
		if keep(name, anchor) {
			continue
		}
		last := c.Old.LastDescendant(anchor)
		produced += int(last-anchor) + 1
		if produced > budget {
			return &BudgetError{What: "nodes", Max: budget}
		}
		written += int(t.Nodes[anchor].End - t.Nodes[anchor].Start)
		if c.MaxBytes > 0 {
			for i := anchor; i <= last; i++ {
				if nd := &t.Nodes[i]; nd.Kind == cst.Scalar {
					bytes += int(nd.End - nd.Start)
				}
			}
			if bytes > c.MaxBytes {
				return &BudgetError{What: "bytes", Max: c.MaxBytes}
			}
		}
		// With a per-value byte bound the walk is already cut off by it, and that
		// bound reports first; without one this keeps the walk short.
		if c.MaxBytes <= 0 && c.MaxWritten > 0 && written > c.MaxWritten {
			return &BudgetError{What: "written", Max: c.MaxWritten}
		}
		work = append(work, aliasNodes(t, anchor)...)
	}
	if c.MaxWritten > 0 && written > c.MaxWritten {
		return &BudgetError{What: "written", Max: c.MaxWritten}
	}
	c.Written = written
	return nil
}
