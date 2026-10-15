package oneyaml

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/edit"
)

// patchCase is a document, a patch and what the document must read as after it.
type patchCase struct {
	name, in, patch, want string
}

func TestApplyPatchOutput(t *testing.T) {
	cases := []patchCase{
		{"copy drops the anchor", "a: &x {k: 1}\nb: 2\n", `[{"op":"copy","from":"/a","path":"/c"}]`,
			"a: &x {k: 1}\nb: 2\nc:\n  k: 1\n"},
		{"copy keeps an alias whose anchor is earlier", "base: &b {x: 1}\nlist: [*b]\nz: 1\n",
			`[{"op":"copy","from":"/list/0","path":"/other"}]`,
			"base: &b {x: 1}\nlist: [*b]\nz: 1\nother: *b\n"},
		{"copy writes an alias out when a later anchor shadows it", "a: &x 1\nb: [*x]\nc: &x 2\nd: 0\n",
			`[{"op":"copy","from":"/b/0","path":"/d"}]`,
			"a: &x 1\nb: [*x]\nc: &x 2\nd: 1\n"},
		{"copy into the anchored node writes the alias out", "k: &a\n  x: 1\nr: *a\n",
			`[{"op":"copy","from":"/r","path":"/k/y"}]`,
			"k: &a\n  x: 1\n  y:\n    x: 1\nr: *a\n"},
		{"move takes the anchors inside it along as plain values", "a: {p: &x 1, q: *x}\nb: 2\n",
			`[{"op":"move","from":"/a","path":"/c"}]`,
			"b: 2\nc:\n  p: 1\n  q: 1\n"},
		{"move an alias in front of its anchor writes it out", "- &x 1\n- *x\n- 3\n",
			`[{"op":"move","from":"/1","path":"/0"}]`,
			"- 1\n- &x 1\n- 3\n"},
		{"move within a sequence indexes after the removal", "- 1\n- 2\n- 3\n",
			`[{"op":"move","from":"/0","path":"/2"}]`, "- 2\n- 3\n- 1\n"},
		{"test follows an alias", "a: &x {k: [1, 2]}\nb: *x\n",
			`[{"op":"test","path":"/b/k","value":[1,2]},{"op":"replace","path":"/a/k/0","value":9}]`,
			"a: &x {k: [9, 2]}\nb: *x\n"},
		{"test ignores the order of keys", "m: {a: 1, b: 2}\nz: 0\n",
			`[{"op":"test","path":"/m","value":{"b":2,"a":1}},{"op":"replace","path":"/z","value":1}]`,
			"m: {a: 1, b: 2}\nz: 1\n"},
		{"test: hex integer equals decimal", "n: 0x1F\nz: 0\n",
			`[{"op":"test","path":"/n","value":31},{"op":"replace","path":"/z","value":1}]`,
			"n: 0x1F\nz: 1\n"},
		{"add at a sequence index inserts", "- a\n- c\n", `[{"op":"add","path":"/1","value":"b"}]`, "- a\n- b\n- c\n"},
		{"add with a dash appends", "- a\n", `[{"op":"add","path":"/-","value":"b"}]`, "- a\n- b\n"},
		{"empty patch changes nothing", "a: 1 # c\n", `[]`, "a: 1 # c\n"},
		{"extra members are ignored", "a: 1\n", `[{"op":"replace","path":"/a","value":2,"note":"x"}]`, "a: 2\n"},
		{"escaped pointer", "a/b: 1\n~: 2\n", `[{"op":"replace","path":"/a~1b","value":5},{"op":"replace","path":"/~0","value":6}]`, "a/b: 5\n~: 6\n"},
		{"unicode escapes in values", "a: 1\n", `[{"op":"replace","path":"/a","value":"\u00e9\ud83d\ude00"}]`, "a: \"é😀\"\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := mustParse(t, c.in)
			if err := d.ApplyPatch([]byte(c.patch)); err != nil {
				t.Fatal(err)
			}
			got := string(d.Bytes())
			if c.name == "unicode escapes in values" {
				// The quoting is the writer's business; the value is the point.
				var s string
				if err := d.Get("/a", &s); err != nil || s != "é😀" {
					t.Fatalf("a = %q, %v; text %q", s, err, got)
				}
				return
			}
			if got != c.want {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// Every failure leaves the document as it was and reports the failing operation.
func TestApplyPatchErrors(t *testing.T) {
	const src = "# keep\na: &x 1\nb: *x\nc: [1, 2]\nd: {k: v}\ne: 1\ne: 2\nf: 1.0\n"
	cases := []struct {
		name, patch string
		kind        error
		category    string
		index       int
	}{
		{"missing path", `[{"op":"replace","path":"/zz","value":1}]`, ErrNotFound, "patch-path", 0},
		{"failed test after an edit", `[{"op":"replace","path":"/c/0","value":9},{"op":"test","path":"/c/0","value":8}]`, ErrTestFailed, "patch-test", 1},
		{"1 is not 1.0", `[{"op":"test","path":"/f","value":1}]`, ErrTestFailed, "patch-test", 0},
		{"write through an alias", `[{"op":"add","path":"/b/k","value":1}]`, ErrAliasWrite, "refused-alias", 0},
		{"remove an anchor in use", `[{"op":"replace","path":"/c","value":1},{"op":"remove","path":"/a"}]`, ErrAnchorInUse, "refused-alias", 1},
		{"move an anchor in use", `[{"op":"move","from":"/a","path":"/z"}]`, ErrAnchorInUse, "refused-alias", 0},
		{"ambiguous key", `[{"op":"replace","path":"/e","value":3}]`, ErrAmbiguousKey, "refused-ambiguous", 0},
		{"bad pointer", `[{"op":"replace","path":"a","value":3}]`, ErrInvalidPointer, "patch-invalid", 0},
		{"unknown operation", `[{"op":"frob","path":"/a"}]`, ErrInvalidPatch, "patch-invalid", 0},
		{"missing value", `[{"op":"add","path":"/z"}]`, ErrInvalidPatch, "patch-invalid", 0},
		{"missing from", `[{"op":"move","path":"/z"}]`, ErrInvalidPatch, "patch-invalid", 0},
		{"not an array", `{"op":"add"}`, ErrInvalidPatch, "patch-invalid", 0},
		{"not json", `[{"op":"add",]`, ErrInvalidPatch, "patch-invalid", 0},
		{"duplicate member", `[{"op":"add","op":"remove","path":"/a"}]`, ErrInvalidPatch, "patch-invalid", 0},
		{"move into itself", `[{"op":"move","from":"/d","path":"/d/kx"}]`, ErrInvalidPatch, "patch-invalid", 0},
		{"copy from nothing", `[{"op":"copy","from":"/nope","path":"/z"}]`, ErrNotFound, "patch-path", 0},
		{"index past the end", `[{"op":"add","path":"/c/5","value":1}]`, ErrNotFound, "patch-path", 0},
		{"test of nothing", `[{"op":"test","path":"/nope","value":1}]`, ErrNotFound, "patch-path", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := mustParse(t, src)
			err := d.ApplyPatch([]byte(c.patch))
			if err == nil {
				t.Fatal("no error")
			}
			if !errors.Is(err, c.kind) {
				t.Fatalf("error %v is not %v", err, c.kind)
			}
			if got := errorCategory(err); got != c.category {
				t.Fatalf("category %s, want %s (%v)", got, c.category, err)
			}
			var ee *EditError
			if !errors.As(err, &ee) {
				t.Fatalf("%T is not *EditError", err)
			}
			if ee.Index != c.index {
				t.Fatalf("index %d, want %d (%v)", ee.Index, c.index, err)
			}
			if string(d.Bytes()) != src {
				t.Fatalf("document changed:\n%s", d.Bytes())
			}
			fresh := mustParse(t, src)
			if !equalTrees(d, fresh) {
				t.Fatal("tree differs from a fresh parse after the failed patch")
			}
		})
	}
}

func equalTrees(a, b *Document) bool {
	x, err1 := streamValues(a)
	y, err2 := streamValues(b)
	return err1 == nil && err2 == nil && len(x) == len(y) && modelEqualAll(x, y)
}

func modelEqualAll(x, y []edit.Value) bool {
	for i := range x {
		if !modelEqual(x[i], y[i]) {
			return false
		}
	}
	return true
}

func TestApplyTyped(t *testing.T) {
	d := mustParse(t, "a: 1\nb: [x]\n")
	err := d.Apply(PatchTest("/a", 1), PatchReplace("/a", 2), PatchAdd("/b/-", "y"),
		PatchCopy("/a", "/c"), PatchMove("/b/0", "/d"), PatchAdd("/e", map[string]any{"k": []int{1, 2}}))
	if err != nil {
		t.Fatal(err)
	}
	want := "a: 2\nb: [y]\nc: 2\nd: x\ne:\n  k:\n    - 1\n    - 2\n"
	if got := string(d.Bytes()); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	before := string(d.Bytes())
	err = d.Apply(PatchRemove("/a"), PatchTest("/c", 3))
	var ee *EditError
	if !errors.Is(err, ErrTestFailed) || !errors.As(err, &ee) || ee.Index != 1 || ee.Op != "test" || ee.Pointer != "/c" {
		t.Fatalf("got %v", err)
	}
	if string(d.Bytes()) != before {
		t.Fatal("document changed")
	}
	if err := d.Apply(PatchOp{Op: "frob"}); !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("got %v", err)
	}
}

func TestApplyPatchSelectsDocument(t *testing.T) {
	d := mustParse(t, "a: 1\n---\na: 2\n---\na: 3\n")
	if err := d.Doc(1).ApplyPatch([]byte(`[{"op":"replace","path":"/a","value":20}]`)); err != nil {
		t.Fatal(err)
	}
	if got := string(d.Bytes()); got != "a: 1\n---\na: 20\n---\na: 3\n" {
		t.Fatalf("got %q", got)
	}
	if err := d.Doc(5).ApplyPatch([]byte(`[{"op":"add","path":"/a","value":1}]`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := d.Doc(2).ApplyMergePatch([]byte(`{"a":null,"b":1}`)); err != nil {
		t.Fatal(err)
	}
	if got := string(d.Bytes()); got != "a: 1\n---\na: 20\n---\nb: 1\n" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyPatchLimits(t *testing.T) {
	d := mustParse(t, "a: 1\n")
	deep := `[{"op":"add","path":"/b","value":` + strings.Repeat("[", 20000) + strings.Repeat("]", 20000) + `}]`
	err := d.ApplyPatch([]byte(deep))
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != "depth" {
		t.Fatalf("got %v", err)
	}
	if string(d.Bytes()) != "a: 1\n" {
		t.Fatal("document changed")
	}
	// An alias bomb is not expanded by test: the budget stops it.
	var b strings.Builder
	b.WriteString("a: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	prev := "a0"
	for i := 1; i < 12; i++ {
		name := "a" + string(rune('0'+i%10)) + string(rune('a'+i))
		b.WriteString(name + ": &" + name + " [" + strings.Repeat("*"+prev+", ", 10) + "*" + prev + "]\n")
		prev = name
	}
	bomb, err := Parse([]byte(b.String()), Limits(LimitSet{MaxAliasNodes: 1000}))
	if err != nil {
		t.Fatal(err)
	}
	err = bomb.ApplyPatch([]byte(`[{"op":"test","path":"/` + prev + `","value":[]}]`))
	if err == nil {
		t.Fatal("no error")
	}
	// The count of the first level differs, so it may fail by comparison before
	// the budget: both are failures that leave the document alone.
	if string(bomb.Bytes()) != b.String() {
		t.Fatal("document changed")
	}
	err = bomb.ApplyPatch([]byte(`[{"op":"copy","from":"/` + prev + `","path":"/a/-"}]`))
	if !errors.As(err, &le) || le.Limit != "alias expansion nodes" {
		t.Fatalf("copy of a bomb: got %v", err)
	}
}

func TestMergePatchRFCExamples(t *testing.T) {
	// RFC 7396 appendix A.
	cases := [][3]string{
		{`{"a":"b"}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"b"}`, `{"b":"c"}`, `{"a":"b","b":"c"}`},
		{`{"a":"b"}`, `{"a":null}`, `{}`},
		{`{"a":"b","b":"c"}`, `{"a":null}`, `{"b":"c"}`},
		{`{"a":["b"]}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"c"}`, `{"a":["b"]}`, `{"a":["b"]}`},
		{`{"a":{"b":"c"}}`, `{"a":{"b":"d","c":null}}`, `{"a":{"b":"d"}}`},
		{`{"a":[{"b":"c"}]}`, `{"a":[1]}`, `{"a":[1]}`},
		{`["a","b"]`, `["c","d"]`, `["c","d"]`},
		{`{"a":"b"}`, `["c"]`, `["c"]`},
		{`{"a":"foo"}`, `null`, `null`},
		{`{"a":"foo"}`, `"bar"`, `"bar"`},
		{`{"e":null}`, `{"a":1}`, `{"e":null,"a":1}`},
		{`[1,2]`, `{"a":"b","c":null}`, `{"a":"b"}`},
		{`{}`, `{"a":{"bb":{"ccc":null}}}`, `{"a":{"bb":{}}}`},
	}
	for _, c := range cases {
		d := mustParse(t, c[0]+"\n")
		if err := d.ApplyMergePatch([]byte(c[1])); err != nil {
			t.Fatalf("%s + %s: %v", c[0], c[1], err)
		}
		got, err := streamValues(mustParse(t, string(d.Bytes())))
		if err != nil || len(got) != 1 {
			t.Fatalf("%s + %s: %v", c[0], c[1], err)
		}
		want, err := orderedValue([]byte(c[2]))
		if err != nil {
			t.Fatal(err)
		}
		if !modelEqual(got[0], want) {
			t.Errorf("%s + %s = %q, want %s", c[0], c[1], d.Bytes(), c[2])
		}
	}
}

func TestMergePatchAliasAndAtomicity(t *testing.T) {
	const src = "base: &b\n  x: 1\nuse: *b\nn: 1\n"
	d := mustParse(t, src)
	err := d.ApplyMergePatch([]byte(`{"n":2,"use":{"x":2}}`))
	if !errors.Is(err, ErrAliasWrite) {
		t.Fatalf("got %v", err)
	}
	if string(d.Bytes()) != src {
		t.Fatalf("a failed merge patch changed the document:\n%s", d.Bytes())
	}
	// Merging into the anchor's source shows through the alias; an empty object
	// against an alias changes nothing.
	if err := d.ApplyMergePatch([]byte(`{"base":{"x":5},"use":{}}`)); err != nil {
		t.Fatal(err)
	}
	if got := string(d.Bytes()); got != "base: &b\n  x: 5\nuse: *b\nn: 1\n" {
		t.Fatalf("got %q", got)
	}
	// An alias to a scalar is replaced as a value.
	d = mustParse(t, "s: &s 1\nt: *s\n")
	if err := d.ApplyMergePatch([]byte(`{"t":{"k":null,"j":1}}`)); err != nil {
		t.Fatal(err)
	}
	if got := string(d.Bytes()); !strings.HasPrefix(got, "s: &s 1\nt:") {
		t.Fatalf("got %q", got)
	}
	var m map[string]any
	if err := d.Get("/t", &m); err != nil || len(m) != 1 || m["j"] != 1 {
		t.Fatalf("t = %v, %v", m, err)
	}
	if err := d.ApplyMergePatch([]byte(`{"a":`)); !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("got %v", err)
	}
}

func TestMergePatchKeepsLayout(t *testing.T) {
	d := mustParse(t, "# top\na: 1   # one\nb:\n  c: [1, 2]  # list\n  d: x\n\n# tail\n")
	if err := d.ApplyMergePatch([]byte(`{"a":2,"b":{"c":[3],"d":null,"e":"new"}}`)); err != nil {
		t.Fatal(err)
	}
	want := "# top\na: 2   # one\nb:\n  c: [3]  # list\n  e: new\n\n# tail\n"
	if got := string(d.Bytes()); !bytes.Equal([]byte(got), []byte(want)) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
