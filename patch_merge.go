package oneyaml

import (
	"errors"
	"fmt"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/patch"
	"oneyaml.dev/go/internal/pointer"
)

// ApplyMergePatch applies an RFC 7396 JSON Merge Patch, given as JSON text or
// as YAML, to the document. A member of the patch that is null deletes the member of the same
// name; an object member is merged into the mapping it meets; any other value
// replaces what is there, and a sequence replaces the whole sequence. A patch
// that is not an object replaces the whole document.
//
// The patch is atomic: on any error the document is left as it was, byte for
// byte. Members that the patch adds go at the end of their mapping in the style
// of their siblings, and comments and layout outside the changed values stay. A
// null in the target is kept unless the patch names it. Merging an object into a
// mapping that is an alias is refused (ErrAliasWrite) because it would change
// every other use of the anchor; an alias to something else is replaced.
//
// Text whose first significant byte is "{" or "[" is read as JSON only, with JSON
// errors, and so is any text that is valid JSON. Other text is read as one YAML
// document, which may be any value, as a merge patch kept in a YAML file is
// written, and gives the same result as the JSON form. The refusals are those of
// ApplyPatch for a YAML patch: aliases, tags outside the core schema, a
// collection as a key, keys that repeat by value, a scalar with a bad escape and
// more than one document are all ErrInvalidPatch, a YAML syntax error is
// reported with its position, and nesting past the depth limit is a *LimitError.
//
// The result may not be longer than a MaxInputBytes limit, the patch text may
// not be longer than an eighth of it (see LimitSet), and the text its edits have to parse again is bounded (MaxPatchWorkRatio);
// past a bound the call fails with a *LimitError and changes nothing.
//
// The patch applies to the first document, or to the document a view from Doc
// selects.
func (d *Document) ApplyMergePatch(rfc7396 []byte) error {
	if d.isNil() {
		return &EditError{Op: "merge-patch", Kind: ErrNilDocument}
	}
	if err := d.s.checkPatchSize(len(rfc7396)); err != nil {
		return &EditError{Op: "merge-patch", Kind: err}
	}
	v, err := patch.ParseValue(rfc7396, d.s.cfg.limits.MaxDepth)
	if err != nil && errors.Is(err, patch.ErrSyntax) && !looksLikeJSON(rfc7396) {
		// Not JSON: a merge patch kept as a YAML file reads the same way. Text that
		// starts like JSON never gets here and keeps its JSON errors.
		if yv, yerr, ok := d.s.yamlPatchValue(rfc7396, false); ok {
			v, err = yv, yerr
		}
	}
	if err != nil {
		return &EditError{Op: "merge-patch", Kind: kindOf(err, d.s.cfg.limits.MaxDepth)}
	}
	return d.s.mergePatch(d.idx, &v, len(rfc7396))
}

// mergeFrame is a mapping of the patch being merged into the mapping at toks.
type mergeFrame struct {
	toks  []string
	items []edit.Value
	i     int
}

func (s *docStream) mergePatch(doc int, v *edit.Value, patchLen int) error {
	defer s.beginBatch()()
	fail := func(toks []string, kind error) error {
		return &EditError{Op: "merge-patch", Pointer: pointer.Join(toks), Kind: kind}
	}
	snap := s.snapshot()
	work := s.newPatchWork(patchLen)
	if err := s.merge(doc, v, fail, &work); err != nil {
		if rerr := s.rollback(snap); rerr != nil {
			return fail(nil, rerr)
		}
		return err
	}
	// A merge patch can add members without bound, so the result is held to the
	// same output limit as a 6902 patch.
	if max := s.cfg.limits.MaxInputBytes; max > 0 && len(s.src) > max {
		if rerr := s.rollback(snap); rerr != nil {
			return fail(nil, rerr)
		}
		return &EditError{Op: "merge-patch", Kind: &LimitError{Limit: "patch output size", Max: max, Option: "Limits (MaxInputBytes)"}}
	}
	return nil
}

