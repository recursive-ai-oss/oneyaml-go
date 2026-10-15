package oneyaml

import "testing"

// An explicit key whose content sits on the line after "?" starts its entry at
// the "?". A key added to the mapping must use that column, not the column of
// the key content, or the new line does not parse.
func TestAddAfterExplicitKeyOnNextLine(t *testing.T) {
	cases := []struct{ name, in, ptr, want string }{
		{"plain", "x:\n  ?\n    a\n  :\n  b: 1\n", "/x/new0",
			"x:\n  ?\n    a\n  :\n  b: 1\n  new0: null\n"},
		{"comment between", "x:\n  ? # why\n    # more\n    a\n  : 2\n", "/x/new0",
			"x:\n  ? # why\n    # more\n    a\n  : 2\n  new0: null\n"},
		{"only entry", "x:\n  ?\n    a\n  : 1\n", "/x/new0",
			"x:\n  ?\n    a\n  : 1\n  new0: null\n"},
		{"earlier value ends in a question mark", "x:\n  b: what?\n  c: 1\n", "/x/new0",
			"x:\n  b: what?\n  c: 1\n  new0: null\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := Parse([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if err := d.Set(c.ptr, nil); err != nil {
				t.Fatalf("Set: %v", err)
			}
			if got := string(d.Bytes()); got != c.want {
				t.Errorf("got %q\nwant %q", got, c.want)
			}
		})
	}
}
