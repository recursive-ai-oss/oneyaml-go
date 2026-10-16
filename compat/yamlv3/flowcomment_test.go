package yaml

import "testing"

// The expected text is what yaml.v3 writes for the same node (the module's
// differential tests compare the two on generated inputs).
func TestNodeFlowComments(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"[a,\n# c\nb]\n", "[a,\n    # c\n    b]\n"},
		{"[a, # l\nb]\n", "[a, # l\n    b]\n"},
		{"[a,\nb # l\n]\n", "[a, b, # l\n]\n"},
		{"{a: 1,\n# h\nb: 2}\n", "{a: 1,\n    # h\n    b: 2}\n"},
		{"k: [a,\n  # h\n  [b,\n  # h2\n  c]]\n", "k: [a,\n    # h\n    [b,\n        # h2\n        c]]\n"},
		{"# top\n[a, # l\n b]\n# foot\n", "# top\n[a, # l\n    b]\n# foot\n"},
	} {
		var n Node
		if err := Unmarshal([]byte(c.in), &n); err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		out, err := Marshal(&n)
		if err != nil || string(out) != c.want {
			t.Errorf("%q: got %q, %v; want %q", c.in, out, err, c.want)
		}
	}
}
