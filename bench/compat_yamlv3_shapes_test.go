package bench

import (
	"testing"

	yv3 "go.yaml.in/yaml/v3"
	compat "oneyaml.dev/go/compat/yamlv3"
)

// Comment shapes where the shim used to place or lay out a comment differently
// from yaml.v3. Each input is read with both libraries (the trees must be
// equal, comments included) and the tree yaml.v3 read is written with both
// (the bytes must be equal).
var commentShapeCases = []struct{ name, in string }{
	{"flow root with a foot", "[a, b]\n# foot\n"},
	{"flow root with a foot, two lines", "[a, b]\n# foot\n# two\n"},
	{"comment on the document start line", "--- # c\na: 1\n"},
	{"comment on the document start line, sequence", "--- # c\n- a\n"},
	{"comment on the document start line, head comment too", "# h\n--- # c\na: 1\n"},
	{"dash comment then blank line", "- # c\n\n- b\n"},
	{"dash comment then blank line, mapping", "k:\n  - # c\n\n  - b\n"},
	{"dash comment on a nested item", "- # c\n  a: 1\n"},
	{"document marker comment, blank line after", "--- # c\n\n"},
	{"document marker comment, blank lines after", "--- # c\n# d\n\n\n"},
	{"empty last item with a comment", "- # c\n"},
	{"empty last item with a comment and a blank line", "- # c\n\n"},
	{"empty last item, comments after", "- # c\n# d\n"},
	{"empty last item, blank line between comments", "- # c\n\n# d\n"},
	{"empty last item, comment line after", "- x\n-\n  # c\n"},
	{"empty last item, nested", "a:\n  - # c\n"},
	{"empty last item, next document", "- # c\n---\na: 1\n"},
	{"dash comment, blank line, comment, then a key", "- # c\n\n  # c\n  k0: v\n"},
	{"dash comment, blank line, comment, then an item", "- # c\n\n  # c\n  - v\n"},
	{"dash comment after an item, blank line, comment, then a key", "- x\n- # c\n\n  # d\n  k0: v\n"},
	{"folded scalar starting with a line break", ">\n\n a\n b\n\n c\n"},
	{"folded scalar starting with a line break, in a mapping", "k: >\n\n  a\n  b\n"},
}

func TestCompatV3CommentShapes(t *testing.T) {
	for _, tc := range commentShapeCases {
		t.Run(tc.name, func(t *testing.T) {
			var y yv3.Node
			var c compat.Node
			if err := yv3.Unmarshal([]byte(tc.in), &y); err != nil {
				t.Fatalf("yaml.v3: %v", err)
			}
			if err := compat.Unmarshal([]byte(tc.in), &c); err != nil {
				t.Fatalf("shim: %v", err)
			}
			if a, b := dumpV3(&y, nodeFields{comments: true}), dumpCompat(&c, nodeFields{comments: true}); a != b {
				t.Errorf("read differs: %s", firstLineDiff(a, b))
			}
			yb, yerr := yv3.Marshal(&y)
			cb, cerr := compat.Marshal(toCompat(&y, map[*yv3.Node]*compat.Node{}))
			if yerr != nil || cerr != nil {
				t.Fatalf("write: v3 err=%v shim err=%v", yerr, cerr)
			}
			if string(yb) != string(cb) && writeDifference(&y, yb, cb, yerr, cerr) == "" {
				t.Errorf("write differs\nyaml.v3:\n%q\nshim:\n%q", yb, cb)
			}
			// Read-then-write with the shim's own reader.
			cb2, err := compat.Marshal(&c)
			if err != nil {
				t.Fatal(err)
			}
			if string(yb) != string(cb2) && writeDifference(&y, yb, cb2, yerr, nil) == "" {
				t.Errorf("read-then-write differs\nyaml.v3:\n%q\nshim:\n%q", yb, cb2)
			}
		})
	}
}
