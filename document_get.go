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
	c := edit.NewCtx(&d.s.tree, d.s.src, d.idx)
	c.MaxAliasHops = d.s.cfg.limits.MaxAliasNodes
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
