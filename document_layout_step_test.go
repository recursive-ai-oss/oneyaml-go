package oneyaml

import "testing"

// A new block collection with no sibling of its kind to copy is indented by the
// step the document's nearest block collection uses, or two spaces when there is
// none; a sibling of the same kind is copied as before.
func TestNewBlockCollectionTakesDocumentStep(t *testing.T) {
	for _, c := range []struct{ name, src, patch, want string }{
		{"sequence in a four-space mapping document", "x:\n    y: 1\n", `[{"op":"add","path":"/n","value":[1]}]`,
			"x:\n    y: 1\nn:\n    - 1\n"},
		{"mapping in a document of four-space sequences", "s:\n    - a\n", `[{"op":"add","path":"/m","value":{"k":1}}]`,
			"s:\n    - a\nm:\n    k: 1\n"},
		{"nested collections", "x:\n    y: 1\n", `[{"op":"add","path":"/n","value":{"s":[1]}}]`,
			"x:\n    y: 1\nn:\n    s:\n        - 1\n"},
		{"no nested collection", "a: 1\n", `[{"op":"add","path":"/n","value":[1]}]`,
			"a: 1\nn:\n  - 1\n"},
		{"sibling sequence is copied", "k:\n- a\nx:\n    y: 1\n", `[{"op":"add","path":"/n","value":[1]}]`,
			"k:\n- a\nx:\n    y: 1\nn:\n- 1\n"},
		{"entry whose key starts on a later line", "x:\n  ?\n    a\n  :\n", `[{"op":"add","path":"/n","value":[1]}]`,
			"x:\n  ?\n    a\n  :\nn:\n  - 1\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := mustParse(t, c.src)
			if err := d.ApplyPatch([]byte(c.patch)); err != nil {
				t.Fatal(err)
			}
			if got := string(d.Bytes()); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
			if err := sameAsFreshParse(d); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Set adds a new member the same way patch does.
func TestSetNewSequenceTakesDocumentStep(t *testing.T) {
	d := mustParse(t, "x:\n    y: 1\n")
	if err := d.Set("/n", []int{1}); err != nil {
		t.Fatal(err)
	}
	if got, want := string(d.Bytes()), "x:\n    y: 1\nn:\n    - 1\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
