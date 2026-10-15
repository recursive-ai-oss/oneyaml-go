package oneyaml

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// editBoth applies fn to two documents parsed from src, one edited through
// regions and one with every edit parsing the whole stream, and returns both.
func editBoth(t *testing.T, src string, fn func(d *Document) error) (region, whole *Document, errRegion, errWhole error) {
	t.Helper()
	region = mustParse(t, src)
	errRegion = fn(region)
	regionOff = true
	defer func() { regionOff = false }()
	whole = mustParse(t, src)
	errWhole = fn(whole)
	return
}

// A region that starts after an indicator on its line is cut out with spaces in
// place of the indicator. That is only the same text when the indicator is a
// sequence dash; a "?" before the run must keep the run a key.
func TestRegionKeepsIndicatorBeforeRun(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 75; i++ {
		fmt.Fprintf(&b, "k%03d: 0\n", i)
	}
	b.WriteString("? #")
	src := b.String()
	d := mustParse(t, src)
	err := d.Set("/", " lead")
	if err != nil {
		if !bytes.Equal(d.Bytes(), []byte(src)) {
			t.Fatalf("refused edit changed the document")
		}
		return
	}
	var got map[string]any
	if err := Unmarshal(d.Bytes(), &got); err != nil {
		t.Fatalf("edited text does not decode: %v\n%q", err, d.Bytes())
	}
	if got[""] != " lead" {
		t.Fatalf("the key \"\" reads back %#v in %q", got[""], d.Bytes()[600:])
	}
	if err := sameAsFreshParse(d); err != nil {
		t.Fatal(err)
	}
}

func TestStandInOK(t *testing.T) {
	for in, want := range map[string]bool{
		"": true, "    ": true, "- ": true, "  - ": true, "- - ": true,
		"? ": false, ": ": false, "-": false, "a- ": false, "- #": false, "\t": false, "é ": false,
		"? # c ": false, "- ? ": false,
	} {
		if got := standInOK([]byte(in)); got != want {
			t.Errorf("standInOK(%q) = %v, want %v", in, got, want)
		}
	}
}

// An empty explicit key is placed by the parser at the next token, which can be a
// line below its "?". Edits beside such entries are written the way a whole parse
// writes them, or refused with the document as it was.
func TestRegionBesideEmptyKeys(t *testing.T) {
	srcs := []string{
		":\n?\nnew2: null",
		"?\n?\nk: 1\nm: 2\n",
		"? :\n?\nk: {a: 1}\nm: 2\n",
		"a: 1\n?\nb: 2\n?\n",
		"- ?\n  k: 1\n- z\n",
		"x:\n  ?\n  k: 1\n  m: 2\n",
	}
	ops := []struct {
		name string
		fn   func(d *Document) error
	}{
		{"set k", func(d *Document) error { return d.Set("/k", 5) }},
		{"set new", func(d *Document) error { return d.Set("/-", nil) }},
		{"set new key", func(d *Document) error { return d.Set("/zz", "v") }},
		{"remove k", func(d *Document) error { return d.Remove("/k") }},
		{"remove m", func(d *Document) error { return d.Remove("/m") }},
		{"set nested", func(d *Document) error { return d.Set("/x/m", []any{1, 2}) }},
	}
	for _, src := range srcs {
		for _, op := range ops {
			region, whole, errR, errW := editBoth(t, src, op.fn)
			if (errR == nil) != (errW == nil) {
				continue // a refusal in one path only is allowed; see below
			}
			if errR != nil {
				if !bytes.Equal(region.Bytes(), []byte(src)) {
					t.Errorf("%q %s: refused edit changed the document", src, op.name)
				}
				continue
			}
			if !bytes.Equal(region.Bytes(), whole.Bytes()) {
				t.Errorf("%q %s: region %q, whole %q", src, op.name, region.Bytes(), whole.Bytes())
			}
			if err := sameAsFreshParse(region); err != nil {
				t.Errorf("%q %s: %v", src, op.name, err)
			}
		}
	}
}

