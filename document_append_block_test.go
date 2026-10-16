package oneyaml

import (
	"reflect"
	"testing"
)

// Appending after a block scalar that ends the input without a line break adds
// that break to the scalar unless its header is changed. A line that starts with
// a tab is content, not a blank line, so it must count as the last content line.
func TestAppendAfterBlockScalarEndingInTab(t *testing.T) {
	for _, in := range []string{
		"? |\n \t",
		"? |\n \tx",
		"? >\n \t",
		"? |\r\n \t",
		"? |+\n \t",
		"a: |\n  x\n  \t",
		"a: >\n  x\n  \t",
		"a: |\r\n  x\r\n  \t",
		"a: |+\n  x\n  \t",
		"a: |2\n  \tx",
		"- |\n  x\n  \t",
		"a: |\n  x\n  \t ",
	} {
		d, err := Parse([]byte(in))
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		var before any
		if err := d.Get("", &before); err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		patch := `[{"op":"add","path":"/new2","value":[null]}]`
		if _, ok := before.([]any); ok {
			patch = `[{"op":"add","path":"/-","value":[null]}]`
		}
		if err := d.ApplyPatch([]byte(patch)); err != nil {
			continue // refusing an edit is allowed; changing a value is not
		}
		var after any
		if err := d.Get("", &after); err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		switch b := before.(type) {
		case map[string]any:
			a := after.(map[string]any)
			for k, v := range b {
				if got, ok := a[k]; !ok || !reflect.DeepEqual(got, v) {
					t.Errorf("%q: entry %q was %q, now %q (output %q)", in, k, v, got, d.Bytes())
				}
			}
		case []any:
			a := after.([]any)
			if !reflect.DeepEqual(a[:len(b)], b) {
				t.Errorf("%q: items were %q, now %q (output %q)", in, b, a, d.Bytes())
			}
		}
	}
}
