package oneyaml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// decodeInputs returns the test inputs: the kit's own cases that hold a plain
// YAML stream and, when ONEYAML_YTS_DIR is set, every yaml-test-suite input.
func decodeInputs(t testing.TB) (names []string, data [][]byte) {
	add := func(name, path string) {
		if b, err := os.ReadFile(path); err == nil && len(b) < 64<<10 {
			names, data = append(names, name), append(data, b)
		}
	}
	if kit := os.Getenv("ONEYAML_KIT"); kit != "" {
		for _, cat := range []string{"parse", "schema", "roundtrip", "edit", "diagnostics", "safety"} {
			m, _ := filepath.Glob(filepath.Join(kit, "suites", "own", "cases", cat, "*", "input*"))
			for _, p := range m {
				if strings.Contains(p, "deep-") || strings.Contains(p, "wide-") || strings.Contains(p, "long-") || strings.Contains(p, "bomb") {
					continue
				}
				add(strings.TrimPrefix(p, kit), p)
			}
		}
	}
	if dir := os.Getenv("ONEYAML_YTS_DIR"); dir != "" {
		for _, pat := range []string{"*/in.yaml", "*/*/in.yaml"} {
			m, _ := filepath.Glob(filepath.Join(dir, pat))
			for _, p := range m {
				add(strings.TrimPrefix(p, dir), p)
			}
		}
	}
	if len(names) == 0 {
		t.Skip("ONEYAML_KIT and ONEYAML_YTS_DIR are not set")
	}
	return
}

// readDocs decodes every document of a stream with the plain decoder.
func readDocs(b []byte) (docs []string, err error) {
	dec := NewDecoder(bytes.NewReader(b))
	for {
		var v any
		if err = dec.Decode(&v); err != nil {
			if err == io.EOF {
				err = nil
			}
			return
		}
		docs = append(docs, fmt.Sprintf("%#v", v))
	}
}

// checkEquivalent compares Document.Decode and Node.Decode of every document with
// the plain decoder on the document's current bytes: the whole value, or both an
// error.
func checkEquivalent(t *testing.T, name string, d *Document, step string) bool {
	want, werr := readDocs(d.Bytes())
	for i := 0; i < d.Len(); i++ {
		var got, viaNode any
		gerr := d.Doc(i).Decode(&got)
		nerr := d.Doc(i).Root().Decode(&viaNode)
		if (gerr == nil) != (nerr == nil) || (gerr == nil && fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", viaNode)) {
			t.Errorf("%s %s doc %d: Decode and Root().Decode differ: %v / %v", name, step, i, gerr, nerr)
			return false
		}
		if i < len(want) {
			if gerr != nil {
				t.Errorf("%s %s doc %d: Decode failed (%v) but the plain decoder read it", name, step, i, gerr)
				return false
			}
			if g := fmt.Sprintf("%#v", got); g != want[i] {
				t.Errorf("%s %s doc %d:\n got %s\nwant %s", name, step, i, g, want[i])
				return false
			}
		} else if gerr == nil && werr == nil {
			t.Errorf("%s %s doc %d: Decode read a document the plain decoder did not see", name, step, i)
			return false
		}
	}
	if werr == nil && len(want) != d.Len() {
		t.Errorf("%s %s: %d documents, plain decoder read %d", name, step, d.Len(), len(want))
		return false
	}
	return true
}

// pointers lists the RFC 6901 pointers of the values inside v.
func valuePointers(v any, prefix string, out *[]string) {
	*out = append(*out, prefix)
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			valuePointers(e, prefix+"/"+strings.NewReplacer("~", "~0", "/", "~1").Replace(k), out)
		}
	case []any:
		for i, e := range x {
			valuePointers(e, prefix+"/"+strconv.Itoa(i), out)
		}
	}
}

