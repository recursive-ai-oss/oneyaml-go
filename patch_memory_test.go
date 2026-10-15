package oneyaml

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// A patch of several operations must not cost a tree and a copy of the stream per
// operation: the tree is parsed into the slices it already has, and the text is
// edited in place or built in one of two buffers the steps alternate between. This
// patch (one test, a scalar replacement, an add and a remove) may allocate the
// two buffers and a little for the patch itself.
func TestPatchHasNoPerStepCopies(t *testing.T) {
	noSpliceCheck(t)
	var b strings.Builder
	for i := 0; b.Len() < 64<<10; i++ {
		fmt.Fprintf(&b, "- id: %d\n  name: Person %d\n  tags: [a, b]\n  address:\n    city: C%d\n    geo: {lat: 1.5, lon: 2.5}\n", i, i, i)
	}
	in := []byte(b.String())
	patch := []byte(`[{"op":"test","path":"/0/id","value":0},{"op":"replace","path":"/1/name","value":"Renamed Person"},` +
		`{"op":"add","path":"/2/tags/-","value":"appended"},{"op":"remove","path":"/3/address/geo"}]`)
	d, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	if err := d.ApplyPatch(patch); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&m1)
	used := m1.TotalAlloc - m0.TotalAlloc
	t.Logf("input %d bytes, patch allocated %d bytes (%.1fx)", len(in), used, float64(used)/float64(len(in)))
	if limit := 3*uint64(len(in)) + 32<<10; used > limit {
		t.Errorf("patch allocated %d bytes for a %d byte input, want at most %d", used, len(in), limit)
	}
	// And the result is what a fresh parse says it is.
	if err := sameAsFreshParse(d); err != nil {
		t.Fatal(err)
	}
}

// A text handed out by Bytes before a patch is not changed by it, although the
// patch edits its own buffers in place between operations.
func TestPatchLeavesEarlierBytesAlone(t *testing.T) {
	d := mustParse(t, "a: 1\nb: two\nc: three\nd: [x, y]\n")
	before := d.Bytes()
	copyOf := string(before)
	err := d.ApplyPatch([]byte(`[{"op":"replace","path":"/a","value":100},{"op":"replace","path":"/b","value":"x"},` +
		`{"op":"replace","path":"/c","value":"a much longer value than before"},{"op":"add","path":"/d/-","value":"z"},{"op":"replace","path":"/a","value":7}]`))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != copyOf {
		t.Fatalf("earlier Bytes changed: %q", before)
	}
	after := d.Bytes()
	copyAfter := string(after)
	if err := d.ApplyMergePatch([]byte(`{"a":1,"b":"y","e":true}`)); err != nil {
		t.Fatal(err)
	}
	if string(after) != copyAfter {
		t.Fatalf("Bytes after the first patch changed: %q", after)
	}
}

// A failed patch restores both text and tree, whatever mix of in-place and
// rebuilt steps ran before the failure.
func TestFailedPatchRestoresEverything(t *testing.T) {
	const src = "a: 1\nb: two\nc: !foo three\nd: [x, y]\ne: &k {p: 1}\nf: *k\n"
	for _, patch := range []string{
		`[{"op":"replace","path":"/a","value":2},{"op":"replace","path":"/b","value":"a longer text"},{"op":"add","path":"/d/-","value":1},{"op":"test","path":"/a","value":3}]`,
		`[{"op":"move","from":"/d","path":"/g"},{"op":"replace","path":"/a","value":9},{"op":"remove","path":"/nope"}]`,
		`[{"op":"copy","from":"/f","path":"/h"},{"op":"replace","path":"/c","value":5},{"op":"add","path":"/f/x","value":1}]`,
		`[{"op":"move","from":"/e","path":"/z"}]`,
	} {
		d := mustParse(t, src)
		if err := d.ApplyPatch([]byte(patch)); err == nil {
			t.Fatalf("%s: no error", patch)
		}
		if string(d.Bytes()) != src {
			t.Fatalf("%s: bytes changed: %q", patch, d.Bytes())
		}
		if err := sameAsFreshParse(d); err != nil {
			t.Fatalf("%s: %v", patch, err)
		}
		// The document is still usable.
		if err := d.Set("/a", 5); err != nil {
			t.Fatal(err)
		}
	}
}