// Text written without a parse must be text a parse accepts. A scalar whose bytes
// are not valid UTF-8 is not written in place, and an edit that takes out the one
// character the scanner reports first does not expose what it hid.
func TestInvalidUTF8NeverCommitted(t *testing.T) {
	src := ": : '\x7f'\n0: 0\n1: \n1:"
	d := mustParse(t, src)
	steps := []func() error{
		func() error { return d.Set("/0", "\x7f\x8e") },
		func() error { return d.Set("/", map[string]any{}) },
		func() error { return d.Set("/0", "\x8e") },
	}
	for i, step := range steps {
		before := append([]byte(nil), d.Bytes()...)
		if err := step(); err != nil {
			if !bytes.Equal(before, d.Bytes()) {
				t.Fatalf("step %d refused but changed the document", i)
			}
			continue
		}
		if _, err := Parse(d.Bytes()); err != nil {
			t.Fatalf("step %d left text that does not parse: %v\n%q", i, err, d.Bytes())
		}
	}
}

func TestCleanText(t *testing.T) {
	for in, want := range map[string]bool{
		"abc": true, "a\tb": true, "é": true, "\u0085": true, "日本": true,
		"\x7f": false, "\x8e": false, "a\x00": false, "\u0080": false, "\ufeff": false, "\ufffe": false, "\xed\xa0\x80": false,
	} {
		if got := cleanText([]byte(in)); got != want {
			t.Errorf("cleanText(%q) = %v, want %v", in, got, want)
		}
	}
}

// Appending to a flow collection parses the new item and its neighbour, not the
// collection: n appends cost n small parses, not n parses of a growing line.
func TestFlowAppendsAreRegionEdits(t *testing.T) {
	const n = 3000
	noSpliceCheck(t)
	for _, tc := range []struct {
		name, head string
		set        func(i int) string
		open, shut string
	}{
		{"sequence", "a: 1\nflow: [x]\nb: 2\n", func(int) string { return "/flow/-" }, "[", "]"},
		{"mapping", "a: 1\nflow: {z: 0}\nb: 2\n", func(i int) string { return fmt.Sprintf("/flow/k%d", i) }, "{", "}"},
		{"root", "[x]\n", func(int) string { return "/-" }, "[", "]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := mustParse(t, tc.head)
			for i := 0; i < n; i++ {
				if err := d.Set(tc.set(i), i); err != nil {
					t.Fatalf("append %d: %v", i, err)
				}
			}
			w := d.s.ed.work
			// The first edits of a small collection may be the whole stream.
			if w.wholes > 4 || w.regions < n-4 {
				t.Fatalf("whole parses %d, region edits %d of %d", w.wholes, w.regions, n)
			}
			if w.bytes > 100*n {
				t.Fatalf("%d appends scanned %d bytes: not linear", n, w.bytes)
			}
			if err := sameAsFreshParse(d); err != nil {
				t.Fatal(err)
			}
			var got any
			if err := Unmarshal(d.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			text := string(d.Bytes())
			if !strings.Contains(text, fmt.Sprint(n-1)) {
				t.Fatalf("last item missing in %.80q", text)
			}
		})
	}
}

