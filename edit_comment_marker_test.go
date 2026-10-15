package oneyaml

import "testing"

// A comment right below an entry and indented more than its key goes with the
// entry. Left behind after a block scalar it would turn into scalar text, so the
// remove used to be refused.
func TestRemoveTakesDeeperCommentBelowEntry(t *testing.T) {
	for _, c := range []struct{ name, in, ptr, want string }{
		{"after literal", "a:\n  k: |\n    v\n  x: 1\n    # c\nb: 2\n", "/a/x", "a:\n  k: |\n    v\nb: 2\n"},
		{"after plain", "a:\n  k: 5\n  x: 1\n    # c\n    # d\nb: 2\n", "/a/x", "a:\n  k: 5\nb: 2\n"},
		{"same indent stays", "a:\n  k: 5\n  x: 1\n  # c\nb: 2\n", "/a/x", "a:\n  k: 5\n  # c\nb: 2\n"},
		{"blank line stops", "a:\n  k: 5\n  x: 1\n\n    # c\nb: 2\n", "/a/x", "a:\n  k: 5\n\n    # c\nb: 2\n"},
		{"sequence item", "- 1\n- 2\n  # c\n- 3\n", "/1", "- 1\n- 3\n"},
	} {
		d, err := Parse([]byte(c.in))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Remove(c.ptr); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := string(d.Bytes()); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// The comment on the start marker line stays there when the empty document gets
// a block collection.
func TestReplaceEmptyDocumentKeepsMarkerComment(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"--- # c\n", "--- # c\na: 1\n"},
		{"--- # c", "--- # c\na: 1"},
		{"---   # c\n", "---   # c\na: 1\n"},
		{"---\n", "---\na: 1\n"},
	} {
		d, err := Parse([]byte(c.in))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Set("", map[string]any{"a": 1}); err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got := string(d.Bytes()); got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}
