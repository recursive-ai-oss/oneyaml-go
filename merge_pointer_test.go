package oneyaml_test

import (
	"errors"
	"testing"

	oneyaml "oneyaml.dev/go"
)

// Two sources and an explicit key: x is in both sources (the first wins), y is
// in the mapping and in a source (the mapping wins), w only in the second source.
const mergePtrDoc = `a: &a {x: 1, y: 2, n: {k: a}}
b: &b {x: 9, w: 5, n: {k: b}}
use:
  <<: [*a, *b]
  y: 20
  z: 3
inline:
  <<: {p: 1, q: 2}
  q: 3
deep: &d
  <<: *b
  own: 1
chain:
  <<: *d
  top: 1
`

func mergePtrParse(t *testing.T, opts ...oneyaml.Option) *oneyaml.Document {
	t.Helper()
	doc, err := oneyaml.Parse([]byte(mergePtrDoc), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestGetMergedKeys(t *testing.T) {
	doc := mergePtrParse(t, oneyaml.MergeKeys())
	cases := []struct {
		ptr  string
		want any
	}{
		{"/use/x", 1},  // earlier source wins
		{"/use/y", 20}, // the mapping's own key wins
		{"/use/z", 3},
		{"/use/w", 5},
		{"/use/n/k", "a"}, // below a merged value
		{"/inline/p", 1},
		{"/inline/q", 3},
		{"/chain/w", 5}, // a source that merges another
		{"/chain/own", 1},
		{"/chain/x", 9},
	}
	for _, c := range cases {
		var got any
		if err := doc.Get(c.ptr, &got); err != nil {
			t.Fatalf("Get %s: %v", c.ptr, err)
		}
		if got != any(c.want) {
			t.Errorf("Get %s = %v, want %v", c.ptr, got, c.want)
		}
	}
	// What Get finds is what decoding finds.
	var all map[string]map[string]any
	if err := doc.Decode(&all); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"x", "y", "z", "w"} {
		var got any
		if err := doc.Get("/use/"+k, &got); err != nil || got != all["use"][k] {
			t.Errorf("/use/%s: Get %v (%v), Decode %v", k, got, err, all["use"][k])
		}
	}
	var x int
	err := doc.Get("/use/nope", &x)
	if !errors.Is(err, oneyaml.ErrNotFound) {
		t.Errorf("missing key: %v", err)
	}
}

func TestGetMergedKeysOffByDefault(t *testing.T) {
	doc := mergePtrParse(t)
	var x int
	if err := doc.Get("/use/x", &x); !errors.Is(err, oneyaml.ErrNotFound) {
		t.Fatalf("without MergeKeys: %v", err)
	}
}

func TestSetMergedKeyAddsExplicit(t *testing.T) {
	doc := mergePtrParse(t, oneyaml.MergeKeys())
	if err := doc.Set("/use/x", 100); err != nil {
		t.Fatal(err)
	}
	var x, ax int
	if err := doc.Get("/use/x", &x); err != nil || x != 100 {
		t.Fatalf("use/x = %d, %v", x, err)
	}
	if err := doc.Get("/a/x", &ax); err != nil || ax != 1 {
		t.Fatalf("source changed: a/x = %d, %v", ax, err)
	}
	out := string(doc.Bytes())
	want := mergePtrDoc[:len("a: &a {x: 1, y: 2, n: {k: a}}\nb: &b {x: 9, w: 5, n: {k: b}}\nuse:\n  <<: [*a, *b]\n  y: 20\n  z: 3\n")] + "  x: 100\n" +
		mergePtrDoc[len("a: &a {x: 1, y: 2, n: {k: a}}\nb: &b {x: 9, w: 5, n: {k: b}}\nuse:\n  <<: [*a, *b]\n  y: 20\n  z: 3\n"):]
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestMergedKeyWritesRefused(t *testing.T) {
	doc := mergePtrParse(t, oneyaml.MergeKeys())
	before := string(doc.Bytes())
	check := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, oneyaml.ErrAliasWrite) {
			t.Errorf("%s: %v, want ErrAliasWrite", name, err)
		}
		if string(doc.Bytes()) != before {
			t.Errorf("%s changed the document", name)
		}
	}
	check("Remove", doc.Remove("/use/x"))
	check("Set below", doc.Set("/use/n/k", "c"))
	check("patch remove", doc.ApplyPatch([]byte(`[{"op":"remove","path":"/use/w"}]`)))
	check("patch replace", doc.ApplyPatch([]byte(`[{"op":"replace","path":"/use/w","value":1}]`)))
	check("patch add below", doc.ApplyPatch([]byte(`[{"op":"add","path":"/use/n/j","value":1}]`)))
	// An explicit key is an ordinary key.
	if err := doc.Remove("/use/y"); err != nil {
		t.Fatal(err)
	}
	var y int
	if err := doc.Get("/use/y", &y); err != nil || y != 2 {
		t.Fatalf("after removing the explicit key the merged one shows: %d, %v", y, err)
	}
}

func TestPatchTestReadsMergedKeys(t *testing.T) {
	doc := mergePtrParse(t, oneyaml.MergeKeys())
	if err := doc.ApplyPatch([]byte(`[{"op":"test","path":"/use/x","value":1}]`)); err != nil {
		t.Fatal(err)
	}
	if err := doc.ApplyPatch([]byte(`[{"op":"test","path":"/use/y","value":2}]`)); !errors.Is(err, oneyaml.ErrTestFailed) {
		t.Fatalf("explicit key must win: %v", err)
	}
	plain := mergePtrParse(t)
	if err := plain.ApplyPatch([]byte(`[{"op":"test","path":"/use/x","value":1}]`)); !errors.Is(err, oneyaml.ErrNotFound) {
		t.Fatalf("without MergeKeys: %v", err)
	}
}

func TestMergeCycleEnds(t *testing.T) {
	doc, err := oneyaml.Parse([]byte("a: &a\n  <<: *a\n  k: 1\n"), oneyaml.MergeKeys())
	if err != nil {
		t.Skip("parse refuses the cycle:", err)
	}
	var v int
	_ = doc.Get("/a/missing", &v)
}

func TestLookupFollowsMerges(t *testing.T) {
	doc := mergePtrParse(t, oneyaml.MergeKeys())
	var use oneyaml.Node
	if err := doc.Get("/use", &use); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"x": "1", "y": "20", "w": "5"} {
		n, ok := use.Lookup(k)
		if !ok || n.Value() != want {
			t.Errorf("Lookup %s = %q, %v", k, n.Value(), ok)
		}
	}
	if _, ok := use.Lookup("nope"); ok {
		t.Error("Lookup found a missing key")
	}
	if use.Len() != 3 { // <<, y, z as written
		t.Errorf("Len = %d", use.Len())
	}
	plain := mergePtrParse(t)
	var pu oneyaml.Node
	_ = plain.Get("/use", &pu)
	if _, ok := pu.Lookup("x"); ok {
		t.Error("Lookup follows merges without MergeKeys")
	}
}
