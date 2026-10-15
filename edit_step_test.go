package oneyaml

import "testing"

// A new nested collection copies the step of the mapping it is added to when
// that mapping is itself nested under a key; only a mapping with no step of its
// own (the root) falls back to the step used elsewhere in the document.
func TestAddSequenceUsesEnclosingMappingStep(t *testing.T) {
	cases := []struct{ name, in, ptr, want string }{
		{"enclosing step wins", "x:\n    y: 1\nz:\n  w: 1\n", "/z/n",
			"x:\n    y: 1\nz:\n  w: 1\n  n:\n    - 1\n"},
		{"root uses the document step", "x:\n    y: 1\n", "/n",
			"x:\n    y: 1\nn:\n    - 1\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := Parse([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if err := d.ApplyPatch([]byte(`[{"op":"add","path":"` + c.ptr + `","value":[1]}]`)); err != nil {
				t.Fatal(err)
			}
			if got := string(d.Bytes()); got != c.want {
				t.Errorf("got %q\nwant %q", got, c.want)
			}
		})
	}
}

// An explicit key in a block sequence item whose content starts on the line
// after the question mark: a key added to the item's mapping goes at the
// column of the question mark.
func TestAddAfterExplicitKeyInSequenceItem(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"dash then question mark", "- ?\n    a\n  :\n  b: 1\n", "- ?\n    a\n  :\n  b: 1\n  new0: null\n"},
		{"nested under a key", "x:\n  - ?\n      a\n    : 2\n", "x:\n  - ?\n      a\n    : 2\n    new0: null\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := Parse([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			ptr := "/0/new0"
			if c.name != "dash then question mark" {
				ptr = "/x/0/new0"
			}
			if err := d.Set(ptr, nil); err != nil {
				t.Fatalf("Set: %v", err)
			}
			if got := string(d.Bytes()); got != c.want {
				t.Errorf("got %q\nwant %q", got, c.want)
			}
		})
	}
}
