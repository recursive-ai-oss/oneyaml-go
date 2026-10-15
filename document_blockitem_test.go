package oneyaml

import "testing"

// A block scalar item replaced by a collection is rewritten as a block: its text
// ends with the line break that closes its last line, so the replacement keeps
// that break and the nested strings take the old style and the siblings' layout.
func TestBlockScalarItemReplacedByCollection(t *testing.T) {
	for _, tc := range []struct {
		name, src, ptr string
		val            any
		want           string
	}{
		{"keep literal, sequence", "- a\n- |+\n  b\n\n- - |+\n    tail\n\n", "/1", []any{"tail\n\n"}, "- a\n- - |+\n    tail\n\n- - |+\n    tail\n\n"},
		{"keep literal, last item", "- a\n- |+\n  b\n\n", "/1", []any{"tail\n\n"}, "- a\n- - |+\n    tail\n\n"},
		{"keep literal, next item", "- a\n- |+\n  b\n\n- z\n", "/1", []any{"tail\n\n"}, "- a\n- - |+\n    tail\n\n- z\n"},
		{"clip literal, mapping", "- a\n- |\n  b\n- z\n", "/1", map[string]any{"k": 1}, "- a\n- k: 1\n- z\n"},
		{"folded, sequence", "- a\n- >\n  b\n- z\n", "/1", []any{1, 2}, "- a\n- - 1\n  - 2\n- z\n"},
		{"strip literal, last item without break", "- a\n- |-\n  b", "/1", []any{"x"}, "- a\n- - x"},
	} {
		for _, off := range []bool{false, true} {
			regionOff = off
			d := mustParse(t, tc.src)
			err := d.Set(tc.ptr, tc.val)
			regionOff = false
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
				continue
			}
			if got := string(d.Bytes()); got != tc.want {
				t.Errorf("%s (regions off %v): got %q, want %q", tc.name, off, got, tc.want)
			}
			if err := sameAsFreshParse(d); err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
		}
	}
}