func TestDecodeEquivalence(t *testing.T) {
	names, data := decodeInputs(t)
	rng := rand.New(rand.NewSource(7))
	replacements := []any{"x", "two words", "multi\nline\n", 12, -3, true, nil, 2.5, "", "- tricky: [colon]",
		[]any{1, "a"}, map[string]any{"k": "v", "n": []any{1}}}
	parsed, edited := 0, 0
	for i, in := range data {
		d, err := Parse(in)
		if err != nil {
			continue
		}
		parsed++
		if !checkEquivalent(t, names[i], d, "as parsed") {
			continue
		}
		if testing.Short() && i%4 != 0 {
			continue
		}
		// Edit random places of the first document, then compare again.
		var v any
		if d.Len() == 0 || d.Doc(0).Decode(&v) != nil {
			continue
		}
		var ptrs []string
		valuePointers(v, "", &ptrs)
		sort.Strings(ptrs)
		for k := 0; k < 4 && len(ptrs) > 0; k++ {
			p := ptrs[rng.Intn(len(ptrs))]
			if p == "" {
				continue
			}
			if err := d.Set(p, replacements[rng.Intn(len(replacements))]); err != nil {
				continue
			}
			edited++
			if !checkEquivalent(t, names[i], d, fmt.Sprintf("after Set(%q)", p)) {
				break
			}
			// The edited stream must still parse to the same thing.
			d2, err := Parse(d.Bytes())
			if err != nil {
				t.Errorf("%s: edited text does not parse: %v", names[i], err)
				break
			}
			var a, b any
			if e1, e2 := d.Decode(&a), d2.Decode(&b); (e1 == nil) != (e2 == nil) || fmt.Sprintf("%#v", a) != fmt.Sprintf("%#v", b) {
				t.Errorf("%s: edited document and its re-parse decode differently", names[i])
				break
			}
			var ps []string
			var cur any
			if d.Doc(0).Decode(&cur) != nil {
				break
			}
			valuePointers(cur, "", &ps)
			sort.Strings(ps)
			ptrs = ps
		}
	}
	t.Logf("inputs %d, parsed %d, edits compared %d", len(data), parsed, edited)
}

// Node.Decode works on every node of every input, and an alias decodes to what it
// points at.
func TestNodeDecodeEveryNode(t *testing.T) {
	names, data := decodeInputs(t)
	checked := 0
	for i, in := range data {
		d, err := Parse(in)
		if err != nil || d.Len() == 0 {
			continue
		}
		var walk func(n Node, depth int)
		walk = func(n Node, depth int) {
			if depth > 50 || !n.IsValid() {
				return
			}
			var got any
			err := n.Decode(&got)
			if n.Kind() == AliasNode {
				var viaTarget any
				if tg := n.Target(); tg.IsValid() && err == nil && tg.Decode(&viaTarget) == nil &&
					fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", viaTarget) {
					t.Errorf("%s: alias *%s decodes to %#v, its target to %#v", names[i], n.AliasName(), got, viaTarget)
				}
			}
			checked++
			switch n.Kind() {
			case SequenceNode:
				for k := 0; k < n.Len(); k++ {
					walk(n.Item(k), depth+1)
				}
			case MappingNode:
				for k := 0; k < n.Len(); k++ {
					key, val := n.Entry(k)
					walk(key, depth+1)
					walk(val, depth+1)
				}
			}
		}
		walk(d.Root(), 0)
	}
	t.Logf("nodes decoded: %d", checked)
}