// Edits inside flow collections give the text and the tree a whole parse gives,
// whatever the layout around the items.
func TestFlowRegionMatchesWholeParse(t *testing.T) {
	docs := []string{
		"k: [a, b, c, d, e]\n",
		"k: [ a , b , c ]\n",
		"k: [a, [b, c], {d: e}, f]\nm: 1\n",
		"k: {a: 1, b: 2, c: 3, d: 4}\n",
		"k: {a: 1, b: [x, y], c: {d: e}, f}\nm: 1\n",
		"k: [a,\n  b, # note\n  c,\n  d]\nm: 1\n",
		"k: {a: 1,\n  b: 2, # note\n  c: 3}\n",
		"k: [a, b: c, ? d : e, f]\n",
		"k: [&x a, *x, !!str b, 'c', \"d\", e f]\n",
		"- [a, b, c]\n- {d: e, f: g}\n- [[1, 2], [3, 4]]\n",
		"[a, b, c, d]\n",
		"{a: 1, b: 2, c: 3}\n",
		"k: {? a : 1, ? b : 2, c}\n",
		"k: [a, b]\n" + strings.Repeat("pad: 1\n", 80),
	}
	ptrsOf := func(d *Document) []string {
		var x any
		if err := d.Get("", &x); err != nil {
			return nil
		}
		var out []string
		var walk func(p string, x any)
		walk = func(p string, x any) {
			out = append(out, p)
			switch x := x.(type) {
			case map[string]any:
				for k, v := range x {
					walk(p+"/"+k, v)
				}
			case []any:
				for i, v := range x {
					walk(fmt.Sprintf("%s/%d", p, i), v)
				}
			}
		}
		walk("", x)
		return out
	}
	vals := []any{"new", 7, []any{1, "two"}, map[string]any{"q": 1}, nil}
	n, viaRegion, viaFlow := 0, 0, 0
	old := spliceHook
	spliceHook = func(s *docStream, r *regionDebug) {
		if s.tree.Nodes[r.cn].Flow {
			viaFlow++
		}
		old(s, r)
	}
	defer func() { spliceHook = old }()
	for _, src := range docs {
		probe := mustParse(t, src)
		for _, p := range ptrsOf(probe) {
			for vi, val := range vals {
				for _, op := range []string{"set", "add", "remove", "append", "newkey"} {
					ptr := p
					switch op {
					case "append":
						ptr = p + "/-"
					case "newkey":
						ptr = p + "/nk"
					}
					fn := func(d *Document) error {
						switch op {
						case "remove":
							return d.Remove(ptr)
						case "add":
							return d.ApplyPatch([]byte(fmt.Sprintf(`[{"op":"add","path":%q,"value":%s}]`, ptr, jsonOf(val))))
						}
						return d.Set(ptr, val)
					}
					if op == "remove" && vi > 0 {
						continue
					}
					region, whole, errR, errW := editBoth(t, src, fn)
					if (errR == nil) != (errW == nil) {
						// The region path may refuse what the whole parse accepts, never the reverse.
						if errR == nil {
							t.Errorf("%q %s %q: region path edited, whole path refused: %v", src, op, ptr, errW)
						}
						continue
					}
					n++
					if region.s.ed != nil {
						viaRegion += region.s.ed.work.regions
					}
					if errR != nil {
						if !bytes.Equal(region.Bytes(), []byte(src)) {
							t.Errorf("%q %s %q: refused edit changed the document", src, op, ptr)
						}
						continue
					}
					if !bytes.Equal(region.Bytes(), whole.Bytes()) {
						t.Errorf("%q %s %q %v: region %q, whole %q", src, op, ptr, val, region.Bytes(), whole.Bytes())
					}
					if err := sameAsFreshParse(region); err != nil {
						t.Errorf("%q %s %q: %v", src, op, ptr, err)
					}
					if !reflect.DeepEqual(region.s.tree.Nodes, whole.s.tree.Nodes) {
						t.Errorf("%q %s %q: trees differ", src, op, ptr)
					}
				}
			}
		}
	}
	if n < 500 {
		t.Fatalf("only %d edits compared", n)
	}
	if viaFlow < 100 {
		t.Fatalf("only %d of the edits went through a flow region", viaFlow)
	}
	t.Logf("%d edits compared, %d through a region, %d of them in a flow collection", n, viaRegion, viaFlow)
}

func jsonOf(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case string:
		return fmt.Sprintf("%q", v)
	case int:
		return fmt.Sprint(v)
	case []any:
		parts := make([]string, len(v))
		for i, x := range v {
			parts[i] = jsonOf(x)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		return `{"q":1}`
	}
	return "null"
}

// A stream whose tree and text cannot be brought back together is not edited
// further, and the text is not touched.
func TestEditEngineLosesTreeWithoutPanic(t *testing.T) {
	noSpliceCheck(t)
	src := "a: 1\nb: 2\n"
	d := mustParse(t, src)
	// Break the text behind the tree's back, so that no parse of it succeeds.
	d.s.src = []byte("a: \x8e\nb: 2\n")
	err := d.Set("/c", 3)
	if err == nil {
		t.Fatal("edit of text that does not parse succeeded")
	}
	if string(d.Bytes()) != "a: \x8e\nb: 2\n" {
		t.Fatalf("text changed: %q", d.Bytes())
	}
	if err := d.Set("/d", 4); err == nil {
		t.Fatal("a lost document accepted an edit")
	}
}

func TestRestoreOfUnparseableSnapshotReturnsError(t *testing.T) {
	d := mustParse(t, "a: 1\n")
	before := append([]byte(nil), d.Bytes()...)
	err := d.s.restore(docSnapshot{src: []byte("a: \x8e\n")})
	if err == nil {
		t.Fatal("restore of text that does not parse reported success")
	}
	if !bytes.Equal(d.Bytes(), before) {
		t.Fatalf("failed restore changed the text: %q", d.Bytes())
	}
	if err := sameAsFreshParse(d); err != nil {
		t.Fatal(err)
	}
}

