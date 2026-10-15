package oneyaml

import (
	"fmt"
	"reflect"
	"strconv"

	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/pointer"
	"oneyaml.dev/go/internal/resolve"
)

// The read-back oracle. After an edit the output is parsed again from its bytes
// and compared, as a whole value, with a model: the value tree of the document
// before the edit with the same operation applied to it by plain tree surgery.
// Every document of the stream is compared, aliases stay aliases, and nothing is
// projected away except two named things: number spelling (0x10 and 16 are the same
// integer) and explicit non-core tags, which the edit rules treat separately.

// streamValues returns the value tree of every document of d.
func streamValues(d *Document) ([]edit.Value, error) {
	var out []edit.Value
	for i := 0; i < d.Len(); i++ {
		v, err := edit.FromTree(&d.s.tree, d.s.src, d.s.tree.Docs[i].Root)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// modelApply applies an operation to a value tree.
func modelApply(root edit.Value, op editKindOp, ptr string, v *edit.Value) (edit.Value, error) {
	toks, err := pointer.Parse(ptr)
	if err != nil {
		return root, err
	}
	if len(toks) == 0 {
		if op == opRemove {
			return root, fmt.Errorf("cannot remove the root")
		}
		return *v, nil
	}
	cur := &root
	for _, tok := range toks[:len(toks)-1] {
		next, err := modelStep(cur, tok)
		if err != nil {
			return root, err
		}
		cur = next
	}
	last := toks[len(toks)-1]
	switch cur.Kind {
	case edit.Map:
		for i := 0; i < len(cur.Items); i += 2 {
			if cur.Items[i].Text == last && isScalarKey(cur.Items[i]) {
				if op == opRemove {
					cur.Items = append(cur.Items[:i:i], cur.Items[i+2:]...)
				} else {
					cur.Items[i+1] = *v
				}
				return root, nil
			}
		}
		if op == opRemove || op == opReplace {
			return root, fmt.Errorf("no key %q", last)
		}
		cur.Items = append(cur.Items, edit.Str(last), *v)
	case edit.Seq:
		n := len(cur.Items)
		idx := n
		if last != "-" {
			i, ok := pointer.Index(last)
			if !ok {
				return root, fmt.Errorf("bad index")
			}
			idx = i
		} else if op == opRemove || op == opReplace {
			return root, fmt.Errorf("bad index")
		}
		switch op {
		case opRemove:
			if idx >= n {
				return root, fmt.Errorf("index out of range")
			}
			cur.Items = append(cur.Items[:idx:idx], cur.Items[idx+1:]...)
		case opReplace:
			if idx >= n {
				return root, fmt.Errorf("index out of range")
			}
			cur.Items[idx] = *v
		case opSet:
			if idx > n {
				return root, fmt.Errorf("index out of range")
			}
			if idx < n {
				cur.Items[idx] = *v
			} else {
				cur.Items = append(cur.Items, *v)
			}
		case opAdd:
			if idx > n {
				return root, fmt.Errorf("index out of range")
			}
			items := append([]edit.Value(nil), cur.Items[:idx]...)
			items = append(items, *v)
			cur.Items = append(items, cur.Items[idx:]...)
		}
	default:
		return root, fmt.Errorf("below a scalar")
	}
	return root, nil
}

func modelStep(cur *edit.Value, tok string) (*edit.Value, error) {
	switch cur.Kind {
	case edit.Map:
		for i := 0; i < len(cur.Items); i += 2 {
			if k := cur.Items[i]; k.Text == tok && isScalarKey(k) {
				return &cur.Items[i+1], nil
			}
		}
	case edit.Seq:
		if i, ok := pointer.Index(tok); ok && i < len(cur.Items) {
			return &cur.Items[i], nil
		}
	}
	return nil, fmt.Errorf("no step %q", tok)
}

// canon normalises the two things the oracle does not compare: number spelling
// and explicit tags.
func canon(v edit.Value) edit.Value {
	v.Tag = ""
	switch v.Kind {
	case edit.Null:
		v.Text = "null"
	case edit.Bool, edit.Int, edit.Float:
		r := resolve.Core([]byte(v.Text))
		switch r.Kind {
		case resolve.Bool:
			v.Text = strconv.FormatBool(r.B)
		case resolve.Int:
			v.Text = r.Big().String()
		case resolve.Float:
			v.Text = strconv.FormatFloat(r.F, 'g', -1, 64)
		}
	}
	if len(v.Items) > 0 {
		items := make([]edit.Value, len(v.Items))
		for i := range v.Items {
			items[i] = canon(v.Items[i])
		}
		v.Items = items
	} else {
		v.Items = nil
	}
	return v
}

func canonAll(vs []edit.Value) []edit.Value {
	out := make([]edit.Value, len(vs))
	for i := range vs {
		out[i] = canon(vs[i])
	}
	return out
}

// checkReadBack parses after again from its bytes and compares the whole stream
// with the model of before with the operation applied to document doc.
func checkReadBack(before []edit.Value, after *Document, doc int, op editKindOp, ptr string, v *edit.Value) error {
	reparsed, err := Parse(after.Bytes())
	if err != nil {
		return fmt.Errorf("output does not parse: %w", err)
	}
	got, err := streamValues(reparsed)
	if err != nil {
		return err
	}
	want := make([]edit.Value, len(before))
	copy(want, before)
	for i := range want {
		want[i] = cloneValue(want[i])
	}
	if doc >= len(want) {
		return fmt.Errorf("document %d missing", doc)
	}
	m, err := modelApply(want[doc], op, ptr, v)
	if err != nil {
		return fmt.Errorf("model: %w", err)
	}
	want[doc] = m
	if !reflect.DeepEqual(canonAll(got), canonAll(want)) {
		return fmt.Errorf("read-back differs\n got: %+v\nwant: %+v", canonAll(got), canonAll(want))
	}
	return nil
}

func cloneValue(v edit.Value) edit.Value {
	if len(v.Items) > 0 {
		items := make([]edit.Value, len(v.Items))
		for i := range v.Items {
			items[i] = cloneValue(v.Items[i])
		}
		v.Items = items
	}
	return v
}

// isScalarKey reports whether a pointer token can match key k: only scalar keys
// are addressable, not collections and not aliases.
func isScalarKey(k edit.Value) bool { return k.Kind <= edit.String }
