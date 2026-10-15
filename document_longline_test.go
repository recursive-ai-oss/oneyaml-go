package oneyaml

import (
	"strings"
	"testing"
)

// A value is written in block style, with the document's own line breaks, also
// when the line it replaces is longer than the window in which the editor looks
// for the break that ends it.
func TestLongLineValueKeepsBlockStyle(t *testing.T) {
	long := strings.Repeat("x", 2000)
	for _, nl := range []string{"\n", "\r\n"} {
		for name, tc := range map[string]struct {
			val  any
			want string
		}{
			"mapping":  {map[string]any{"x": 1, "y": 2}, "a:" + nl + "  x: 1" + nl + "  y: 2" + nl + "b: 1" + nl},
			"sequence": {[]any{1, 2}, "a:" + nl + "  - 1" + nl + "  - 2" + nl + "b: 1" + nl},
		} {
			d := mustParse(t, "a: "+long+nl+"b: 1"+nl)
			if err := d.Set("/a", tc.val); err != nil {
				t.Fatalf("%s %q: %v", name, nl, err)
			}
			if got := string(d.Bytes()); got != tc.want {
				t.Errorf("%s %q: got %q, want %q", name, nl, got, tc.want)
			}
		}
	}
}