// Removing the entry of an empty key takes that entry and no other, wherever the
// next entry's key sits.
func TestRemoveEmptyKeyEntry(t *testing.T) {
	for _, src := range []string{
		" ?\n new0: null",
		" ?\n '-': null\n 'new0': {}",
		"?\nnew0: 1\nz: 2\n",
		"a: 1\n?\nb: 2\n",
	} {
		var before map[string]any
		if err := Unmarshal([]byte(src), &before); err != nil {
			t.Fatal(err)
		}
		for _, off := range []bool{false, true} {
			regionOff = off
			d := mustParse(t, src)
			err := d.Remove("/")
			regionOff = false
			if err != nil {
				t.Errorf("%q: %v", src, err)
				continue
			}
			var got map[string]any
			if err := Unmarshal(d.Bytes(), &got); err != nil {
				t.Errorf("%q: %v\n%q", src, err, d.Bytes())
				continue
			}
			want := map[string]any{}
			for k, v := range before {
				want[k] = v
			}
			delete(want, "")
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%q (regions off: %v): reads back %v, want %v; text %q", src, off, got, want, d.Bytes())
			}
		}
	}
}

// A value set on an explicit key that has none goes on a new line whose colon
// stands in the key's column, in a stream long enough to be edited by region and
// in one short enough to be parsed whole.
func TestValueOfEmptyKeyAtEndOfLargeMapping(t *testing.T) {
	for _, entries := range []int{75, 5} {
		var b strings.Builder
		for i := 0; i < entries; i++ {
			fmt.Fprintf(&b, "k%03d: 0\n", i)
		}
		head := b.String()
		src := head + "? #"
		want := head + "? #\n: ' lead'"
		for _, mode := range []string{"set", "patch"} {
			for _, off := range []bool{false, true} {
				regionOff = off
				d := mustParse(t, src)
				var err error
				if mode == "set" {
					err = d.Set("/", " lead")
				} else {
					err = d.ApplyPatch([]byte(`[{"op":"replace","path":"/","value":" lead"}]`))
				}
				regionOff = false
				if err != nil {
					t.Fatalf("%d entries, %s, regions off %v: %v", entries, mode, off, err)
				}
				if got := string(d.Bytes()); got != want {
					t.Errorf("%d entries, %s, regions off %v: got %q, want %q", entries, mode, off, got[len(head):], want[len(head):])
				}
			}
		}
	}
}

// The colon line for the value of an explicit key goes after the key's own lines,
// including a block scalar key that ends with its line break.
func TestValueOfBlockScalarKey(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"? |\n  a\nz: 1\n", "? |\n  a\n: v\nz: 1\n"},
		{"? |\n  a\n", "? |\n  a\n: v\n"},
		{"? k # c\nz: 1\n", "? k # c\n: v\nz: 1\n"},
	} {
		for _, off := range []bool{false, true} {
			regionOff = off
			d := mustParse(t, tc.src)
			var key any = "a\n"
			if strings.HasPrefix(tc.src, "? k") {
				key = "k"
			}
			err := d.Set("/"+key.(string), "v")
			regionOff = false
			if err != nil {
				t.Errorf("%q: %v", tc.src, err)
				continue
			}
			if got := string(d.Bytes()); got != tc.want {
				t.Errorf("%q (regions off %v): got %q, want %q", tc.src, off, got, tc.want)
			}
		}
	}
}

// A single pair in a flow collection ends where the next token starts, white space
// before it included. When an edit leaves white space after the pair that was not
// part of the edited items, the pair still ends at the next token.
func TestFlowPairEndsAtNextTokenAfterEdit(t *testing.T) {
	for _, tc := range []struct {
		name, src, ptr string
	}{
		{"remove the item after an empty pair", ": [: ,0 ]", "//1"},
		{"remove the item after a pair", "k: [a: b ,0  ]\n", "/k/1"},
		{"remove the last of two pairs", "k: [a: b, c: d , 0 ]\n", "/k/2"},
		{"remove the item after a pair, then more", "k: [a: b ,0 ] # c\nz: 1\n", "/k/1"},
	} {
		for _, off := range []bool{false, true} {
			regionOff = off
			d := mustParse(t, tc.src)
			err := d.Remove(tc.ptr)
			regionOff = false
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
				continue
			}
			if err := sameAsFreshParse(d); err != nil {
				t.Errorf("%s (regions off %v): %v", tc.name, off, err)
			}
		}
	}
}
