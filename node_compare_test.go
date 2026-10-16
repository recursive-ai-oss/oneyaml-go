package oneyaml_test

import (
	"testing"

	oneyaml "oneyaml.dev/go"
)

func TestNodeEquality(t *testing.T) {
	const src = "a: 1\nb:\n  c: x\nl: [1, 2]\n"
	read := func(d *oneyaml.Document) oneyaml.Node {
		n, ok := d.Root().Lookup("a")
		if !ok {
			t.Fatal("no a")
		}
		return n
	}
	doc, err := oneyaml.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	n := read(doc)
	if n != read(doc) {
		t.Fatal("the same node read twice without an edit differs")
	}
	seen := map[oneyaml.Node]string{n: "a"} // usable as a key
	if seen[read(doc)] != "a" {
		t.Fatal("a Node does not work as a map key")
	}
	other, _ := oneyaml.Parse([]byte(src))
	if n == read(other) {
		t.Fatal("nodes of two documents are equal")
	}

	// An edit that is refused leaves the nodes equal.
	if err := doc.Remove("/nope"); err == nil {
		t.Fatal("remove of a missing key succeeded")
	}
	if err := doc.ApplyPatch([]byte(`[{"op":"replace","path":"/a","value":5},{"op":"remove","path":"/nope"}]`)); err == nil {
		t.Fatal("failing patch succeeded")
	}
	if n != read(doc) {
		t.Fatal("a refused edit or rolled-back patch changed node identity")
	}

	edits := map[string]func(d *oneyaml.Document) error{
		"set same place":  func(d *oneyaml.Document) error { return d.Set("/a", 2) },
		"set new key":     func(d *oneyaml.Document) error { return d.Set("/z", 2) },
		"remove":          func(d *oneyaml.Document) error { return d.Remove("/b/c") },
		"rename":          func(d *oneyaml.Document) error { return d.Rename("/b", "bb") },
		"head comment":    func(d *oneyaml.Document) error { return d.SetHeadComment("/b", "hi") },
		"line comment":    func(d *oneyaml.Document) error { return d.SetLineComment("/l", "hi") },
		"patch":           func(d *oneyaml.Document) error { return d.ApplyPatch([]byte(`[{"op":"add","path":"/q","value":1}]`)) },
		"merge patch":     func(d *oneyaml.Document) error { return d.ApplyMergePatch([]byte(`{"a":3}`)) },
		"sequence append": func(d *oneyaml.Document) error { return d.Set("/l/-", 3) },
	}
	for name, f := range edits {
		d, _ := oneyaml.Parse([]byte(src))
		before := read(d)
		if err := f(d); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := d.Root().Lookup("a"); ok && before == read(d) {
			t.Errorf("%s: a Node read before the edit equals the one read after", name)
		}
		if !before.Stale() {
			t.Errorf("%s: the old Node is not stale", name)
		}
	}
}
