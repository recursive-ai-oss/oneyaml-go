package oneyaml

import "testing"

// A patch that fails after edits that kept the written-size count up to date puts
// the old text back, and the count must be dropped with it: a stale count would
// let later edits pass or fail the size limit on the wrong number.
func TestRolledBackPatchDoesNotLeaveAStaleWrittenSize(t *testing.T) {
	src := cjkDoc(4 << 10)
	// Between the written size and twice the UTF-8 form: every size check counts.
	d, err := Parse(src, Limits(LimitSet{MaxInputBytes: len(src) + len(src)/4}))
	if err != nil {
		t.Fatal(err)
	}
	de, ok := d.s.docEncoding()
	if !ok {
		t.Fatal("not a UTF-16 document")
	}
	check := func(what string) {
		t.Helper()
		if e := d.s.ed; e != nil && e.wlOK && e.wl != de.writtenLen(d.s.src) {
			t.Fatalf("after %s the kept count is %d, a count gives %d", what, e.wl, de.writtenLen(d.s.src))
		}
	}
	d.s.writtenSrc(de) // start keeping the count
	before := string(d.Bytes())
	for i := 0; i < 3; i++ {
		// The first step is an in-place edit that grows the text and updates the
		// count; the second fails and takes the first back.
		err := d.ApplyPatch([]byte(`[{"op":"replace","path":"/a","value":"\u4e2d"},{"op":"replace","path":"/a","value":"\u4e2d\u4e2d\u4e2d"},{"op":"replace","path":"/a","value":"\u4e2d\u4e2d\u4e2d\u4e2d\u4e2d"},{"op":"test","path":"/nope","value":1}]`))
		if err == nil {
			t.Fatal("the failing patch succeeded")
		}
		if string(d.Bytes()) != before {
			t.Fatal("the failed patch changed the document")
		}
		check("the rolled-back patch")
		if err := d.Set("/a", i+1); err != nil {
			t.Fatal(err)
		}
		before = string(d.Bytes())
		check("an edit after it")
		d.s.writtenSrc(de)
	}
}
