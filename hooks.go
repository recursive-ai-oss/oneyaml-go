package oneyaml

import (
	"reflect"

	"oneyaml.dev/go/internal/bind"
	"oneyaml.dev/go/internal/emit"
	"oneyaml.dev/go/internal/typeinfo"
)

// funcUnmarshaler is the UnmarshalYAML form of yaml.v2, yaml.v3 (before Node)
// and goccy: the method is given a function that decodes the value into any
// target. Code written for them keeps working.
type funcUnmarshaler interface {
	UnmarshalYAML(unmarshal func(any) error) error
}

func init() {
	bind.SetFuncHook(reflect.TypeFor[funcUnmarshaler](), func(inst any, ref bind.NodeRef) error {
		n := nodeFromRef(ref)
		return inst.(funcUnmarshaler).UnmarshalYAML(func(v any) error { return n.Decode(v) })
	})

	// A Node is a view of a document. It is never a decode target, and when it is
	// written its value is written: a struct that holds a Node round trips.
	typeinfo.SetNodeType(reflect.TypeFor[Node]())
	emit.SetNodeValue(func(v reflect.Value) (any, error) {
		var n Node
		switch x := v.Interface().(type) {
		case Node:
			n = x
		case *Node:
			n = *x
		}
		if n.Stale() {
			// Not the zero Node: the document was edited since it was read, and
			// writing null would change the data without a word.
			return nil, n.errInvalid("marshal")
		}
		if !n.IsValid() {
			return nil, nil
		}
		var out any
		if err := n.Decode(&out); err != nil {
			return nil, err
		}
		return out, nil
	})
}
