package oneyaml

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// cjkDoc is a UTF-16 document of about size bytes whose UTF-8 form is half as
// long again as what is written: three-byte characters take two bytes in UTF-16.
func cjkDoc(size int) []byte {
	var sb strings.Builder
	sb.WriteString("a: 0\n")
	line := strings.Repeat("\u4e2d", 400)
	for i := 0; i < size/800; i++ {
		fmt.Fprintf(&sb, "k%d: %s\n", i, line)
	}
	return utf16le(sb.String())
}

// The size check after a step must cost the step, not the document: the text is
// counted once, and the count is kept up to date by the replacements.
func TestWrittenSizeIsCountedOncePerPatch(t *testing.T) {
	src := cjkDoc(200 << 10)
	// Between the written size and the most it could be (twice the UTF-8 form):
	// every step takes the exact count.
	lim := len(src) + len(src)/4
	parse := func() *Document {
		d, err := Parse(src, Limits(LimitSet{MaxInputBytes: lim}))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	const ops = 200
	var p strings.Builder
	p.WriteString("[")
	for i := 0; i < ops; i++ {
		if i > 0 {
			p.WriteString(",")
		}
		fmt.Fprintf(&p, `{"op":"replace","path":"/a","value":%d}`, i%10)
	}
	p.WriteString("]")

	d := parse()
	if err := d.ApplyPatch([]byte(p.String())); err != nil {
		t.Fatal(err)
	}
	// The first edit may have to copy the text into a buffer of its own, which
	// forgets the count once; no step after it does.
	if n := d.s.ed.wlScans; n > 2 {
		t.Errorf("a patch of %d steps counted the whole text %d times", ops, n)
	}
	d = parse()
	for i := 0; i < ops; i++ {
		if err := d.Set("/a", i%10); err != nil {
			t.Fatal(err)
		}
	}
	if n := d.s.ed.wlScans; n > 2 {
		t.Errorf("%d Sets counted the whole text %d times", ops, n)
	}
}

// The kept count must always equal a fresh count, whatever edits came before,
// and the limit must hold at its exact byte.
func TestKeptWrittenSizeMatchesACount(t *testing.T) {
	src := utf16le("a: 1\nb: [x, y]\nc: {d: \u4e2d\u4e2d}\n")
	d, err := Parse(src, Limits(LimitSet{MaxInputBytes: 1 << 20}))
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
	d.s.writtenSrc(de) // start keeping it
	steps := []func() error{
		func() error { return d.Set("/a", "\u4e2d\u4e2d\u4e2d") },
		func() error { return d.Set("/a", 7) },
		func() error { return d.Set("/e", "new") },
		func() error { return d.Set("/b/-", "\U0001F600") },
		func() error { return d.Remove("/b/0") },
		func() error { return d.Rename("/c", "cc") },
		func() error {
			return d.ApplyPatch([]byte(`[{"op":"add","path":"/f","value":"\u4e2d"},{"op":"replace","path":"/a","value":"zz"},{"op":"remove","path":"/e"}]`))
		},
		func() error {
			return d.ApplyPatch([]byte(`[{"op":"replace","path":"/a","value":1},{"op":"test","path":"/nope","value":1}]`))
		}, // fails, rolled back
		func() error { return d.ApplyMergePatch([]byte(`{"g":"\u4e2d\u4e2d","f":null}`)) },
	}
	for i, st := range steps {
		err := st()
		if i == 7 && err == nil {
			t.Fatal("the failing patch succeeded")
		}
		if i != 7 && err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		check("step " + strconv.Itoa(i))
		d.s.writtenSrc(de)
		check("recount " + strconv.Itoa(i))
	}
}
