package oneyaml

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// aliasBytesBomb is a document whose node x expands to ten thousand copies of a
// scalar of the given size, written as a few lines of aliases.
func aliasBytesBomb(size int) string {
	var b strings.Builder
	b.WriteString("a: &a " + strings.Repeat("x", size) + "\n")
	prev := "a"
	for _, n := range []string{"b", "c", "d", "e"} {
		b.WriteString(n + ": &" + n + " [" + strings.TrimSuffix(strings.Repeat("*"+prev+", ", 10), ", ") + "]\n")
		prev = n
	}
	b.WriteString("x: *e\n")
	return b.String()
}

func allocated(f func()) uint64 {
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	f()
	runtime.ReadMemStats(&m1)
	return m1.TotalAlloc - m0.TotalAlloc
}

// Writing a Node's aliases out is charged to the alias byte bound before anything
// is built, so a small document cannot make a large write.
func TestSetFromNodeChargesAliasBytes(t *testing.T) {
	src := mustParse(t, aliasBytesBomb(10000)) // expands to 100 MB; the default bound is 64 MB
	var n Node
	if err := src.Get("/x", &n); err != nil {
		t.Fatal(err)
	}
	dst := mustParse(t, "b: 2\n")
	var err error
	used := allocated(func() { err = dst.Set("/b", n) })
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != LimitMaxAliasBytes {
		t.Fatalf("got %v", err)
	}
	if string(dst.Bytes()) != "b: 2\n" {
		t.Fatal("document changed")
	}
	if used > 16<<20 {
		t.Fatalf("refusing allocated %d bytes", used)
	}
	dst2 := mustParse(t, "b: 2\n")
	if err := dst2.Apply(PatchAdd("/c", n)); !errors.As(err, &le) || le.Limit != LimitMaxAliasBytes {
		t.Fatalf("Apply: got %v", err)
	}
	if string(dst2.Bytes()) != "b: 2\n" {
		t.Fatal("document changed by Apply")
	}
}

func TestAliasBytesBoundSemantics(t *testing.T) {
	// 1 MB of expansion from a 12 KB document: inside the default bound.
	src := mustParse(t, aliasBytesBomb(100))
	var n Node
	if err := src.Get("/x", &n); err != nil {
		t.Fatal(err)
	}
	dst := mustParse(t, "b: 2\n")
	if err := dst.Set("/b", n); err != nil {
		t.Fatalf("default bound refused 1 MB: %v", err)
	}
	// An explicit limit is honoured exactly, with no ratio.
	tight := mustParse(t, "b: 2\n", Limits(LimitSet{MaxAliasBytes: 1000}))
	var le *LimitError
	if err := tight.Set("/b", n); !errors.As(err, &le) || le.Limit != LimitMaxAliasBytes || le.Max != 1000 {
		t.Fatalf("got %v", err)
	}
	// The bound is raised for a large source: ten times its own size by default.
	big := mustParse(t, aliasBytesBomb(2000), Limits(LimitSet{MaxAliasBytes: 1000}))
	if err := big.Get("/x", &n); err != nil {
		t.Fatal(err)
	}
	if err := mustParse(t, "b: 2\n", Limits(LimitSet{MaxAliasBytes: 1000})).Set("/b", n); err == nil {
		t.Fatal("explicit limit with no ratio was not honoured")
	}
	// NoLimits escapes.
	free := mustParse(t, "b: 2\n", NoLimits())
	src2 := mustParse(t, aliasBytesBomb(1000)) // 10 MB
	if err := src2.Get("/x", &n); err != nil {
		t.Fatal(err)
	}
	if err := free.Set("/b", n); err != nil {
		t.Fatalf("NoLimits: %v", err)
	}
}

