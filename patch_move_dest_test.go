package oneyaml

import "testing"

// A move removes its source first, so a destination that named a node of the
// old tree may name nothing in the new one. Alias handling must not read the old
// node number in the new tree.
func TestMoveWhoseDestinationDisappearsWithTheSource(t *testing.T) {
	d, err := Parse([]byte("- &a {x: 1}\n- *a\n- x: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	before := string(d.Bytes())
	err = d.ApplyPatch([]byte(`[{"op":"move","from":"/1","path":"/2/x"}]`))
	if err == nil {
		t.Fatalf("move to a path that is gone succeeded:\n%s", d.Bytes())
	}
	if got := string(d.Bytes()); got != before {
		t.Errorf("failed patch changed the document:\n%s", got)
	}
}
