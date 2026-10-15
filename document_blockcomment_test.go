package oneyaml

import (
	"fmt"
	"reflect"
	"testing"
)

// Lines inside a block scalar that start with "#" are content, not comments that
// belong to the entry after the scalar. Edits of that entry keep the content and
// are not refused.
func TestHashLinesInBlockScalarAreNotComments(t *testing.T) {
	for _, chomp := range []string{"", "-", "+"} {
		for _, tail := range []string{"", "\n"} {
			mapSrc := fmt.Sprintf("a: |%s\n  # c\n  ## d\n%sb: 1\nc: 2\n", chomp, tail)
			seqSrc := fmt.Sprintf("- |%s\n  # c\n  ## d\n%s- 1\n- 2\n", chomp, tail)
			var mapWant, seqWant any
			if err := Unmarshal([]byte(mapSrc), &mapWant); err != nil {
				t.Fatal(err)
			}
			if err := Unmarshal([]byte(seqSrc), &seqWant); err != nil {
				t.Fatal(err)
			}
			a := mapWant.(map[string]any)["a"]
			s0 := seqWant.([]any)[0]
			for _, tc := range []struct {
				name string
				src  string
				op   func(d *Document) error
				want any
			}{
				{"remove key", mapSrc, func(d *Document) error { return d.Remove("/b") }, map[string]any{"a": a, "c": 2}},
				{"remove last key", mapSrc, func(d *Document) error { return d.Remove("/c") }, map[string]any{"a": a, "b": 1}},
				{"replace key", mapSrc, func(d *Document) error { return d.Set("/b", []any{1, 2}) }, map[string]any{"a": a, "b": []any{1, 2}, "c": 2}},
				{"remove item", seqSrc, func(d *Document) error { return d.Remove("/1") }, []any{s0, 2}},
				{"insert item", seqSrc, func(d *Document) error {
					return d.ApplyPatch([]byte(`[{"op":"add","path":"/1","value":"new"}]`))
				}, []any{s0, "new", 1, 2}},
				{"replace item", seqSrc, func(d *Document) error { return d.Set("/1", "x") }, []any{s0, "x", 2}},
			} {
				for _, off := range []bool{false, true} {
					regionOff = off
					d := mustParse(t, tc.src)
					err := tc.op(d)
					regionOff = false
					if err != nil {
						t.Errorf("|%s tail %q, %s (regions off %v): %v", chomp, tail, tc.name, off, err)
						continue
					}
					var got any
					if err := Unmarshal(d.Bytes(), &got); err != nil || !reflect.DeepEqual(got, tc.want) {
						t.Errorf("|%s tail %q, %s (regions off %v): reads back %v (%v), want %v\n%q", chomp, tail, tc.name, off, got, err, tc.want, d.Bytes())
					}
					if err := sameAsFreshParse(d); err != nil {
						t.Errorf("%s: %v", tc.name, err)
					}
				}
			}
		}
	}
}
