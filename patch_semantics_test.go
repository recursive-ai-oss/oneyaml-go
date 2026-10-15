package oneyaml

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// noPatchWork returns l without the bound on the work of a patch, so a test of
// another bound is not stopped by it first.
func noPatchWork(l LimitSet) Option {
	l.MaxPatchWorkRatio = NoBound
	return Limits(l)
}

// A few short copies of the whole document must not double it until memory is gone.
func TestPatchCopiesAreBounded(t *testing.T) {
	var ops []string
	for i := 0; i < 40; i++ {
		ops = append(ops, `{"op":"copy","from":"","path":"/k`+strconv.Itoa(i)+`"}`)
	}
	patch := "[" + strings.Join(ops, ",") + "]"
	const src = "a: xxxxxxxxxxxxxxxx\n"
	for _, c := range []struct {
		name string
		opt  Option
		max  int
	}{
		{"untrusted", noPatchWork(UntrustedLimits()), 8 << 20},
		{"default", noPatchWork(DefaultLimits()), 64 << 20},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "default" && testing.Short() {
				t.Skip("builds documents up to the default bound")
			}
			d := mustParse(t, src, c.opt)
			var m0, m1 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&m0)
			err := d.ApplyPatch([]byte(patch))
			runtime.ReadMemStats(&m1)
			var le *LimitError
			if !errors.As(err, &le) || le.Limit != "patch copy bytes" || le.Max != c.max {
				t.Fatalf("got %v", err)
			}
			if string(d.Bytes()) != src {
				t.Fatal("document changed")
			}
			if err := sameAsFreshParse(d); err != nil {
				t.Fatal(err)
			}
			// The text is refused before it is built. Each step that is let through
			// allocates a few copies of the document (value, text, tree); without the
			// bound the same patch allocates thousands of times the limit.
			if used := m1.TotalAlloc - m0.TotalAlloc; used > uint64(48*c.max) {
				t.Fatalf("refusing allocated %d bytes for a bound of %d", used, c.max)
			}
		})
	}
	// NoLimits removes the bound.
	d := mustParse(t, src, NoLimits())
	if err := d.ApplyPatch([]byte("[" + strings.Join(ops[:12], ",") + "]")); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := Unmarshal(d.Bytes(), &got); err != nil || len(got) != 13 {
		t.Fatalf("%d keys, %v", len(got), err)
	}
}

// The input limit bounds the document a step leaves, and a step that goes past it
// is undone with the rest of the patch.
func TestPatchOutputIsBoundedByTheInputLimit(t *testing.T) {
	src := "a: " + strings.Repeat("x", 40) + "\n"
	lim := Limits(LimitSet{MaxInputBytes: 120})
	cases := []string{
		`[{"op":"add","path":"/b","value":"` + strings.Repeat("y", 80) + `"}]`,
		`[{"op":"copy","from":"/a","path":"/b"},{"op":"copy","from":"/a","path":"/c"},{"op":"copy","from":"/a","path":"/d"}]`,
	}
	for _, p := range cases {
		d := mustParse(t, src, lim)
		var le *LimitError
		err := d.ApplyPatch([]byte(p))
		if !errors.As(err, &le) || le.Limit != "patch output size" || le.Max != 120 {
			t.Fatalf("%s: got %v", p, err)
		}
		if string(d.Bytes()) != src {
			t.Fatalf("%s: document changed", p)
		}
		if err := sameAsFreshParse(d); err != nil {
			t.Fatal(err)
		}
	}
}