func TestDecodeErrors(t *testing.T) {
	d, err := Parse([]byte("a: 1\n---\nb: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	var te *TypeError
	var s struct{ A int }
	if err := d.Decode(1); !errors.As(err, &te) {
		t.Errorf("non-pointer: %v", err)
	}
	if err := d.Doc(5).Decode(&s); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing doc: %v", err)
	}
	empty, _ := Parse(nil)
	s.A = 9
	if err := empty.Decode(&s); err != nil || s.A != 9 {
		t.Errorf("empty stream: %v %d", err, s.A)
	}
	if err := d.Doc(1).Decode(&s); err != nil {
		t.Errorf("second document: %v", err)
	}
	n := d.Root()
	if err := d.Set("/a", 5); err != nil {
		t.Fatal(err)
	}
	if err := n.Decode(&s); !errors.Is(err, ErrStaleNode) {
		t.Errorf("stale node: %v", err)
	}
}

func TestDecodeSharesLimitsAndPaths(t *testing.T) {
	d, err := Parse([]byte("a: &a [x, x, x, x]\nb: &b [*a, *a, *a, *a]\nc: [*b, *b, *b, *b]\n"),
		Limits(LimitSet{MaxAliasNodes: 20}))
	if err != nil {
		t.Fatal(err)
	}
	var v any
	var le *LimitError
	if err := d.Decode(&v); !errors.As(err, &le) {
		t.Errorf("alias budget: %v", err)
	}
	d, _ = Parse([]byte("list:\n- n: 1\n- n: x\n"))
	var cfg struct {
		List []struct{ N int } `yaml:"list"`
	}
	var te *TypeError
	err = d.Decode(&cfg)
	if !errors.As(err, &te) || te.Path != "/list/1/n" || te.Line != 3 || te.Column != 6 {
		t.Errorf("Decode error = %#v", err)
	}
	err = d.Get("/list", &cfg.List)
	if !errors.As(err, &te) || te.Path != "/list/1/n" {
		t.Errorf("Get error path = %#v", err)
	}
	var item struct{ N int }
	err = d.Get("/list/1", &item)
	if !errors.As(err, &te) || te.Path != "/list/1/n" {
		t.Errorf("Get item error path = %#v", err)
	}
}

func TestNodeDecodeOutsideAnchors(t *testing.T) {
	d, err := Parse([]byte("base: &b {x: 1, y: [1, 2]}\nmore: &m [*b, 3]\nitem:\n  use: *b\n  list: *m\n"))
	if err != nil {
		t.Fatal(err)
	}
	var got any
	item, _ := d.Root().Lookup("item")
	if err := item.Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := `map[string]interface {}{"list":[]interface {}{map[string]interface {}{"x":1, "y":[]interface {}{1, 2}}, 3}, "use":map[string]interface {}{"x":1, "y":[]interface {}{1, 2}}}`
	if g := fmt.Sprintf("%#v", got); g != want {
		t.Errorf("got %s", g)
	}
	var typed struct {
		X int   `yaml:"x"`
		Y []int `yaml:"y"`
	}
	if err := d.Get("/item/use", &typed); err != nil || typed.X != 1 || len(typed.Y) != 2 {
		t.Errorf("Get through alias: %v %+v", err, typed)
	}
}

func TestTagsPerDocument(t *testing.T) {
	src := "%TAG !e! tag:example.com,2000:\n--- !e!foo bar\n...\n%TAG !e! tag:other.org,2001:\n--- !e!foo baz\n"
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Doc(0).Root().Tag(); got != "tag:example.com,2000:foo" {
		t.Errorf("doc 0 tag = %q", got)
	}
	if got := d.Doc(1).Root().Tag(); got != "tag:other.org,2001:foo" {
		t.Errorf("doc 1 tag = %q", got)
	}
	var v any
	if err := d.Doc(1).Decode(&v); err != nil || v != "baz" {
		t.Errorf("decode = %v %v", v, err)
	}
	d, err = Parse([]byte("%TAG !e! tag:example.com,2000:\n--- !!str a\n...\n--- b\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Doc(1).Root().Tag(); got != "tag:yaml.org,2002:str" {
		t.Errorf("tag = %q", got)
	}
}

// The non-specific tag "!" makes a scalar a string, and Node and Decode agree
// (YAML 1.2.2 section 6.9.1).
func TestNonSpecificTag(t *testing.T) {
	d, err := Parse([]byte("- ! 12\n- ! [a]\n- !!int 12\n- 12\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := d.Root()
	want := []string{"tag:yaml.org,2002:str", "tag:yaml.org,2002:seq", "tag:yaml.org,2002:int", "tag:yaml.org,2002:int"}
	for i, w := range want {
		if got := r.Item(i).Tag(); got != w {
			t.Errorf("item %d tag = %q, want %q", i, got, w)
		}
	}
	var v []any
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v[0] != "12" || v[2] != 12 || v[3] != 12 {
		t.Errorf("decoded %#v", v)
	}
}

func TestSuiteNonSpecificTags(t *testing.T) {
	dir := os.Getenv("ONEYAML_YTS_DIR")
	if dir == "" {
		t.Skip("ONEYAML_YTS_DIR is not set")
	}
	for _, id := range []string{"S4JQ", "2G84/02", "2G84/03", "UKK6/00", "UKK6/01", "UKK6/02"} {
		in, err := os.ReadFile(filepath.Join(dir, id, "in.yaml"))
		if err != nil {
			continue
		}
		d, err := Parse(in)
		var u any
		uerr := Unmarshal(in, &u)
		if (err == nil) != (uerr == nil) {
			t.Errorf("%s: Parse %v, Unmarshal %v", id, err, uerr)
			continue
		}
		if err != nil {
			continue
		}
		var v any
		if err := d.Decode(&v); err != nil || fmt.Sprintf("%#v", v) != fmt.Sprintf("%#v", u) {
			t.Errorf("%s: Decode %#v (%v), Unmarshal %#v", id, v, err, u)
		}
		// A scalar node reports the string tag exactly when it decodes to a string.
		var walk func(n Node)
		walk = func(n Node) {
			if !n.IsValid() {
				return
			}
			if n.Kind() == ScalarNode {
				var x any
				if n.Decode(&x) == nil {
					_, isStr := x.(string)
					if isStr != (n.Tag() == "tag:yaml.org,2002:str") {
						t.Errorf("%s: node %q decodes to %#v but has tag %s", id, n.Source(), x, n.Tag())
					}
				}
			}
			switch n.Kind() {
			case MappingNode:
				for k := 0; k < n.Len(); k++ {
					key, val := n.Entry(k)
					walk(key)
					walk(val)
				}
			case SequenceNode:
				for k := 0; k < n.Len(); k++ {
					walk(n.Item(k))
				}
			}
		}
		walk(d.Root())
	}
}
