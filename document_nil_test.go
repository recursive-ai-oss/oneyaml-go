package oneyaml

import (
	"errors"
	"io"
	"reflect"
	"testing"
)

// A failed Parse returns a nil *Document. Calling its methods must not panic.
func TestNilDocument(t *testing.T) {
	d, err := Parse([]byte("a: ["))
	if err == nil || d != nil {
		t.Fatalf("Parse of bad input returned %v, %v", d, err)
	}
	var x any
	for name, err := range map[string]error{
		"Set":             d.Set("/a", 1),
		"Remove":          d.Remove("/a"),
		"Get":             d.Get("/a", &x),
		"Decode":          d.Decode(&x),
		"ApplyPatch":      d.ApplyPatch([]byte(`[]`)),
		"Apply":           d.Apply(PatchOp{Op: "remove", Path: "/a"}),
		"ApplyMergePatch": d.ApplyMergePatch([]byte(`{}`)),
	} {
		if !errors.Is(err, ErrNilDocument) {
			t.Errorf("%s: error %v, want ErrNilDocument", name, err)
		}
	}
	if n, err := d.WriteTo(io.Discard); n != 0 || !errors.Is(err, ErrNilDocument) {
		t.Errorf("WriteTo: %d, %v", n, err)
	}
	if d.Len() != 0 || d.Bytes() != nil || d.Doc(0) != nil || d.Root().IsValid() {
		t.Error("getters of a nil Document return non-zero values")
	}
	if d.ExplicitStart() || d.ExplicitEnd() || d.TagDirectives() != nil {
		t.Error("info getters of a nil Document return non-zero values")
	}
	if _, _, ok := d.Version(); ok {
		t.Error("Version reports a directive on a nil Document")
	}
	// The zero Document has no stream either.
	var z Document
	if err := z.Set("/a", 1); !errors.Is(err, ErrNilDocument) || z.Len() != 0 {
		t.Errorf("zero Document: %v", err)
	}
}

// The zero Node, which a nil Document's Root returns, answers every method with a
// zero value or an error.
func TestZeroNode(t *testing.T) {
	var n Node
	v := reflect.ValueOf(n)
	for i := 0; i < v.NumMethod(); i++ {
		m := v.Method(i)
		var args []reflect.Value
		for j := 0; j < m.Type().NumIn(); j++ {
			args = append(args, reflect.Zero(m.Type().In(j)))
		}
		name := v.Type().Method(i).Name
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Node.%s panics on the zero Node: %v", name, r)
				}
			}()
			if name == "Decode" {
				var x any
				if err := n.Decode(&x); err == nil {
					t.Error("Decode of the zero Node returned no error")
				}
				return
			}
			m.Call(args)
		}()
	}
	for range n.Items() {
		t.Error("Items of the zero Node yields")
	}
	for range n.Entries() {
		t.Error("Entries of the zero Node yields")
	}
}