// RFC 6902, section 4.5: copy is an add of the value at "from", so a copy onto its
// own sequence index inserts a second copy.
func TestCopyOntoItsOwnSequenceIndex(t *testing.T) {
	cases := []struct {
		name, src, patch string
		want             any
	}{
		{"index", "a: [1, 2]\n", `[{"op":"copy","from":"/a/0","path":"/a/0"}]`, map[string]any{"a": []any{1, 1, 2}}},
		{"block index", "a:\n  - 1\n  - 2\n", `[{"op":"copy","from":"/a/1","path":"/a/1"}]`, map[string]any{"a": []any{1, 2, 2}}},
		{"root sequence", "- x\n", `[{"op":"copy","from":"/0","path":"/0"}]`, []any{"x", "x"}},
		{"mapping key is a no-op", "a: {b: 1}\n", `[{"op":"copy","from":"/a/b","path":"/a/b"}]`, map[string]any{"a": map[string]any{"b": 1}}},
		{"move onto itself is a no-op", "a: [1, 2]\n", `[{"op":"move","from":"/a/0","path":"/a/0"}]`, map[string]any{"a": []any{1, 2}}},
		{"root onto root", "a: 1\n", `[{"op":"copy","from":"","path":""}]`, map[string]any{"a": 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := mustParse(t, c.src)
			if err := d.ApplyPatch([]byte(c.patch)); err != nil {
				t.Fatal(err)
			}
			var got any
			if err := Unmarshal(d.Bytes(), &got); err != nil {
				t.Fatalf("%v\n%s", err, d.Bytes())
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("reads back %#v, want %#v\n%s", got, c.want, d.Bytes())
			}
			if err := sameAsFreshParse(d); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The tag decides the type of the scalar it is on: under !!float the text -1 is the
// float -1, which the decoder also reads; without a tag 1 and 1.0 stay distinct.
func TestTestFollowsTheTagOfTheScalar(t *testing.T) {
	const doc = "negative: !!float -1\nzero: !!float 0\nbig: !!float 12345678901234567890123\nint: !!int 7\nplain: 1\nfloaty: 1.0\ntext: !!str 12\n"
	var decoded map[string]any
	if err := Unmarshal([]byte(doc), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["negative"] != float64(-1) || decoded["zero"] != float64(0) {
		t.Fatalf("decoder reads %#v", decoded)
	}
	cases := []struct {
		ptr, value string
		ok         bool
	}{
		{"/negative", "-1.0", true},
		{"/negative", "-1e0", true},
		{"/negative", "-2.0", false},
		{"/negative", "-1", false}, // an integer in the patch is not a float
		{"/negative", `"-1"`, false},
		{"/zero", "0.0", true},
		{"/zero", "0", false},
		{"/big", "1.2345678901234568e22", true},
		{"/int", "7", true},
		{"/int", "7.0", false},
		{"/plain", "1", true},
		{"/plain", "1.0", false},
		{"/floaty", "1.0", true},
		{"/floaty", "1", false},
		{"/text", `"12"`, true},
		{"/text", "12", false},
	}
	for _, c := range cases {
		d := mustParse(t, doc)
		err := d.ApplyPatch([]byte(`[{"op":"test","path":"` + c.ptr + `","value":` + c.value + `}]`))
		if (err == nil) != c.ok {
			t.Errorf("test %s = %s: got %v, want ok=%v", c.ptr, c.value, err, c.ok)
		}
	}
	d := mustParse(t, doc)
	if err := d.Apply(PatchTest("/negative", -1.0)); err != nil {
		t.Errorf("Apply(PatchTest(-1.0)): %v", err)
	}
	if err := d.Apply(PatchTest("", decoded)); err != nil {
		t.Errorf("Apply(PatchTest(whole document)): %v", err)
	}
}

// Members an operation does not define are ignored (RFC 6902, section 4).
func TestPatchIgnoresMembersTheOperationDoesNotUse(t *testing.T) {
	cases := []struct {
		patch string
		want  any
	}{
		{`[{"op":"add","path":"/b","value":1,"from":5}]`, map[string]any{"a": 1, "b": 1}},
		{`[{"op":"remove","path":"/a","from":null}]`, map[string]any{}},
		{`[{"op":"test","path":"/a","value":1,"from":[]}]`, map[string]any{"a": 1}},
		{`[{"op":"replace","path":"/a","value":2,"from":{"x":1}}]`, map[string]any{"a": 2}},
	}
	for _, c := range cases {
		d := mustParse(t, "a: 1\n")
		if err := d.ApplyPatch([]byte(c.patch)); err != nil {
			t.Errorf("%s: %v", c.patch, err)
			continue
		}
		var got any
		if err := Unmarshal(d.Bytes(), &got); err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: reads back %#v (%v)", c.patch, got, err)
		}
	}
	// Move and copy still need a string.
	for _, op := range []string{"move", "copy"} {
		d := mustParse(t, "a: 1\n")
		err := d.ApplyPatch([]byte(`[{"op":"` + op + `","path":"/b","from":5}]`))
		if !errors.Is(err, ErrInvalidPatch) {
			t.Errorf("%s with a number for from: %v", op, err)
		}
	}
}

// An error in the text of a patch names the operation and the path as far as they
// were read.
func TestPatchParseErrorsNameTheOperation(t *testing.T) {
	d := mustParse(t, "a: 1\n")
	err := d.ApplyPatch([]byte(`[{"op":"add","path":"/b"}]`))
	if !errors.Is(err, ErrInvalidPatch) || !strings.Contains(err.Error(), `add "/b"`) {
		t.Fatalf("got %v", err)
	}
	err = d.ApplyPatch([]byte(`[{"op":"remove","path":"/a"},{"op":"move","path":"/c"}]`))
	if !errors.Is(err, ErrInvalidPatch) || !strings.Contains(err.Error(), `move "/c"`) || !strings.Contains(err.Error(), "operation 1") {
		t.Fatalf("got %v", err)
	}
}

// A value copied past the depth limit is a depth limit error and leaves the
// document as it was.
func TestCopyPastTheDepthLimit(t *testing.T) {
	const src = "a: [[[1]]]\nb: {c: {}}\n"
	for _, p := range []string{
		`[{"op":"copy","from":"/a","path":"/b/c/x"}]`,
		`[{"op":"move","from":"/a","path":"/b/c/x"}]`,
		`[{"op":"add","path":"/b/c/x","value":[[[1]]]}]`,
	} {
		d := mustParse(t, src, Limits(LimitSet{MaxDepth: 5}))
		var le *LimitError
		err := d.ApplyPatch([]byte(p))
		if !errors.As(err, &le) || le.Limit != "depth" {
			t.Fatalf("%s: got %v", p, err)
		}
		if string(d.Bytes()) != src {
			t.Fatalf("%s: document changed", p)
		}
		if err := sameAsFreshParse(d); err != nil {
			t.Fatal(err)
		}
	}
}

// An alias far from its anchor resolves to the nearest earlier definition of the
// name, for a test step and for a Node, however many aliases there are.
func TestFarAliasesResolveToTheNearestDefinition(t *testing.T) {
	var b strings.Builder
	b.WriteString("first: &x 1\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "f%d: %d\n", i, i)
	}
	b.WriteString("second: &x 2\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "g%d: %d\n", i, i)
	}
	b.WriteString("use: [*x, *x]\nplain: *x\n")
	d := mustParse(t, b.String())
	if err := d.ApplyPatch([]byte(`[{"op":"test","path":"/use","value":[2,2]},{"op":"test","path":"/plain","value":2}]`)); err != nil {
		t.Fatal(err)
	}
	if err := d.ApplyPatch([]byte(`[{"op":"test","path":"/plain","value":1}]`)); !errors.Is(err, ErrTestFailed) {
		t.Fatalf("test against the earlier anchor: %v", err)
	}
	var n Node
	if err := d.Get("/plain", &n); err != nil {
		t.Fatal(err)
	}
	if got := n.Target().Value(); got != "2" {
		t.Fatalf("Target = %q", got)
	}
	// After an edit the index is not reused.
	if err := d.Set("/second", 3); err != nil {
		t.Fatal(err)
	}
	if err := d.Get("/plain", &n); err != nil {
		t.Fatal(err)
	}
	if got := n.Target().Value(); got != "3" {
		t.Fatalf("Target after an edit = %q", got)
	}
}
