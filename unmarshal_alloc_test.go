package oneyaml_test

import (
	"testing"

	oneyaml "oneyaml.dev/go"
)

type nodeReader struct{ kind oneyaml.NodeKind }

func (r *nodeReader) UnmarshalYAML(n oneyaml.Node) error { r.kind = n.Kind(); return nil }

// A plain load pays nothing for the Unmarshaler seam when the types do not use it.
func TestPlainLoadAllocationsUnchanged(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation counts")
	}
	src := []byte("name: api\nports: [80, 443]\nlabels: {a: b, c: d}\nsub:\n- a: x\n  b: 1\n- a: y\n")
	type target struct {
		Name  string            `yaml:"name"`
		Ports []int             `yaml:"ports"`
		Lbl   map[string]string `yaml:"labels"`
		Sub   []struct {
			A string
			B *int
		}
	}
	var s target
	var a any
	oneyaml.Unmarshal(src, &s)
	oneyaml.Unmarshal(src, &a)
	// The counts are those of the decoder before the seam existed.
	if n := testing.AllocsPerRun(100, func() { var s target; oneyaml.Unmarshal(src, &s) }); n > 40 {
		t.Errorf("typed Unmarshal allocates %v times, was 40", n)
	}
	if n := testing.AllocsPerRun(100, func() { var a any; oneyaml.Unmarshal(src, &a) }); n > 21 {
		t.Errorf("generic Unmarshal allocates %v times, was 21", n)
	}
	// A type with the hook does pay: it gets a tree for its subtree.
	var h struct{ V nodeReader }
	hsrc := []byte("v: {a: 1}\n")
	if n := testing.AllocsPerRun(100, func() { oneyaml.Unmarshal(hsrc, &h) }); n <= 5 {
		t.Errorf("hook type allocates only %v times; the node should cost something", n)
	}
}