func (s *docStream) merge(doc int, v *edit.Value, fail func([]string, error) error, work *patchWork) error {
	set := func(toks []string, val edit.Value) error {
		ptr := pointer.Join(toks)
		if err := work.check(ptr); err != nil {
			return fail(toks, err)
		}
		if err := s.edit(doc, opSet, ptr, &val); err != nil {
			return unwrapMerge(ptr, err)
		}
		return nil
	}
	if v.Kind != edit.Map {
		return set(nil, *v)
	}
	// kindAt reports how the target looks at toks: a mapping to merge into, an
	// alias that refers to a mapping, or something to replace (including nothing).
	const (
		other = iota
		mapping
		aliasToMapping
	)
	kindAt := func(toks []string) int {
		if doc < 0 || doc >= len(s.tree.Docs) {
			return other
		}
		c := s.ctx(doc)
		loc, err := c.Resolve(toks, false)
		if err != nil || loc.Node == 0 {
			return other
		}
		switch s.tree.Nodes[loc.Node].Kind {
		case cst.Mapping:
			return mapping
		case cst.Alias:
			if s.tree.Nodes[c.Follow(loc.Node)].Kind == cst.Mapping {
				return aliasToMapping
			}
		}
		return other
	}
	switch kindAt(nil) {
	case other:
		return set(nil, withoutNulls(*v))
	case aliasToMapping:
		if len(v.Items) == 0 {
			return nil
		}
		return fail(nil, fmt.Errorf("%w: the target is an alias", ErrAliasWrite))
	}
	stack := []mergeFrame{{items: v.Items}}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.i >= len(f.items) {
			// Deletions come last: members are distinct, so the order does not change
			// the result, and a mapping that loses its last member to a deletion
			// would turn into "{}" before the new members are added to it.
			var gone []string
			for j := 0; j < len(f.items); j += 2 {
				if f.items[j+1].Kind == edit.Null {
					gone = append(gone, f.items[j].Text)
				}
			}
			if len(gone) >= batchMin {
				if err := work.check(pointer.Join(f.toks)); err != nil {
					return fail(f.toks, err)
				}
				if s.removeKeys(doc, f.toks, gone, false) {
					stack = stack[:len(stack)-1]
					continue
				}
			}
			for j := 0; j < len(f.items); j += 2 {
				if f.items[j+1].Kind != edit.Null {
					continue
				}
				ptr := pointer.Join(append(append(make([]string, 0, len(f.toks)+1), f.toks...), f.items[j].Text))
				if err := work.check(ptr); err != nil {
					return fail(append(append([]string(nil), f.toks...), f.items[j].Text), err)
				}
				// Deleting a member that is not there is not an error.
				if err := s.edit(doc, opRemove, ptr, nil); err != nil && !errors.Is(err, ErrNotFound) {
					return unwrapMerge(ptr, err)
				}
			}
			stack = stack[:len(stack)-1]
			continue
		}
		key, val := f.items[f.i].Text, f.items[f.i+1]
		f.i += 2
		toks := append(append(make([]string, 0, len(f.toks)+1), f.toks...), key)
		switch {
		case val.Kind == edit.Null:
			// Deleted when the frame ends.
		case val.Kind == edit.Map:
			switch kindAt(toks) {
			case mapping:
				stack = append(stack, mergeFrame{toks: toks, items: val.Items})
			case aliasToMapping:
				if len(val.Items) > 0 {
					return fail(toks, fmt.Errorf("%w: the target is an alias", ErrAliasWrite))
				}
			default:
				if err := set(toks, withoutNulls(val)); err != nil {
					return err
				}
			}
		default:
			if err := set(toks, val); err != nil {
				return err
			}
		}
	}
	return nil
}

func unwrapMerge(ptr string, err error) error {
	var ee *EditError
	if errors.As(err, &ee) {
		return &EditError{Op: "merge-patch", Pointer: ptr, Kind: ee.Kind}
	}
	return &EditError{Op: "merge-patch", Pointer: ptr, Kind: err}
}

// withoutNulls returns v with every null member of every object removed, as
// RFC 7396 merges a patch into a target that is not an object. Nesting is bounded
// by the patch parser.
func withoutNulls(v edit.Value) edit.Value {
	if v.Kind != edit.Map {
		return v
	}
	out := edit.Value{Kind: edit.Map}
	for i := 0; i < len(v.Items); i += 2 {
		if v.Items[i+1].Kind == edit.Null {
			continue
		}
		out.Items = append(out.Items, v.Items[i], withoutNulls(v.Items[i+1]))
	}
	return out
}
