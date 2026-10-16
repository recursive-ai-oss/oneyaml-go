package oneyaml

import (
	"bytes"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
)

func mustParse(t *testing.T, src string, opts ...Option) *Document {
	t.Helper()
	d, err := Parse([]byte(src), opts...)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return d
}

func TestSetConventions(t *testing.T) {
	tests := []struct {
		name, in, ptr string
		val           any
		want          string
	}{
		{"anchor stays on a replaced scalar", "a: &x 1\nb: *x\n", "/a", 5, "a: &x 5\nb: *x\n"},
		{"anchor stays when a mapping becomes a scalar", "base: &b\n  x: 1\nn: 2\n", "/base", 5, "base: &b 5\nn: 2\n"},
		{"core tag that no longer fits is dropped", "a: !!str 5\nb: 1\n", "/a", 6, "a: 6\nb: 1\n"},
		{"core tag that still fits stays", "a: !!str x\n", "/a", "y", "a: !!str y\n"},
		{"local tag stays on a scalar", "a: !e v\n", "/a", "w", "a: !e w\n"},
		{"local tag goes when the kind changes", "a: !e v\n", "/a", []int{1}, "a:\n  - 1\n"},
		{"same value is a no-op", "a: 0x10 # hex\n", "/a", 16, "a: 0x10 # hex\n"},
		{"existing null spelling is kept", "a: ~\nb: 1\n", "/a", nil, "a: ~\nb: 1\n"},
		{"new null is written null", "a: 1\n", "/a", nil, "a: null\n"},
		{"empty value gets a scalar", "a:\nb: 1\n", "/a", 3, "a: 3\nb: 1\n"},
		{"empty value keeps its comment", "a: # why\nb: 1\n", "/a", 3, "a: 3 # why\nb: 1\n"},
		{"scalar becomes a block mapping", "a: 1 # c\nb: 2\n", "/a", map[string]int{"x": 1, "y": 2}, "a: # c\n  x: 1\n  y: 2\nb: 2\n"},
		{"scalar becomes a block sequence", "a: 1\nb: 2\n", "/a", []string{"p", "q"}, "a:\n  - p\n  - q\nb: 2\n"},
		{"block scalar becomes a mapping", "a: |\n  t\nb: 2\n", "/a", map[string]int{"x": 1}, "a:\n  x: 1\nb: 2\n"},
		{"block mapping is replaced in place", "k:\n    a: 1\n    b: 2\nz: 1\n", "/k", map[string]int{"c": 3}, "k:\n    c: 3\nz: 1\n"},
		{"flow sequence stays flow", "a: [1, 2]\n", "/a", []int{3, 4, 5}, "a: [3, 4, 5]\n"},
		{"empty collection is written inline", "a:\n  x: 1\nb: 2\n", "/a", map[string]int{}, "a: {}\nb: 2\n"},
		{"sequence item becomes a mapping", "- a\n- b\n", "/0", map[string]int{"x": 1, "y": 2}, "- x: 1\n  y: 2\n- b\n"},
		{"multi-line string into plain falls to double quotes", "a: x\n", "/a", "l1\nl2", `a: "l1\nl2"` + "\n"},
		{"plain flow item with a comma is quoted", "a: [x, y]\n", "/a/0", "p,q", "a: ['p,q', y]\n"},
		{"folded value", "a: >\n  one\n  two\n", "/a", "x\n\ny\n", "a: >\n  x\n\n\n  y\n"},
		{"replace root scalar", "5\n", "", "x", "x\n"},
		{"replace root mapping", "# c\na: 1\n", "", map[string]int{"b": 2}, "# c\nb: 2\n"},
		{"add before a footer comment", "a: 1\n# end\n", "/b", 2, "a: 1\nb: 2\n# end\n"},
		{"add keeps a missing final newline", "a: 1", "/b", 2, "a: 1\nb: 2"},
		{"add to an empty flow mapping with spaces", "a: { }\n", "/a/k", 1, "a: {k: 1}\n"},
		{"add a nested mapping uses the sibling step", "x:\n    y: 1\n", "/z", map[string]int{"p": 1}, "x:\n    y: 1\nz:\n    p: 1\n"},
		{"add key to a compact sequence mapping", "- a: 1\n  b: 2\n", "/0/c", 3, "- a: 1\n  b: 2\n  c: 3\n"},
		{"key with a colon is quoted", "a: 1\n", "/b: c", 1, "a: 1\n'b: c': 1\n"},
		{"numeric looking key stays a string", "a: 1\n", "/80", 1, "a: 1\n'80': 1\n"},
		{"multi-line string added becomes a literal", "a: 1\n", "/s", "l1\nl2\n", "a: 1\ns: |\n  l1\n  l2\n"},
		{"string next to double quoted siblings", "a: \"x\"\n", "/b", "multi\nline", "a: \"x\"\nb: \"multi\\nline\"\n"},
		{"remove keeps comment above next entry", "a: 1\n# b\nb: 2\n\n# c\nc: 3\n", "/b", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := mustParse(t, tc.in)
			var err error
			if tc.want == "" {
				err = d.Remove(tc.ptr)
				tc.want = "a: 1\n\n# c\nc: 3\n"
			} else {
				err = d.Set(tc.ptr, tc.val)
			}
			if err != nil {
				t.Fatalf("edit failed: %v", err)
			}
			if got := string(d.Bytes()); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestAddInsertsIntoSequences(t *testing.T) {
	tests := []struct{ name, in, ptr, want string }{
		{"before the first item of a nested sequence on the dash line", "- - a\n  - b\n", "/0/0", "- - n\n  - a\n  - b\n"},
		{"in the middle", "- a\n# about c\n- c\n", "/1", "- a\n- n\n# about c\n- c\n"},
		{"flow start", "[b]\n", "/0", "[n, b]\n"},
		{"odd spacing after the dash is copied", "k:\n   -   x\n   -   y\n", "/k/1", "k:\n   -   x\n   -   n\n   -   y\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := mustParse(t, tc.in)
			v := edit.Str("n")
			if err := d.s.edit(0, opAdd, tc.ptr, &v); err != nil {
				t.Fatal(err)
			}
			if got := string(d.Bytes()); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestEditErrors(t *testing.T) {
	const src = "base: &b\n  x: 1\ncopy: *b\n80: a\n\"80\": b\nl: [1, 2]\n"
	tests := []struct {
		name string
		do   func(d *Document) error
		want error
	}{
		{"write below alias", func(d *Document) error { return d.Set("/copy/x", 2) }, ErrAliasWrite},
		{"add below alias", func(d *Document) error { return d.Set("/copy/y", 2) }, ErrAliasWrite},
		{"remove below alias", func(d *Document) error { return d.Remove("/copy/x") }, ErrAliasWrite},
		{"remove anchored node that is aliased", func(d *Document) error { return d.Remove("/base") }, ErrAnchorInUse},
		{"replace the container of a used anchor", func(d *Document) error {
			d2 := mustParse(t, "l:\n  - a: &x 1\nr: *x\n")
			return d2.Set("/l", 5)
		}, ErrAnchorInUse},
		{"ambiguous key", func(d *Document) error { return d.Set("/80", 1) }, ErrAmbiguousKey},
		{"ambiguous remove", func(d *Document) error { return d.Remove("/80") }, ErrAmbiguousKey},
		{"missing path", func(d *Document) error { return d.Remove("/nope") }, ErrNotFound},
		{"missing parent", func(d *Document) error { return d.Set("/nope/x", 1) }, ErrNotFound},
		{"index past the end", func(d *Document) error { return d.Set("/l/5", 1) }, ErrNotFound},
		{"leading zero index", func(d *Document) error { return d.Set("/l/01", 1) }, ErrNotFound},
		{"below a scalar", func(d *Document) error { return d.Set("/80/x", 1) }, ErrAmbiguousKey},
		{"pointer without a slash", func(d *Document) error { return d.Set("l", 1) }, ErrInvalidPointer},
		{"bad escape", func(d *Document) error { return d.Set("/a~2", 1) }, ErrInvalidPointer},
		{"no such document", func(d *Document) error { return d.Doc(3).Set("/a", 1) }, ErrNotFound},
		{"remove root", func(d *Document) error { return d.Remove("") }, ErrUnsupportedEdit},
		{"unsupported value", func(d *Document) error { return d.Set("/l", make(chan int)) }, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := mustParse(t, src)
			err := tc.do(d)
			if err == nil {
				t.Fatal("expected an error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("error %v is not %v", err, tc.want)
			}
			var ee *EditError
			if tc.want != nil && !errors.As(err, &ee) {
				t.Errorf("error %T is not an *EditError", err)
			}
			if string(d.Bytes()) != src {
				t.Errorf("document changed after a failed edit: %q", d.Bytes())
			}
		})
	}
}

func TestReplaceAliasNodeAndSource(t *testing.T) {
	d := mustParse(t, "base: &b\n  x: 1\ncopy: *b\n")
	if err := d.Set("/copy", 5); err != nil {
		t.Fatal(err)
	}
	if got := string(d.Bytes()); got != "base: &b\n  x: 1\ncopy: 5\n" {
		t.Errorf("got %q", got)
	}
	d = mustParse(t, "base: &b\n  x: 1\ncopy: *b\n")
	if err := d.Set("/base/x", 2); err != nil {
		t.Fatal(err)
	}
	var viaAlias int
	if err := d.Get("/copy/x", &viaAlias); err != nil || viaAlias != 2 {
		t.Errorf("read through the alias: %d, %v", viaAlias, err)
	}
}

func TestMultipleDocuments(t *testing.T) {
	d := mustParse(t, "a: 1\n---\nb: 2\n...\n---\nc: 3\n")
	if d.Len() != 3 {
		t.Fatalf("Len = %d", d.Len())
	}
	view := d.Doc(2)
	if err := view.Set("/c", 4); err != nil {
		t.Fatal(err)
	}
	if err := d.Doc(1).Remove("/b"); err != nil {
		t.Fatal(err)
	}
	// Every view sees every change.
	if got := string(view.Bytes()); got != "a: 1\n---\n{}\n...\n---\nc: 4\n" {
		t.Errorf("got %q", got)
	}
	var c int
	if err := view.Get("/c", &c); err != nil || c != 4 {
		t.Errorf("c = %d, %v", c, err)
	}
	if err := d.Get("/b", &c); !errors.Is(err, ErrNotFound) {
		t.Errorf("first document has no /b: %v", err)
	}
}

func TestEmptyAndOddStreams(t *testing.T) {
	for _, src := range []string{"", "\n", "# only a comment\n", "---\n", "--- \n...\n", "\ufeff", "a", "- ", "{}"} {
		d, err := Parse([]byte(src))
		if err != nil {
			t.Errorf("Parse(%q): %v", src, err)
			continue
		}
		if string(d.Bytes()) != src {
			t.Errorf("round trip of %q gave %q", src, d.Bytes())
		}
		_ = d.Root().Kind()
		_ = d.Set("/a", 1)
		_ = d.Set("", 1)
		_ = d.Remove("/a")
		var x any
		_ = d.Get("", &x)
	}
	d := mustParse(t, "")
	if d.Len() != 0 || d.Root().IsValid() {
		t.Error("empty input has no documents")
	}
	if err := d.Set("/a", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("editing no document: %v", err)
	}
	d = mustParse(t, "# c\n")
	if err := d.Set("", map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if got := string(d.Bytes()); got != "# c\na: 1\n" {
		t.Errorf("got %q", got)
	}
}

func TestBOMAndBreaksKept(t *testing.T) {
	d := mustParse(t, "\ufeffa: 1\r\nb:\r\n  - x\r\n")
	if err := d.Set("/b/-", "y\nz"); err != nil {
		t.Fatal(err)
	}
	if err := d.Set("/c", map[string]int{"p": 1}); err != nil {
		t.Fatal(err)
	}
	want := "\ufeffa: 1\r\nb:\r\n  - x\r\n  - |-\r\n    y\r\n    z\r\nc:\r\n  p: 1\r\n"
	if string(d.Bytes()) != want {
		t.Errorf("got  %q\nwant %q", d.Bytes(), want)
	}
}

func TestBytesAreImmutableAcrossEdits(t *testing.T) {
	d := mustParse(t, "a: 1\n")
	before := d.Bytes()
	if err := d.Set("/a", 2); err != nil {
		t.Fatal(err)
	}
	if string(before) != "a: 1\n" {
		t.Errorf("an earlier Bytes result changed: %q", before)
	}
	src := []byte("a: 1\n")
	d, _ = Parse(src)
	src[3] = '9'
	if string(d.Bytes()) != "a: 1\n" {
		t.Error("Parse must copy its input")
	}
	var buf bytes.Buffer
	if n, err := d.WriteTo(&buf); err != nil || int(n) != buf.Len() || buf.String() != "a: 1\n" {
		t.Errorf("WriteTo: %d %v %q", n, err, buf.String())
	}
}

func TestNodeView(t *testing.T) {
	d := mustParse(t, "# head\nname: api # tail\nitems:\n  - &a one\n  - 2\n  - *a\nm: {x: !!str 1, \"y\": 'z'}\nblock: |\n  text\n")
	root := d.Root()
	if root.Kind() != MappingNode || root.Len() != 4 || root.Style() != BlockStyle {
		t.Fatalf("root: %v %d %v", root.Kind(), root.Len(), root.Style())
	}
	name, ok := root.Lookup("name")
	if !ok || name.Value() != "api" || name.Style() != PlainStyle || name.LineComment() != "# tail" {
		t.Errorf("name: %q %v %q", name.Value(), name.Style(), name.LineComment())
	}
	if h := name.HeadComment(); h != "# head" {
		t.Errorf("head comment %q", h)
	}
	if l, c := name.Position(); l != 2 || c != 7 {
		t.Errorf("position %d:%d", l, c)
	}
	items, _ := root.Lookup("items")
	if items.Kind() != SequenceNode || items.Len() != 3 {
		t.Fatalf("items %v %d", items.Kind(), items.Len())
	}
	one := items.Item(0)
	if one.Anchor() != "a" || one.Tag() != "tag:yaml.org,2002:str" || one.Value() != "one" {
		t.Errorf("one: %q %q %q", one.Anchor(), one.Tag(), one.Value())
	}
	if items.Item(1).Tag() != "tag:yaml.org,2002:int" {
		t.Errorf("tag of 2: %q", items.Item(1).Tag())
	}
	al := items.Item(2)
	if al.Kind() != AliasNode || al.AliasName() != "a" || al.Target().Value() != "one" {
		t.Errorf("alias: %v %q", al.Kind(), al.AliasName())
	}
	m, _ := root.Lookup("m")
	if m.Style() != FlowStyle {
		t.Errorf("m style %v", m.Style())
	}
	x, _ := m.Lookup("x")
	if x.Tag() != "tag:yaml.org,2002:str" || x.Value() != "1" {
		t.Errorf("x: %q %q", x.Tag(), x.Value())
	}
	y, _ := m.Lookup("y")
	if y.Style() != DoubleQuotedStyle && y.Value() != "z" {
		t.Errorf("y: %v", y.Style())
	}
	k, v := m.Entry(1)
	if k.Value() != "y" || v.Style() != SingleQuotedStyle {
		t.Errorf("entry: %q %v", k.Value(), v.Style())
	}
	b, _ := root.Lookup("block")
	if b.Style() != LiteralStyle || b.Value() != "text\n" {
		t.Errorf("block: %v %q", b.Style(), b.Value())
	}
	// A Node is invalid after an edit.
	if err := d.Set("/name", "other"); err != nil {
		t.Fatal(err)
	}
	if name.IsValid() || name.Value() != "" || name.Kind() != InvalidNode {
		t.Error("a Node must go stale after an edit")
	}
	var zero Node
	if zero.IsValid() || zero.Len() != 0 || zero.Tag() != "" {
		t.Error("zero Node")
	}
	var nd Node
	if err := d.Get("/items/1", &nd); err != nil || nd.Value() != "2" {
		t.Errorf("Get *Node: %v", err)
	}
}

func TestTagExpansion(t *testing.T) {
	d := mustParse(t, "%TAG !e! tag:example.com,2000:\n---\na: !e!t v\nb: !<tag:x,2000:y> w\nc: !local z\n")
	for _, c := range []struct{ key, want string }{
		{"a", "tag:example.com,2000:t"}, {"b", "tag:x,2000:y"}, {"c", "!local"},
	} {
		n, _ := d.Root().Lookup(c.key)
		if n.Tag() != c.want {
			t.Errorf("%s: tag %q, want %q", c.key, n.Tag(), c.want)
		}
	}
}

func TestGetTypes(t *testing.T) {
	d := mustParse(t, "i: 42\nf: 1.5\nb: true\ns: hi\nn: ~\nbig: 123456789012345678901234567890\nu: 18446744073709551615\nseq: [1, {a: b}]\nm: {k: v, 1: x}\nal: &x [1, 2]\nbe: *x\n")
	var i int
	var f float64
	var b bool
	var s string
	var any1 any
	var u uint64
	for _, c := range []struct {
		ptr string
		dst any
	}{{"/i", &i}, {"/f", &f}, {"/b", &b}, {"/s", &s}, {"/n", &any1}, {"/u", &u}} {
		if err := d.Get(c.ptr, c.dst); err != nil {
			t.Errorf("%s: %v", c.ptr, err)
		}
	}
	if i != 42 || f != 1.5 || !b || s != "hi" || any1 != nil || u != 18446744073709551615 {
		t.Errorf("values: %v %v %v %v %v %v", i, f, b, s, any1, u)
	}
	var bg any
	_ = d.Get("/big", &bg)
	if x, ok := bg.(*big.Int); !ok || x.String() != "123456789012345678901234567890" {
		t.Errorf("big = %#v", bg)
	}
	var seq []any
	if err := d.Get("/seq", &seq); err != nil || len(seq) != 2 || seq[0] != 1 || seq[1].(map[string]any)["a"] != "b" {
		t.Errorf("seq = %#v, %v", seq, err)
	}
	var m map[any]any
	if err := d.Get("/m", &m); err != nil || m["k"] != "v" || m[1] != "x" {
		t.Errorf("m = %#v, %v", m, err)
	}
	var viaAlias []any
	if err := d.Get("/be", &viaAlias); err != nil || len(viaAlias) != 2 {
		t.Errorf("alias read: %v %v", viaAlias, err)
	}
	var whole any
	if err := d.Get("", &whole); err != nil || len(whole.(map[string]any)) != 11 {
		t.Errorf("whole: %v", err)
	}
	var te *TypeError
	if err := d.Get("/s", &i); !errors.As(err, &te) {
		t.Errorf("string into int: %v", err)
	}
	if err := d.Get("/i", new(chan int)); !errors.As(err, &te) {
		t.Errorf("unsupported target: %v", err)
	}
	if err := d.Get("/zz", &i); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}

func TestAliasBudgetOnGet(t *testing.T) {
	var b strings.Builder
	b.WriteString("a: &a [x, x, x, x, x, x, x, x, x, x]\n")
	prev := "a"
	for i := 0; i < 8; i++ {
		name := string(rune('b' + i))
		b.WriteString(name + ": &" + name + " [")
		for j := 0; j < 10; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString("*" + prev)
		}
		b.WriteString("]\n")
		prev = name
	}
	d := mustParse(t, b.String())
	var x any
	err := d.Get("/"+prev, &x)
	var le *LimitError
	if !errors.As(err, &le) {
		t.Fatalf("expected a LimitError, got %v", err)
	}
	if err := d.Get("/a", &x); err != nil {
		t.Errorf("small alias read: %v", err)
	}
	d2, _ := Parse([]byte(b.String()), NoLimits())
	_ = d2
}

func TestDepthLimit(t *testing.T) {
	deep := strings.Repeat("[", 5000) + strings.Repeat("]", 5000)
	if _, err := Parse([]byte(deep), Limits(LimitSet{MaxDepth: 100})); err == nil {
		t.Error("expected a depth error")
	} else {
		var le *LimitError
		if !errors.As(err, &le) {
			t.Errorf("error %T %v", err, err)
		}
	}
	d, err := Parse([]byte(deep))
	if err != nil {
		t.Fatal(err)
	}
	var x any
	if err := d.Get("", &x); err != nil {
		t.Errorf("deep read: %v", err)
	}
	if _, err := Parse([]byte("a: 1"), Limits(LimitSet{MaxInputBytes: 2})); err == nil {
		t.Error("expected an input size error")
	}
	if _, err := Parse([]byte("a: 1\n---\nb: 2\n"), Limits(LimitSet{MaxDocuments: 1})); err == nil {
		t.Error("expected a document count error")
	}
	if _, err := Parse([]byte("a: xxxxxxxx\n"), Limits(LimitSet{MaxScalarBytes: 4})); err == nil {
		t.Error("expected a scalar size error")
	}
}

func TestSyntaxErrorFromParse(t *testing.T) {
	_, err := Parse([]byte("a: [1\nb: 2\n"))
	var se *SyntaxError
	if !errors.As(err, &se) || se.Line == 0 {
		t.Errorf("error %T %v", err, err)
	}
}

// TestEditRegressions replays layouts the fuzzer found. Each edit must read back
// exactly, leave every other node as it was, and keep the tree equal to a fresh
// parse of the new text.
func TestEditRegressions(t *testing.T) {
	str := func(s string) *edit.Value { v := edit.Str(s); return &v }
	num := func(s string) *edit.Value { return &edit.Value{Kind: edit.Int, Text: s} }
	tests := []struct {
		name, in string
		op       editKindOp
		ptr      string
		v        *edit.Value
		want     string // exact output; empty means only the invariants are checked
	}{
		{"block scalar at the end of the file keeps its value when an entry follows", "a: |\n  x", opSet, "/b", num("1"), "a: |-\n  x\nb: 1"},
		{"keep scalar at the end of the file", "a: |+\n  x", opSet, "/b", num("1"), "a: |-\n  x\nb: 1"},
		{"blank lines after a removed entry do not join a keep scalar", "a: |+\n  k\nb: 1\n\nc: 2\n", opRemove, "/b", nil, "a: |+\n  k\nc: 2\n"},
		{"value after an empty plain key in a flow mapping", "{: {0}} ", opSet, "/", str("x"), "{: x} "},
		{"entry without a colon gets one", "? a\n", opSet, "/a", num("1"), "? a\n: 1\n"},
		{"empty document after a marker and a comment", "---\n#c\n", opSet, "", str("v"), "--- v\n#c\n"},
		{"block scalar header on a later line than its key", ": \n>\n0:", opSet, "/", num("4"), ""},
		{"the stripped scalar is not the last text of the file", "- - |+\n    x\n\n\n  -", opSet, "/-", str("N"), "- - |+\n    x\n\n\n  -\n- N"},
		{"adding to an alias-keyed flow mapping", ": {*a, b: 1}", opSet, "//a", str("v"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := mustParse(t, tc.in)
			before, err := streamValues(d)
			if err != nil {
				t.Fatal(err)
			}
			if err := d.s.edit(0, tc.op, tc.ptr, tc.v); err != nil {
				if tc.want == "" && errors.Is(err, errEdit) {
					return // a safe refusal is acceptable for the exotic layouts
				}
				t.Fatal(err)
			}
			if tc.want != "" && string(d.Bytes()) != tc.want {
				t.Errorf("got  %q\nwant %q", d.Bytes(), tc.want)
			}
			if err := checkReadBack(before, d, 0, tc.op, tc.ptr, tc.v); err != nil {
				t.Error(err)
			}
			d.s.tree.Settle()
			var fresh cst.Tree
			if err := cst.Build(&fresh, d.s.src, -1); err != nil || !reflect.DeepEqual(fresh.Nodes, d.s.tree.Nodes) {
				t.Errorf("tree differs from a fresh parse (%v)", err)
			}
		})
	}
}