// A copy or a move in a patch that has to write aliases out is charged too.
func TestPatchCopyChargesAliasBytes(t *testing.T) {
	ten := func(name string) string {
		return "[" + strings.TrimSuffix(strings.Repeat("*"+name+", ", 10), ", ") + "]"
	}
	// The value lands inside the anchor of a, which comes before the anchors it
	// uses, so none of its aliases can stay.
	src := "a: &a [" + strings.Repeat("y", 50) + "]\nb: &b " + ten("a") + "\nc: &c " + ten("b") + "\ne: &e " + ten("c") + "\n"
	for _, op := range []string{"copy", "move"} {
		d := mustParse(t, src, Limits(LimitSet{MaxAliasBytes: 1000}))
		patch := `[{"op":"` + op + `","from":"/e","path":"/a/-"}]`
		var le *LimitError
		err := d.ApplyPatch([]byte(patch))
		if !errors.As(err, &le) || le.Limit != LimitMaxAliasBytes || le.Max != 1000 {
			t.Fatalf("%s: got %v", op, err)
		}
		if string(d.Bytes()) != src {
			t.Fatalf("%s: document changed", op)
		}
		if err := sameAsFreshParse(d); err != nil {
			t.Fatalf("%s: %v", op, err)
		}
	}
	// Without the bound it works, and reads back as the whole expansion.
	d := mustParse(t, src, NoLimits())
	if err := d.ApplyPatch([]byte(`[{"op":"copy","from":"/e","path":"/a/-"}]`)); err != nil {
		t.Fatal(err)
	}
	var a []any
	if err := d.Get("/a", &a); err != nil || len(a) != 2 {
		t.Fatalf("a = %d items, %v", len(a), err)
	}
}

// An alias with no anchor in the node's own document has nothing to be written
// out as, and keeping it would bind it to whatever anchor the destination has.
func TestUndefinedAliasIsRefused(t *testing.T) {
	src := mustParse(t, "x: *zz\ny: [1, *zz]\n")
	var x, y Node
	if err := src.Get("/x", &x); err != nil {
		t.Fatal(err)
	}
	if err := src.Get("/y", &y); err != nil {
		t.Fatal(err)
	}
	for name, n := range map[string]Node{"alias": x, "nested": y} {
		for _, dstSrc := range []string{"a: &zz 1\nb: 2\n", "b: 2\n"} {
			d := mustParse(t, dstSrc)
			err := d.Set("/b", n)
			if !errors.Is(err, ErrUndefinedAlias) {
				t.Fatalf("%s into %q: got %v", name, dstSrc, err)
			}
			if string(d.Bytes()) != dstSrc {
				t.Fatalf("%s: document changed: %q", name, d.Bytes())
			}
			if err := d.Apply(PatchAdd("/c", n)); !errors.Is(err, ErrUndefinedAlias) {
				t.Fatalf("%s: Apply got %v", name, err)
			}
		}
	}
	// The same alias copied inside its own document is refused too.
	d := mustParse(t, "x: *zz\n")
	if err := d.ApplyPatch([]byte(`[{"op":"copy","from":"/x","path":"/y"}]`)); !errors.Is(err, ErrUndefinedAlias) {
		t.Fatalf("copy: got %v", err)
	}
	if string(d.Bytes()) != "x: *zz\n" {
		t.Fatal("document changed")
	}
}

// A tag stays only if the value read back under it is exactly the value set, however
// the core tag is written.
func TestCoreTagWrittenOtherWaysFollowsTheValue(t *testing.T) {
	cases := []struct {
		name, src string
		v         any
		want      any
	}{
		{"verbatim str", "k: !<tag:yaml.org,2002:str> a\n", 7, 7},
		{"verbatim int kept", "k: !<tag:yaml.org,2002:int> 7\n", 8, 8},
		{"verbatim binary", "k: !<tag:yaml.org,2002:binary> aGk=\n", 5, 5},
		{"verbatim str to string", "k: !<tag:yaml.org,2002:str> 12\n", "x", "x"},
		{"handle str", "%TAG !e! tag:yaml.org,2002:\n---\nk: !e!str a\n", 7, 7},
		{"handle int", "%TAG !e! tag:yaml.org,2002:\n---\nk: !e!int 3\n", "s", "s"},
		{"redefined secondary handle is custom", "%TAG !! tag:example.com,2000:\n---\nk: !!str a\n", 7, 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := mustParse(t, c.src)
			if err := d.Set("/k", c.v); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := Unmarshal(d.Bytes(), &got); err != nil {
				t.Fatalf("%v\n%s", err, d.Bytes())
			}
			if fmt.Sprint(got["k"]) != fmt.Sprint(c.want) || fmt.Sprintf("%T", got["k"]) != fmt.Sprintf("%T", c.want) {
				t.Fatalf("reads back %#v, want %#v\n%s", got["k"], c.want, d.Bytes())
			}
			if err := sameAsFreshParse(d); err != nil {
				t.Fatal(err)
			}
		})
	}
}
