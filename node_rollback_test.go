package oneyaml

import (
	"reflect"
	"testing"
)

const rollbackDoc = "a: 1\nb: [x, y]\nc: &r {k: 1}\nd: *r\n"

// Patches that change the text before they fail.
var rollbackPatches = map[string]string{
	"replace then missing": `[{"op":"replace","path":"/a","value":5},{"op":"remove","path":"/nope"}]`,
	"grow then test":       `[{"op":"add","path":"/e","value":{"p":[1,2,3]}},{"op":"test","path":"/a","value":99}]`,
	"move then missing":    `[{"op":"move","from":"/b","path":"/z"},{"op":"remove","path":"/nope"}]`,
	"move into then test":  `[{"op":"move","from":"/a","path":"/b/0"},{"op":"test","path":"/a","value":1}]`,
	"remove then alias":    `[{"op":"remove","path":"/a"},{"op":"replace","path":"/d/k","value":2}]`,
}

func TestFailedPatchKeepsNodesValid(t *testing.T) {
	for name, p := range rollbackPatches {
		t.Run(name, func(t *testing.T) {
			doc, err := Parse([]byte(rollbackDoc))
			if err != nil {
				t.Fatal(err)
			}
			before := string(doc.Bytes())
			treeBefore := copyNodes(doc)
			var ptrs, vals []string
			for k, v := range doc.Root().Entries() {
				pt, err := v.Pointer()
				if err != nil {
					t.Fatal(err)
				}
				ptrs = append(ptrs, pt)
				vals = append(vals, k.Value()+"="+v.Source())
			}
			root := doc.Root()
			visited := 0
			for k, v := range root.Entries() {
				visited++
				if err := doc.ApplyPatch([]byte(p)); err == nil {
					t.Fatal("patch should fail")
				}
				if got := string(doc.Bytes()); got != before {
					t.Fatalf("document changed:\n%s", got)
				}
				if k.Stale() || v.Stale() {
					t.Fatal("nodes stale after a failed patch")
				}
				if got := k.Value() + "=" + v.Source(); got != vals[visited-1] {
					t.Errorf("entry %d reads %q, want %q", visited, got, vals[visited-1])
				}
				if pt, err := v.Pointer(); err != nil || pt != ptrs[visited-1] {
					t.Errorf("entry %d pointer %q, %v; want %q", visited, pt, err, ptrs[visited-1])
				}
			}
			if visited != 4 {
				t.Errorf("loop visited %d entries, want 4", visited)
			}
			if !reflect.DeepEqual(treeBefore, copyNodes(doc)) {
				t.Error("tree differs from the one before the failed patch")
			}
			// A second failure, and then a real edit: nodes read before go stale.
			_ = doc.ApplyPatch([]byte(p))
			if root.Stale() {
				t.Error("stale after two failed patches")
			}
			if err := doc.Set("/a", 7); err != nil {
				t.Fatal(err)
			}
			if !root.Stale() {
				t.Error("a successful edit must make earlier nodes stale")
			}
		})
	}
}

func TestFailedMergePatchKeepsNodesValid(t *testing.T) {
	doc, _ := Parse([]byte(rollbackDoc))
	before := string(doc.Bytes())
	root := doc.Root()
	err := doc.ApplyMergePatch([]byte(`{"a":2,"d":{"k":3}}`))
	if err == nil {
		t.Fatal("merge patch through an alias should fail")
	}
	if string(doc.Bytes()) != before {
		t.Fatal("document changed")
	}
	if root.Stale() || root.Len() != 4 {
		t.Error("nodes stale after a failed merge patch")
	}
}

func TestSuccessfulPatchStalesNodes(t *testing.T) {
	doc, _ := Parse([]byte(rollbackDoc))
	root := doc.Root()
	if err := doc.ApplyPatch([]byte(`[{"op":"replace","path":"/a","value":5}]`)); err != nil {
		t.Fatal(err)
	}
	if !root.Stale() {
		t.Error("nodes not stale after a successful patch")
	}
	// A failed patch afterwards does not bring them back.
	_ = doc.ApplyPatch([]byte(`[{"op":"replace","path":"/a","value":6},{"op":"remove","path":"/nope"}]`))
	if !root.Stale() {
		t.Error("stale nodes became valid after a later failed patch")
	}
}

type nodeCopy struct {
	Kind         uint8
	Start, End   int32
	Parent, Next int32
}

func copyNodes(d *Document) []nodeCopy {
	d.s.tree.Settle()
	var out []nodeCopy
	for _, n := range d.s.tree.Nodes {
		out = append(out, nodeCopy{uint8(n.Kind), n.Start, n.End, n.Parent, n.Next})
	}
	return out
}
