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

// A tag on the marker line does not hide the comment after it: the comment stays
// on the marker line when a merge patch gives the empty document a mapping.
func TestMergeIntoTaggedMarkerKeepsComment(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"--- !!null # c\n", "--- # c\na: 1\n"},
		{"--- !!null # c", "--- # c\na: 1"},
		{"--- &x !!null # c\n", "--- &x # c\na: 1\n"},
		{"--- !!null\n", "---\na: 1\n"},
	} {
		d, err := Parse([]byte(c.in))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.ApplyMergePatch([]byte(`{"a":1}`)); err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got := string(d.Bytes()); got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}

// A comment after a dash with no value stays on the dash's line when the item
// becomes a block collection; the collection goes on the lines below.
func TestReplaceEmptyItemKeepsItsComment(t *testing.T) {
	for _, c := range []struct{ in, ptr, want string }{
		{"- # c\n- x\n", "/0", "- # c\n  k:\n    - 1\n    - 2\n- x\n"},
		{"- # c", "/0", "- # c\n  k:\n    - 1\n    - 2"},
		{"- # c\r\n- x\r\n", "/0", "- # c\r\n  k:\r\n    - 1\r\n    - 2\r\n- x\r\n"},
		{"a:\n  - # c\n  - x\n", "/a/0", "a:\n  - # c\n    k:\n      - 1\n      - 2\n  - x\n"},
		{"-\n- x\n", "/0", "- k:\n    - 1\n    - 2\n- x\n"},
	} {
		d, err := Parse([]byte(c.in))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Set(c.ptr, map[string]any{"k": []any{1, 2}}); err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got := string(d.Bytes()); got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}

// A line of spaces below a removed entry, indented deeper than the literal scalar
// before it, would become text of that scalar; it goes with the entry. A shorter
// line or an empty one stays.
func TestRemoveTakesDeepBlankLineAfterLiteral(t *testing.T) {
	for _, c := range []struct{ name, in, ptr, want string }{
		{"last entry", "top:\n   key1: |\n     t\n   key2: v\n      \nafter: 1\n", "/top/key2", "top:\n   key1: |\n     t\nafter: 1\n"},
		{"middle entry", "top:\n   key1: |\n     t\n   key2: v\n      \n   key3: 1\n", "/top/key2", "top:\n   key1: |\n     t\n   key3: 1\n"},
		{"shorter line stays", "top:\n   key1: |\n     t\n   key2: v\n    \nafter: 1\n", "/top/key2", "top:\n   key1: |\n     t\n    \nafter: 1\n"},
		{"empty line stays", "top:\n   key1: |\n     t\n   key2: v\n\nafter: 1\n", "/top/key2", "top:\n   key1: |\n     t\n\nafter: 1\n"},
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
