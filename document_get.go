package oneyaml

import (
	"errors"

	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/pointer"
)

// Get reads the value at the RFC 6901 pointer ptr into v, as Decode does for the
// whole document: v is any target Unmarshal accepts. Aliases are followed, and
// expanding them is charged to the alias limits. A *TypeError's Path is the
// pointer ptr followed by the path inside the value.
//
// With MergeKeys, a pointer also finds the keys a "<<" merge brings in, with the
// precedence Decode gives them: a key the mapping has itself wins, and an earlier
// merge source wins over a later one. The pointer to the merge key itself still
// reads the merged source as written. Without MergeKeys the pointer addresses
// only the keys as written.
//
// If v is a *Node it receives the node itself, and the alias at the end of the
// pointer, if there is one, is not followed.
func (d *Document) Get(ptr string, v any) error {
	fail := func(kind error) error { return &EditError{Op: "get", Pointer: ptr, Kind: kind} }
	if d.isNil() {
		return fail(ErrNilDocument)
	}
	if !d.valid() {
		return fail(ErrNotFound)
	}
	toks, err := pointer.Parse(ptr)
	if err != nil {
		return fail(ErrInvalidPointer)
	}
	d.s.tree.Settle() // (NewCtx would do it, but it is too big to inline and the Ctx would be put on the heap)
	c := edit.NewCtxLagging(&d.s.tree, d.s.src, d.idx)
	c.MaxAliasHops = d.s.cfg.limits.MaxAliasNodes
	c.Merge = d.s.cfg.mergeKeys
	loc, err := c.Resolve(toks, false)
	if err != nil {
		return fail(editKind(err))
	}
	if loc.Node == 0 {
		return fail(ErrNotFound)
	}
	n := d.node(loc.Node)
	if t, ok := v.(*Node); ok {
		*t = n
		return nil
	}
	err = n.Decode(v)
	var te *TypeError
	if errors.As(err, &te) {
		te.Path = ptr + te.Path
	}
	return err
}
