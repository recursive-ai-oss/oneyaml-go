package oneyaml

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/pointer"
)

// decoded loads text through the decoder, the consumer that reads values without a
// tree, and returns the value of the first document.
func decoded(t testing.TB, text string) any {
	t.Helper()
	var v any
	if err := Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("decoder: %v\n%s", err, text)
	}
	return v
}

// modelRename renames a key in a value tree by plain surgery.
func modelRename(root edit.Value, ptr, newKey string) (edit.Value, error) {
	toks, err := pointer.Parse(ptr)
	if err != nil || len(toks) == 0 {
		return root, fmt.Errorf("bad pointer")
	}
	cur := &root
	for _, tok := range toks[:len(toks)-1] {
		next, err := modelStep(cur, tok)
		if err != nil {
			return root, err
		}
		cur = next
	}
	last := toks[len(toks)-1]
	if cur.Kind != edit.Map {
		return root, fmt.Errorf("not a mapping")
	}
	for i := 0; i < len(cur.Items); i += 2 {
		if cur.Items[i].Text == last && isScalarKey(cur.Items[i]) {
			cur.Items[i] = edit.Value{Kind: edit.String, Text: newKey}
			return root, nil
		}
	}
	return root, fmt.Errorf("no key")
}

// sameValues reports whether two documents hold the same values in every document
// of the stream, ignoring number spelling.
func sameValues(a, b *Document) error {
	va, err := streamValues(a)
	if err != nil {
		return err
	}
	vb, err := streamValues(b)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(canonAll(va), canonAll(vb)) {
		return fmt.Errorf("values differ\n before: %+v\n  after: %+v", canonAll(va), canonAll(vb))
	}
	return nil
}

func TestRename(t *testing.T) {
	for _, c := range []struct{ name, in, ptr, key, want string }{
		{"keeps value, position and comments", "# chart\nimage: nginx\nreplicas: 2 # scaled\n# above port\nport: 80\n", "/replicas", "replicaCount", "# chart\nimage: nginx\nreplicaCount: 2 # scaled\n# above port\nport: 80\n"},
		{"nested", "a:\n  b: 1\n  c: 2\n", "/a/b", "z", "a:\n  z: 1\n  c: 2\n"},
		{"collection value", "a:\n  - 1\n  - 2\nb: 1\n", "/a", "list", "list:\n  - 1\n  - 2\nb: 1\n"},
		{"single-quoted key stays quoted", "'a b': 1\n", "/a b", "c d", "'c d': 1\n"},
		{"double-quoted key stays quoted", "\"a\": 1\n", "/a", "b", "\"b\": 1\n"},
		{"plain key that needs quotes", "a: 1\n", "/a", "x: y", "'x: y': 1\n"},
		{"plain key that reads as a number", "a: 1\n", "/a", "2", "'2': 1\n"},
		{"plain key that reads as null", "a: 1\n", "/a", "null", "'null': 1\n"},
		{"empty new key", "a: 1\n", "/a", "", "'': 1\n"},
		{"line break in the new key", "a: 1\n", "/a", "x\ny", "\"x\\ny\": 1\n"},
		{"single quote in a quoted key", "'a': 1\n", "/a", "it's", "'it''s': 1\n"},
		{"anchor on the key stays", "&k a: 1\nb: 2\n", "/a", "c", "&k c: 1\nb: 2\n"},
		{"alias value", "x: &v 1\na: *v\n", "/a", "b", "x: &v 1\nb: *v\n"},
		{"explicit key", "? a\n: 1\n", "/a", "b", "? b\n: 1\n"},
		{"flow mapping", "{a: 1, b: 2}\n", "/a", "c", "{c: 1, b: 2}\n"},
		{"flow quoted key before a colon", "{\"a\":1}\n", "/a", "b", "{\"b\":1}\n"},
		{"flow key with a comma", "{a: 1}\n", "/a", "x,y", "{'x,y': 1}\n"},
		{"sequence of mappings", "- a: 1\n  b: 2\n- a: 3\n", "/0/a", "z", "- z: 1\n  b: 2\n- a: 3\n"},
		{"crlf", "a: 1\r\nb: 2\r\n", "/b", "c", "a: 1\r\nc: 2\r\n"},
		{"same key is no change", "a: 1\n", "/a", "a", "a: 1\n"},
		{"unicode", "a: 1\n", "/a", "ключ", "ключ: 1\n"},
		{"literal value untouched", "a: |\n  text\nb: 1\n", "/a", "c", "c: |\n  text\nb: 1\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Parse([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			before, _ := streamValues(d)
			if err := d.Rename(c.ptr, c.key); err != nil {
				t.Fatal(err)
			}
			if got := d.String(); got != c.want {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
			re, err := Parse(d.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			after, _ := streamValues(re)
			want, err := modelRename(cloneValue(before[0]), c.ptr, c.key)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(canonAll(after), canonAll([]edit.Value{want})) {
				t.Errorf("read back differs\n got: %+v\nwant: %+v", canonAll(after), canonAll([]edit.Value{want}))
			}
			// The decoder reads the new key and not the old one.
			if m, ok := decoded(t, d.String()).(map[string]any); ok && c.key != "a" {
				if _, has := m[c.key]; !has && c.ptr == "/"+strings.TrimPrefix(c.ptr, "/") && strings.Count(c.ptr, "/") == 1 {
					t.Errorf("decoder does not see key %q: %v", c.key, m)
				}
			}
		})
	}
}

func TestRenameGetsOldValue(t *testing.T) {
	d, _ := Parse([]byte("a:\n  b: [1, 2]\n  c: 3\n"))
	if err := d.Rename("/a/b", "bb"); err != nil {
		t.Fatal(err)
	}
	var v []int
	if err := d.Get("/a/bb", &v); err != nil || !reflect.DeepEqual(v, []int{1, 2}) {
		t.Errorf("Get /a/bb = %v, %v", v, err)
	}
	if err := d.Get("/a/b", &v); !errors.Is(err, ErrNotFound) {
		t.Errorf("old key still there: %v", err)
	}
}

func TestRenameRefusals(t *testing.T) {
	for _, c := range []struct {
		name, in, ptr, key string
		kind               error
	}{
		{"key exists", "a: 1\nb: 2\n", "/a", "b", ErrKeyExists},
		{"key exists as quoted", "a: 1\n'b': 2\n", "/a", "b", ErrKeyExists},
		{"key exists twice", "a: 1\nb: 2\nb: 3\n", "/a", "b", ErrKeyExists},
		{"sequence item", "- a\n- b\n", "/0", "x", ErrNotAnEntry},
		{"root", "a: 1\n", "", "x", ErrNotAnEntry},
		{"missing", "a: 1\n", "/zz", "x", ErrNotFound},
		{"missing parent", "a: 1\n", "/zz/a", "x", ErrNotFound},
		{"through an alias", "x: &v\n  k: 1\na: *v\n", "/a/k", "z", ErrAliasWrite},
		{"ambiguous key", "a: 1\na: 2\n", "/a", "b", ErrAmbiguousKey},
		{"bad pointer", "a: 1\n", "a", "x", ErrInvalidPointer},
		{"merge key", "b: &b {x: 1}\na:\n  <<: *b\n", "/a/<<", "m", ErrUnsupportedEdit},
		{"empty key", ": 1\n", "/", "x", ErrUnsupportedEdit},
		{"block scalar key", "? |\n  a\n: 1\n", "/a\n", "x", ErrUnsupportedEdit},
		{"tag that does not fit", "!!int 1: a\n", "/1", "x", ErrUnsupportedEdit},
		{"invalid UTF-8", "a: 1\n", "/a", "\xff", ErrUnsupportedEdit},
		{"long key", "a: 1\n", "/a", strings.Repeat("k", 1001), ErrUnsupportedEdit},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Parse([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			err = d.Rename(c.ptr, c.key)
			if !errors.Is(err, c.kind) {
				t.Fatalf("error %v, want kind %v", err, c.kind)
			}
			var ee *EditError
			if !errors.As(err, &ee) || ee.Op != "rename" {
				t.Errorf("not an EditError for rename: %#v", err)
			}
			if d.String() != c.in {
				t.Errorf("document changed: %q", d.String())
			}
		})
	}
	var nd *Document
	if err := nd.Rename("/a", "b"); !errors.Is(err, ErrNilDocument) {
		t.Errorf("nil document: %v", err)
	}
}

func TestRenameSecondDocument(t *testing.T) {
	d, _ := Parse([]byte("a: 1\n---\na: 2\nb: 3\n"))
	if err := d.Doc(1).Rename("/a", "c"); err != nil {
		t.Fatal(err)
	}
	if got, want := d.String(), "a: 1\n---\nc: 2\nb: 3\n"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if err := d.Rename("/c", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("first document has no /c: %v", err)
	}
	if err := d.Doc(5).Rename("/a", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("no document 5: %v", err)
	}
}

func TestRenameStalesNodes(t *testing.T) {
	d, _ := Parse([]byte("a: 1\nb: 2\n"))
	n, _ := d.Root().Lookup("b")
	if err := d.Rename("/a", "c"); err != nil {
		t.Fatal(err)
	}
	if !n.Stale() {
		t.Error("a Node read before the rename is still valid")
	}
	if v, ok := d.Root().Lookup("c"); !ok || v.Value() != "1" {
		t.Errorf("new key: %v %v", v.Value(), ok)
	}
}

func TestSetHeadComment(t *testing.T) {
	for _, c := range []struct{ name, in, ptr, text, want string }{
		{"adds above a mapping entry", "a: 1\nb: 2\n", "/b", "second", "a: 1\n# second\nb: 2\n"},
		{"two lines", "a: 1\n", "/a", "one\ntwo", "# one\n# two\na: 1\n"},
		{"hash kept", "a: 1\n", "/a", "# one\n#two", "# one\n#two\na: 1\n"},
		{"blank line inside becomes a bare hash", "a: 1\n", "/a", "one\n\ntwo", "# one\n#\n# two\na: 1\n"},
		{"trailing newline dropped", "a: 1\n", "/a", "one\n", "# one\na: 1\n"},
		{"replaces the old comment at the entry's indentation", "x:\n      # old\n      # older\n  a: 1\n", "/x/a", "new", "x:\n  # new\n  a: 1\n"},
		{"removes", "x:\n  # old\n  a: 1\n  b: 2\n", "/x/a", "", "x:\n  a: 1\n  b: 2\n"},
		{"removing nothing changes nothing", "a: 1\n", "/a", "", "a: 1\n"},
		{"same text changes nothing", "# one\na: 1\n", "/a", "one", "# one\na: 1\n"},
		{"blank line above stays", "a: 1\n\nb: 2\n", "/b", "c", "a: 1\n\n# c\nb: 2\n"},
		{"sequence item", "- a\n- b\n", "/1", "second", "- a\n# second\n- b\n"},
		{"nested item indentation", "k:\n  - a\n  - b\n", "/k/1", "second", "k:\n  - a\n  # second\n  - b\n"},
		{"after a block scalar", "a: |\n  text\nb: 1\n", "/b", "c", "a: |\n  text\n# c\nb: 1\n"},
		{"first mapping inside an item", "- a: 1\n  b: 2\n", "/0", "item", "# item\n- a: 1\n  b: 2\n"},
		{"second key inside an item", "- a: 1\n  b: 2\n", "/0/b", "ok", "- a: 1\n  # ok\n  b: 2\n"},
		{"collection value goes above its key", "a:\n  b: 1\n", "/a", "the a", "# the a\na:\n  b: 1\n"},
		{"root", "a: 1\n", "", "top", "# top\na: 1\n"},
		{"root after a marker", "--- # m\na: 1\n", "", "top", "--- # m\n# top\na: 1\n"},
		{"crlf", "a: 1\r\nb: 2\r\n", "/b", "c", "a: 1\r\n# c\r\nb: 2\r\n"},
		{"explicit key", "? a\n: 1\n", "/a", "c", "# c\n? a\n: 1\n"},
		{"unicode", "a: 1\n", "/a", "привет", "# привет\na: 1\n"},
		{"spaces only removes", "# c\na: 1\n", "/a", "  ", "a: 1\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Parse([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if err := d.SetHeadComment(c.ptr, c.text); err != nil {
				t.Fatal(err)
			}
			if got := d.String(); got != c.want {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
			re, err := Parse(d.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			orig, _ := Parse([]byte(c.in))
			if err := sameValues(orig, re); err != nil {
				t.Error(err)
			}
			if !reflect.DeepEqual(decoded(t, c.in), decoded(t, d.String())) {
				t.Error("the decoder reads other values after the edit")
			}
			// Reading the comment back gives the text that was set.
			toks, _ := pointer.Parse(c.ptr)
			n := re.Root()
			for _, tok := range toks {
				if n.Kind() == SequenceNode {
					var i int
					fmt.Sscan(tok, &i)
					n = n.Item(i)
				} else {
					n, _ = n.Lookup(tok)
				}
			}
			want := strings.Join(mustLines(t, c.text), "\n")
			if got := n.HeadComment(); got != want {
				t.Errorf("HeadComment() = %q, want %q", got, want)
			}
		})
	}
}

func mustLines(t *testing.T, text string) []string {
	t.Helper()
	l, err := edit.CommentLines(text)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestSetLineComment(t *testing.T) {
	for _, c := range []struct{ name, in, ptr, text, want string }{
		{"adds after a scalar", "a: 1\nb: 2\n", "/a", "one", "a: 1 # one\nb: 2\n"},
		{"hash kept", "a: 1\n", "/a", "# one", "a: 1 # one\n"},
		{"replaces", "a: 1   # old stuff\n", "/a", "new", "a: 1   # new\n"},
		{"removes with its blanks", "a: 1   # old\nb: 2\n", "/a", "", "a: 1\nb: 2\n"},
		{"removing nothing changes nothing", "a: 1\n", "/a", "", "a: 1\n"},
		{"trailing blanks go", "a: 1   \nb: 2\n", "/a", "c", "a: 1 # c\nb: 2\n"},
		{"after a quoted scalar", "a: 'x y'\n", "/a", "c", "a: 'x y' # c\n"},
		{"no final line break", "a: 1", "/a", "c", "a: 1 # c"},
		{"after the header of a block scalar", "a: |\n  text\nb: 2\n", "/a", "blk", "a: | # blk\n  text\nb: 2\n"},
		{"after a header with indicators", "a: >-2\n   text\nb: 2\n", "/a", "blk", "a: >-2 # blk\n   text\nb: 2\n"},
		{"replaces a header comment", "a: | # old\n  text\n", "/a", "new", "a: | # new\n  text\n"},
		{"after the colon of a block collection", "a:\n  b: 1\n", "/a", "coll", "a: # coll\n  b: 1\n"},
		{"after the colon, properties first", "a: &x\n  b: 1\n", "/a", "coll", "a: &x # coll\n  b: 1\n"},
		{"after the dash of a block collection", "-\n  a: 1\n", "/0", "item", "- # item\n  a: 1\n"},
		{"empty value", "a:\nb: 1\n", "/a", "empty", "a: # empty\nb: 1\n"},
		{"empty item", "-\n- x\n", "/0", "empty", "- # empty\n- x\n"},
		{"after a flow collection", "a: [1, 2]\n", "/a", "flow", "a: [1, 2] # flow\n"},
		{"after a multi-line flow collection", "a: [1,\n  2]\n", "/a", "flow", "a: [1,\n  2] # flow\n"},
		{"after an alias", "x: &v 1\na: *v\n", "/a", "al", "x: &v 1\na: *v # al\n"},
		{"root scalar", "text\n", "", "c", "text # c\n"},
		{"root flow", "[1, 2]\n", "", "c", "[1, 2] # c\n"},
		{"sequence item", "- a\n- b\n", "/1", "c", "- a\n- b # c\n"},
		{"crlf", "a: 1\r\nb: 2\r\n", "/a", "c", "a: 1 # c\r\nb: 2\r\n"},
		{"multi-line plain scalar", "a: one\n  two\nb: 1\n", "/a", "c", "a: one\n  two # c\nb: 1\n"},
		{"unicode", "a: 1\n", "/a", "привет", "a: 1 # привет\n"},
		{"spaces only removes", "a: 1 # c\n", "/a", "  ", "a: 1\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Parse([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if err := d.SetLineComment(c.ptr, c.text); err != nil {
				t.Fatal(err)
			}
			if got := d.String(); got != c.want {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
			re, err := Parse(d.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			orig, _ := Parse([]byte(c.in))
			if err := sameValues(orig, re); err != nil {
				t.Error(err)
			}
			if !reflect.DeepEqual(decoded(t, c.in), decoded(t, d.String())) {
				t.Error("the decoder reads other values after the edit")
			}
			toks, _ := pointer.Parse(c.ptr)
			n := re.Root()
			for _, tok := range toks {
				if n.Kind() == SequenceNode {
					var i int
					fmt.Sscan(tok, &i)
					n = n.Item(i)
				} else {
					n, _ = n.Lookup(tok)
				}
			}
			want := ""
			if strings.TrimSpace(c.text) != "" {
				want = mustLines(t, c.text)[0]
			}
			if got := n.LineComment(); got != want {
				t.Errorf("LineComment() = %q, want %q", got, want)
			}
		})
	}
}

func TestCommentRefusals(t *testing.T) {
	for _, c := range []struct {
		name, in, op, ptr, text string
		kind                    error
	}{
		{"line comment with a line break", "a: 1\n", "line", "/a", "x\ny", ErrInvalidComment},
		{"line comment with a carriage return", "a: 1\n", "line", "/a", "x\ry", ErrInvalidComment},
		{"line comment with a separator", "a: 1\n", "line", "/a", "x\u2028y", ErrInvalidComment},
		{"line comment with a control character", "a: 1\n", "line", "/a", "x\x01y", ErrInvalidComment},
		{"line comment, invalid UTF-8", "a: 1\n", "line", "/a", "x\xffy", ErrInvalidComment},
		{"head comment with a separator", "a: 1\n", "head", "/a", "x\u0085y", ErrInvalidComment},
		{"head comment with a control character", "a: 1\n", "head", "/a", "x\x00y", ErrInvalidComment},
		{"head comment, invalid UTF-8", "a: 1\n", "head", "/a", "\xff", ErrInvalidComment},
		{"line comment on a missing path", "a: 1\n", "line", "/b", "x", ErrNotFound},
		{"head comment on a missing path", "a: 1\n", "head", "/b", "x", ErrNotFound},
		{"line comment through an alias", "x: &v\n  k: 1\na: *v\n", "line", "/a/k", "x", ErrAliasWrite},
		{"head comment through an alias", "x: &v\n  k: 1\na: *v\n", "head", "/a/k", "x", ErrAliasWrite},
		{"bad pointer", "a: 1\n", "line", "a", "x", ErrInvalidPointer},
		{"line comment inside a flow collection", "a: [1, 2]\n", "line", "/a/0", "x", ErrUnsupportedEdit},
		{"head comment inside a flow collection", "a: [1, 2]\n", "head", "/a/0", "x", ErrUnsupportedEdit},
		{"head comment of an entry that shares a line", "- a: 1\n  b: 2\n", "head", "/0/a", "x", ErrUnsupportedEdit},
		{"line comment of the root block mapping", "a: 1\n", "line", "", "x", ErrUnsupportedEdit},
		{"line comment of a block collection that starts on the line", "- a: 1\n", "line", "/0", "x", ErrUnsupportedEdit},
		{"line comment of an empty document", "", "line", "", "x", ErrNotFound},
		{"head comment of the root after a marker with content", "--- a: 1\n", "head", "", "x", ErrUnsupportedEdit},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Parse([]byte(c.in))
			if err != nil {
				t.Skipf("input does not parse: %v", err)
			}
			if c.op == "line" {
				err = d.SetLineComment(c.ptr, c.text)
			} else {
				err = d.SetHeadComment(c.ptr, c.text)
			}
			if !errors.Is(err, c.kind) {
				t.Fatalf("error %v, want kind %v", err, c.kind)
			}
			if d.String() != c.in {
				t.Errorf("document changed: %q", d.String())
			}
		})
	}
	var nd *Document
	if err := nd.SetHeadComment("/a", "x"); !errors.Is(err, ErrNilDocument) {
		t.Errorf("nil document: %v", err)
	}
	if err := nd.SetLineComment("/a", "x"); !errors.Is(err, ErrNilDocument) {
		t.Errorf("nil document: %v", err)
	}
}

func TestCommentEditsInSecondDocument(t *testing.T) {
	d, _ := Parse([]byte("a: 1\n---\na: 2\n"))
	if err := d.Doc(1).SetLineComment("/a", "two"); err != nil {
		t.Fatal(err)
	}
	if err := d.Doc(1).SetHeadComment("/a", "above"); err != nil {
		t.Fatal(err)
	}
	if got, want := d.String(), "a: 1\n---\n# above\na: 2 # two\n"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestSetThenRemoveLineCommentRestores(t *testing.T) {
	in := "a: 1\nb: |\n  x\nc:\n  d: [1, 2]\ne:\n"
	d, _ := Parse([]byte(in))
	for _, p := range []string{"/a", "/b", "/c", "/c/d", "/e"} {
		if err := d.SetLineComment(p, "note"); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	for _, p := range []string{"/a", "/b", "/c", "/c/d", "/e"} {
		if err := d.SetLineComment(p, ""); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if d.String() != in {
		t.Errorf("not restored: %q", d.String())
	}
	for _, p := range []string{"/a", "/b", "/c", "/c/d", "/e"} {
		if err := d.SetHeadComment(p, "note"); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	for _, p := range []string{"/a", "/b", "/c", "/c/d", "/e"} {
		if err := d.SetHeadComment(p, ""); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if d.String() != in {
		t.Errorf("head comments not restored: %q", d.String())
	}
}

func TestStringers(t *testing.T) {
	d, _ := Parse([]byte("a: [1, 2]\nb: text # c\n"))
	if got := fmt.Sprint(d); got != "a: [1, 2]\nb: text # c\n" {
		t.Errorf("Sprint(doc) = %q", got)
	}
	if got := fmt.Sprintf("%s|%v", d, d); got != "a: [1, 2]\nb: text # c\n|a: [1, 2]\nb: text # c\n" {
		t.Errorf("Sprintf = %q", got)
	}
	a, _ := d.Root().Lookup("a")
	if got := fmt.Sprint(a); got != "[1, 2]" {
		t.Errorf("Sprint(node) = %q", got)
	}
	b, _ := d.Root().Lookup("b")
	if got := b.String(); got != "text" {
		t.Errorf("String() = %q", got)
	}
	var zero Node
	if got := zero.String(); got != "<invalid node>" {
		t.Errorf("zero Node: %q", got)
	}
	if err := d.Set("/b", "x"); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(b); got != "<stale node>" {
		t.Errorf("stale node: %q", got)
	}
	var nd *Document
	if nd.String() != "" {
		t.Error("nil document")
	}
	// A view prints the whole stream, as Bytes does.
	m, _ := Parse([]byte("a: 1\n---\nb: 2\n"))
	if m.Doc(1).String() != m.String() || m.String() != string(m.Bytes()) {
		t.Error("String differs from Bytes")
	}
}

func TestTrimmedComments(t *testing.T) {
	src := "top:\n  # first\n    # second\n  a: 1 # tail   \n  # solo\n  b: 2\n"
	d, _ := Parse([]byte(src))
	top, _ := d.Root().Lookup("top")
	a, _ := top.Lookup("a")
	b, _ := top.Lookup("b")
	if got := a.HeadComment(); got != "# first\n# second" {
		t.Errorf("a head: %q", got)
	}
	if got := a.LineComment(); got != "# tail" {
		t.Errorf("a line: %q", got)
	}
	if got := b.HeadComment(); got != "# solo" {
		t.Errorf("b head: %q", got)
	}
	if got := b.LineComment(); got != "" {
		t.Errorf("b line: %q", got)
	}
	c, _ := Parse([]byte("a: 1 # x\r\n# h\r\nb: 2\r\n"))
	ca, _ := c.Root().Lookup("a")
	cb, _ := c.Root().Lookup("b")
	if ca.LineComment() != "# x" || cb.HeadComment() != "# h" {
		t.Errorf("crlf: %q %q", ca.LineComment(), cb.HeadComment())
	}
	// Source still gives the exact text.
	if got := a.Source(); got != "1" {
		t.Errorf("source: %q", got)
	}
}

func TestYAMLPatchText(t *testing.T) {
	doc := "a: 1\nlist:\n  - x\n  - y\nobj:\n  k: v\n"
	yamlPatch := "- op: replace\n  path: /a\n  value: 7\n" +
		"- op: add\n  path: /list/-\n  value: z\n" +
		"- op: remove\n  path: /obj/k\n" +
		"- {op: add, path: /new, value: {n: [1, 2.5, true, null, \"s\"]}}\n" +
		"- op: copy\n  from: /a\n  path: /b\n" +
		"- op: move\n  from: /b\n  path: /c\n" +
		"- op: test\n  path: /c\n  value: 7\n"
	jsonPatch := `[{"op":"replace","path":"/a","value":7},{"op":"add","path":"/list/-","value":"z"},{"op":"remove","path":"/obj/k"},` +
		`{"op":"add","path":"/new","value":{"n":[1,2.5,true,null,"s"]}},{"op":"copy","from":"/a","path":"/b"},{"op":"move","from":"/b","path":"/c"},{"op":"test","path":"/c","value":7}]`
	y, _ := Parse([]byte(doc))
	if err := y.ApplyPatch([]byte(yamlPatch)); err != nil {
		t.Fatal(err)
	}
	j, _ := Parse([]byte(doc))
	if err := j.ApplyPatch([]byte(jsonPatch)); err != nil {
		t.Fatal(err)
	}
	if y.String() != j.String() {
		t.Errorf("YAML patch gives\n%s\nJSON patch gives\n%s", y.String(), j.String())
	}
	// A YAML patch that holds JSON-looking text is still JSON first.
	f, _ := Parse([]byte(doc))
	if err := f.ApplyPatch([]byte(`[{"op": "replace", "path": "/a", "value": "7"}]`)); err != nil {
		t.Fatal(err)
	}
	if f.String() != strings.Replace(doc, "a: 1", `a: "7"`, 1) && f.String() != strings.Replace(doc, "a: 1", "a: '7'", 1) {
		t.Errorf("json route: %q", f.String())
	}
	// A failing step leaves the document as it was, and is named.
	z, _ := Parse([]byte(doc))
	err := z.ApplyPatch([]byte("- op: replace\n  path: /a\n  value: 2\n- op: test\n  path: /a\n  value: 3\n"))
	var ee *EditError
	if !errors.As(err, &ee) || ee.Index != 1 || z.String() != doc {
		t.Errorf("failing test step: %v, doc %q", err, z.String())
	}
}

func TestYAMLPatchTextRefusals(t *testing.T) {
	doc := "a: 1\n"
	for _, c := range []struct{ name, patch string }{
		{"not a sequence", "op: add\npath: /a\nvalue: 1\n"},
		{"unknown op", "- op: frobnicate\n  path: /a\n"},
		{"no path", "- op: remove\n"},
		{"no value", "- op: add\n  path: /b\n"},
		{"alias", "- op: add\n  path: /b\n  value: &x [1]\n- op: add\n  path: /c\n  value: *x\n"},
		{"duplicate key", "- op: add\n  path: /b\n  op: remove\n"},
		{"duplicate key in a value", "- op: add\n  path: /b\n  value: {k: 1, k: 2}\n"},
		{"two documents", "- op: remove\n  path: /a\n---\n- op: remove\n  path: /a\n"},
		{"plain text", "hello world"},
		{"broken yaml", "- op: [add\n"},
		{"broken json", `[{"op":"add",`},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, _ := Parse([]byte(doc))
			err := d.ApplyPatch([]byte(c.patch))
			if !errors.Is(err, ErrInvalidPatch) {
				t.Fatalf("error %v, want ErrInvalidPatch", err)
			}
			if d.String() != doc {
				t.Errorf("document changed: %q", d.String())
			}
		})
	}
	// Invalid JSON that is not YAML either keeps reporting the JSON error.
	d, _ := Parse([]byte(doc))
	err := d.ApplyPatch([]byte(`[{"op":"add",`))
	if err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("broken JSON error: %v", err)
	}
}

func TestMergeKeyPointers(t *testing.T) {
	src := "base: &b\n  x: 1\n  y: 2\nuse:\n  <<: *b\n  z: 3\n"
	d, err := Parse([]byte(src), MergeKeys())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]int
	if err := d.Get("/use", &m); err != nil || len(m) != 3 {
		t.Fatalf("decoded mapping: %v %v", m, err)
	}
	var x int
	if err := d.Get("/use/x", &x); err != nil || x != 1 {
		t.Errorf("a merged key is read through its pointer: %v %v", x, err)
	}
	if err := d.Set("/use/<</x", 5); !errors.Is(err, ErrAliasWrite) {
		t.Errorf("writing through the merge key: %v", err)
	}
	if err := d.Set("/use/x", 9); err != nil {
		t.Fatal(err)
	}
	if err := d.Get("/use/x", &x); err != nil || x != 9 {
		t.Errorf("explicit key: %v %v", x, err)
	}
}

// ---- generated probe ----

var commentOnlyLine = regexp.MustCompile(`^[ \t]*#`)

var lineRe = regexp.MustCompile(`[^\r\n]*(\r\n|\r|\n)|[^\r\n]+`)

// changedLines returns the lines of a and b between their common first and last
// lines.
func changedLines(a, b string) (ra, rb []string) {
	a, b = strings.TrimPrefix(a, "\ufeff"), strings.TrimPrefix(b, "\ufeff")
	la, lb := lineRe.FindAllString(a, -1), lineRe.FindAllString(b, -1)
	i := 0
	for i < len(la) && i < len(lb) && la[i] == lb[i] {
		i++
	}
	ja, jb := len(la), len(lb)
	for ja > i && jb > i && la[ja-1] == lb[jb-1] {
		ja--
		jb--
	}
	return la[i:ja], lb[i:jb]
}

type probeEntry struct {
	ptr      string
	anch     bool // the value or an ancestor carries an anchor, so aliases may share it
	doc      int
	keyStart int // span of the key text, -1 for items
	keyEnd   int
}

func probeEntries(d *Document) []probeEntry {
	var out []probeEntry
	for i := 0; i < d.Len(); i++ {
		var walk func(n Node, depth int, anch bool)
		walk = func(n Node, depth int, anch bool) {
			if len(out) > 60 || depth > 8 {
				return
			}
			anch = anch || n.Anchor() != ""
			if p, err := n.Pointer(); err == nil {
				e := probeEntry{ptr: p, doc: i, keyStart: -1, anch: anch}
				out = append(out, e)
			}
			switch n.Kind() {
			case MappingNode:
				for k, v := range n.Entries() {
					if p, err := v.Pointer(); err == nil && len(out) < 60 {
						s, e := k.Span()
						out = append(out, probeEntry{ptr: p + "\x00key", doc: i, keyStart: s, keyEnd: e, anch: anch || v.Anchor() != ""})
					}
					walk(v, depth+1, anch)
				}
			case SequenceNode:
				for v := range n.Items() {
					walk(v, depth+1, anch)
				}
			}
		}
		walk(d.Doc(i).Root(), 0, false)
	}
	return out
}

type probeCounts struct {
	renameDecoded                                                                      int
	files, renameOK, renameRefused, headOK, headRefused, lineOK, lineRefused, restored int
	refusals                                                                           map[string]int
}

func (pc *probeCounts) refuse(op string, err error) {
	if pc.refusals == nil {
		pc.refusals = map[string]int{}
	}
	var ee *EditError
	kind := "other"
	switch {
	case errors.Is(err, ErrUnsupportedEdit):
		kind = "unsupported (" + trimKind(err.Error()) + ")"
		if i := strings.LastIndex(err.Error(), ": "); i >= 0 {
			kind = "unsupported (" + err.Error()[i+2:] + ")"
		}
	case errors.Is(err, ErrKeyExists):
		kind = "key exists"
	case errors.Is(err, ErrNotAnEntry):
		kind = "not an entry"
	case errors.Is(err, ErrNotFound):
		kind = "not found"
	case errors.Is(err, ErrAliasWrite):
		kind = "alias"
	case errors.Is(err, ErrAmbiguousKey):
		kind = "ambiguous"
	case errors.As(err, &ee):
		kind = "edit error: " + ee.Kind.Error()
	}
	pc.refusals[op+": "+kind]++
}

// probeInput renames and comments every entry of one input. Every accepted edit
// must parse, read back with the values it had (the key renamed, for a rename),
// agree with the decoder, and change only the lines it names.
func probeInput(t *testing.T, name string, data []byte, pc *probeCounts) {
	d0, err := Parse(data)
	if err != nil {
		return
	}
	pc.files++
	// The document's own text: UTF-8, as the edits see it.
	src := string(d0.s.src)
	if !strings.HasSuffix(src, "\n") && strings.Contains(src, "\n") && false {
		return
	}
	before, err := streamValues(d0)
	if err != nil {
		return
	}
	var decBefore any
	decOK := Unmarshal(data, &decBefore) == nil
	for _, e := range probeEntries(d0) {
		isKey := strings.HasSuffix(e.ptr, "\x00key")
		ptr := strings.TrimSuffix(e.ptr, "\x00key")
		fresh := func() *Document {
			d, err := Parse(data)
			if err != nil {
				t.Fatalf("%s: reparse: %v", name, err)
			}
			return d
		}
		check := func(op string, d *Document) {
			out := string(d.s.src)
			re, err := Parse([]byte(out))
			if err != nil {
				t.Errorf("%s %s %q: output does not parse: %v\n%q", name, op, ptr, err, out)
				return
			}
			got, err := streamValues(re)
			if err != nil {
				t.Errorf("%s %s %q: %v", name, op, ptr, err)
				return
			}
			want := before
			if op == "rename" {
				want = make([]edit.Value, len(before))
				for i := range before {
					want[i] = cloneValue(before[i])
				}
				m, err := modelRename(want[e.doc], ptr, "probe key")
				if err != nil {
					t.Errorf("%s %s %q: model: %v", name, op, ptr, err)
					return
				}
				want[e.doc] = m
			}
			if !reflect.DeepEqual(canonAll(got), canonAll(want)) {
				t.Errorf("%s %s %q: values differ\n%q", name, op, ptr, out)
				return
			}
			if decOK && op != "rename" {
				var dv any
				if err := Unmarshal([]byte(out), &dv); err != nil || fmt.Sprintf("%v", dv) != fmt.Sprintf("%v", decBefore) {
					t.Errorf("%s %s %q: the decoder reads other values (%v)\n%q", name, op, ptr, err, out)
				}
			}
		}
		if isKey {
			d := fresh()
			if err := d.Doc(e.doc).Rename(ptr, "probe key"); err != nil {
				pc.renameRefused++
				pc.refuse("rename", err)
				if string(d.s.src) != src {
					t.Errorf("%s rename %q refused but the text changed", name, ptr)
				}
			} else {
				pc.renameOK++
				check("rename", d)
				out := string(d.s.src)
				// Outside the key nothing changed.
				if !strings.HasPrefix(out, src[:e.keyStart]) || !strings.HasSuffix(out, src[e.keyEnd:]) {
					// With a tag or anchor on the key the span excludes them, so this holds too.
					t.Errorf("%s rename %q: text outside the key changed\n in: %q\nout: %q", name, ptr, src, out)
				}
				// Through the decoder, which expands aliases: the same values with the key
				// renamed. A mapping that aliases share is renamed in every use, so entries
				// below an anchor are left to the tree comparison above.
				if decOK && e.doc == 0 && !e.anch {
					var d0, d1 any
					toks, _ := pointer.Parse(ptr)
					if Unmarshal(data, &d0) == nil && renameGeneric(d0, toks, "probe key") {
						if err := Unmarshal([]byte(out), &d1); err != nil || fmt.Sprintf("%v", d1) != fmt.Sprintf("%v", d0) {
							t.Errorf("%s rename %q: the decoder reads other values (%v)\n in: %q\nout: %q", name, ptr, err, src, out)
						}
						pc.renameDecoded++
					}
				}
			}
			continue
		}
		// Head comment.
		d := fresh()
		if err := d.Doc(e.doc).SetHeadComment(ptr, "probe one\nprobe two"); err != nil {
			pc.headRefused++
			pc.refuse("head", err)
			if errors.Is(err, errEdit) && os.Getenv("PROBE_DEBUG") != "" {
				t.Logf("EDITFAIL %s %q in %q", name, ptr, src)
			}
			if string(d.s.src) != src {
				t.Errorf("%s head %q refused but the text changed", name, ptr)
			}
		} else {
			pc.headOK++
			check("head", d)
			ra, rb := changedLines(src, string(d.s.src))
			for _, l := range append(append([]string(nil), ra...), rb...) {
				if !commentOnlyLine.MatchString(l) {
					t.Errorf("%s head %q: a line that is not a comment changed: %q", name, ptr, l)
				}
			}
			re, _ := Parse(d.s.src)
			if n, ok := nodeAt(re.Doc(e.doc), ptr); ok && n.HeadComment() != "# probe one\n# probe two" {
				t.Errorf("%s head %q: reads back %q", name, ptr, n.HeadComment())
			}
			if err := d.Doc(e.doc).SetHeadComment(ptr, ""); err != nil {
				t.Errorf("%s head %q: removing: %v", name, ptr, err)
			}
		}
		// Line comment.
		d = fresh()
		if err := d.Doc(e.doc).SetLineComment(ptr, "probe line"); err != nil {
			pc.lineRefused++
			pc.refuse("line", err)
			if string(d.s.src) != src {
				t.Errorf("%s line %q refused but the text changed", name, ptr)
			}
		} else {
			pc.lineOK++
			check("line", d)
			ra, rb := changedLines(src, string(d.s.src))
			if len(ra) > 1 || len(rb) != 1 || (len(ra) == 1 && strings.TrimRight(ra[0], "\r\n") == strings.TrimRight(rb[0], "\r\n")) {
				t.Errorf("%s line %q: more than one line changed: %q -> %q", name, ptr, ra, rb)
			}
			re, _ := Parse(d.s.src)
			if n, ok := nodeAt(re.Doc(e.doc), ptr); ok && n.LineComment() != "# probe line" {
				t.Errorf("%s line %q: reads back %q", name, ptr, n.LineComment())
			}
			if err := d.Doc(e.doc).SetLineComment(ptr, ""); err != nil {
				t.Errorf("%s line %q: removing: %v", name, ptr, err)
			} else if !hasLineComment(d0, e.doc, ptr) {
				if trimTrailing(string(d.s.src)) == trimTrailing(src) {
					pc.restored++
				} else {
					t.Errorf("%s line %q: set then remove does not restore the text\n in: %q\nout: %q", name, ptr, src, string(d.s.src))
				}
			}
		}
	}
}

// trimTrailing removes the blanks before each line break.
func trimTrailing(s string) string {
	return regexp.MustCompile(`[ 	]+(
?
)`).ReplaceAllString(s, "$1")
}

// renameGeneric renames, in place, the key at toks in a value the decoder made. It
// reports whether the path led to a mapping entry.
func renameGeneric(root any, toks []string, newKey string) bool {
	cur := root
	for i, tok := range toks {
		last := i == len(toks)-1
		switch m := cur.(type) {
		case map[string]any:
			v, ok := m[tok]
			if !ok {
				return false
			}
			if last {
				delete(m, tok)
				m[newKey] = v
				return true
			}
			cur = v
		case map[any]any:
			var k any
			found := false
			for key := range m {
				if _, coll := key.(map[any]any); coll {
					continue
				}
				if _, coll := key.([]any); coll {
					continue
				}
				if fmt.Sprint(key) == tok {
					if found {
						return false // keys that print alike (1 and 1.0): the pointer's meaning is the document's
					}
					k, found = key, true
				}
			}
			if !found {
				return false
			}
			if last {
				v := m[k]
				delete(m, k)
				m[newKey] = v
				return true
			}
			cur = m[k]
		case []any:
			var idx int
			if _, err := fmt.Sscan(tok, &idx); err != nil || idx < 0 || idx >= len(m) || last {
				return false
			}
			cur = m[idx]
		default:
			return false
		}
	}
	return false
}

func hasLineComment(d *Document, doc int, ptr string) bool {
	n, ok := nodeAt(d.Doc(doc), ptr)
	return !ok || n.LineComment() != ""
}

func nodeAt(d *Document, ptr string) (Node, bool) {
	toks, err := pointer.Parse(ptr)
	if err != nil {
		return Node{}, false
	}
	n := d.Root()
	for _, tok := range toks {
		switch n.Kind() {
		case SequenceNode:
			var i int
			if _, err := fmt.Sscan(tok, &i); err != nil || i >= n.Len() {
				return Node{}, false
			}
			n = n.Item(i)
		case MappingNode:
			var ok bool
			if n, ok = n.Lookup(tok); !ok {
				return Node{}, false
			}
		default:
			return Node{}, false
		}
	}
	return n, true
}

// TestRenameAndCommentProbe renames and comments every entry of every kit input
// (and, outside -short, of the yaml-test-suite inputs).
func TestRenameAndCommentProbe(t *testing.T) {
	var files []string
	if root := os.Getenv("ONEYAML_KIT"); root != "" {
		for _, cat := range []string{"roundtrip", "edit", "schema", "parse"} {
			m, _ := filepath.Glob(filepath.Join(root, "suites", "own", "cases", cat, "*", "input*"))
			files = append(files, m...)
			m, _ = filepath.Glob(filepath.Join(root, "suites", "own", "cases", cat, "*", "expected*"))
			files = append(files, m...)
		}
	}
	if dir := os.Getenv("ONEYAML_YTS_DIR"); dir != "" && !testing.Short() {
		m, _ := filepath.Glob(filepath.Join(dir, "*", "in.yaml"))
		files = append(files, m...)
	}
	// A few inputs of the probe's own, for runs without the kit.
	own := []string{
		"a: 1\nb:\n  - x\n  - y: 2\n    z: [1, 2]\n# tail\n",
		"? a\n: 1\n? |\n  b\n: 2\n",
		"- &x a\n- *x\n- {k: v, 'q r': \"s\"}\n- |\n  lit\n- >-\n  fold\n",
		"--- # m\na: 1\n...\n--- !!map\nb: 2 # c\n",
		"a: 1\r\nb: |\r\n  t\r\nc: 3\r\n",
	}
	var pc probeCounts
	for i, s := range own {
		probeInput(t, fmt.Sprintf("own-%d", i), []byte(s), &pc)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		probeInput(t, f, data, &pc)
	}
	t.Logf("probe: %d inputs; rename %d accepted, %d refused; head comment %d accepted, %d refused; line comment %d accepted, %d refused; %d set-then-remove restored the input", pc.files, pc.renameOK, pc.renameRefused, pc.headOK, pc.headRefused, pc.lineOK, pc.lineRefused, pc.restored)
	t.Logf("%d accepted renames were also compared through the alias-expanding decoder", pc.renameDecoded)
	t.Logf("refusals: %v", pc.refusals)
}

func TestYAMLPatchBadScalarsRefused(t *testing.T) {
	doc := "a: 1\nb: [x, y]\n"
	for _, p := range []string{
		"- op: replace\n  path: \"/a\\uDFFF\"\n  value: {x: 1}\n",
		"- op: add\n  path: /c\n  value: [\"ok\", \"bad \\ud800 tail\"]\n",
		"- op: add\n  path: /c\n  value: \"\\x4\"\n",
		"- op: add\n  path: /c\n  value: \"\\q\"\n",
		"- op: add\n  path: /c\n  value: {\"k\\ud800\": 1}\n",
		"- op: add\n  path: /c\n  value: !foo bar\n",
		"- op: add\n  path: /c\n  value: !foo [1]\n",
		"- op: add\n  path: /c\n  value: !<!x> y\n",
	} {
		d, _ := Parse([]byte(doc))
		err := d.ApplyPatch([]byte(p))
		if !errors.Is(err, ErrInvalidPatch) {
			t.Errorf("%q: error %v, want ErrInvalidPatch", p, err)
		}
		if d.String() != doc {
			t.Errorf("%q: document changed to %q", p, d.String())
		}
	}
	// A core tag is fine, and so is a valid escape.
	d, _ := Parse([]byte(doc))
	if err := d.ApplyPatch([]byte("- op: add\n  path: /c\n  value: !!str 12\n- op: add\n  path: /d\n  value: \"t\\u00e9\\n\"\n")); err != nil {
		t.Fatal(err)
	}
	var v struct {
		C string
		D string
	}
	if err := d.Get("", &v); err != nil || v.C != "12" || v.D != "t\u00e9\n" {
		t.Errorf("values: %+v %v", v, err)
	}
	// The error says where.
	err := func() error {
		d, _ := Parse([]byte(doc))
		return d.ApplyPatch([]byte("- op: replace\n  path: \"/a\\uDFFF\"\n  value: 1\n"))
	}()
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("no position in %v", err)
	}
}

// Text that starts like JSON is judged as JSON, with the JSON errors, however
// YAML would read it.
func TestJSONLookingPatchesKeepJSONErrors(t *testing.T) {
	doc := "a: 1\nb: [x, y]\n"
	for _, p := range []string{
		`[{"op":"add","op":"remove","path":"/a"}]`,
		`[{"op":"add","path":"/z","value":1},]`,
		`[{'op':'add','path':'/z','value':1}]`,
		`[{op: add, path: /z, value: 1}]`,
		`[{"op":"add","path":"/z","value":NaN}]`,
		`[{"op":"add","path":"/z","value":Infinity}]`,
		`[{"op":"add","path":"/z","value":01}]`,
		`[{"op":"add","path":"/z","value":+1}]`,
		`[{"op":"add","path":"/z","value":0x10}]`,
		`[{"op":"add","path":"/z","value":"\x41"}]`,
		"[{\"op\":\"add\",\"path\":\"/z\",\"value\":\"a\tb\"}]",
		"\ufeff[{\"op\":\"add\",\"path\":\"/z\",\"value\":1}]",
		"\ufeff \n[{op: add, path: /z, value: 1}]",
		"  \n\t[{op: add, path: /z, value: 1}]",
		`{"op":"add","path":"/z","value":1}`,
		`[`,
		`[{"op":"add","path":"/z","value":1}] trailing`,
	} {
		d, _ := Parse([]byte(doc))
		err := d.ApplyPatch([]byte(p))
		if !errors.Is(err, ErrInvalidPatch) || !strings.Contains(err.Error(), "JSON") {
			t.Errorf("%q: error %v, want an ErrInvalidPatch about JSON", p, err)
		}
		if d.String() != doc {
			t.Errorf("%q: document changed to %q", p, d.String())
		}
	}
}

func TestYAMLPatchDepthLimit(t *testing.T) {
	doc := "a: 1\n"
	deep := strings.Repeat("[", 50) + strings.Repeat("]", 50)
	d, _ := Parse([]byte(doc), Limits(LimitSet{MaxDepth: 10}))
	err := d.ApplyPatch([]byte("- op: add\n  path: /z\n  value: " + deep + "\n"))
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != "depth" {
		t.Errorf("error %v, want a depth LimitError", err)
	}
	if d.String() != doc {
		t.Errorf("document changed: %q", d.String())
	}
}

func TestRenameAnchoredKey(t *testing.T) {
	d, _ := Parse([]byte("&k a: 1\nb: *k\n"))
	err := d.Rename("/a", "x")
	if !errors.Is(err, ErrAnchorInUse) {
		t.Errorf("error %v, want ErrAnchorInUse", err)
	}
	if d.String() != "&k a: 1\nb: *k\n" {
		t.Errorf("document changed: %q", d.String())
	}
	// An anchor nothing uses does not stand in the way, and neither does an alias
	// of the value.
	u, _ := Parse([]byte("&k a: 1\nb: 2\n"))
	if err := u.Rename("/a", "x"); err != nil || u.String() != "&k x: 1\nb: 2\n" {
		t.Errorf("unused anchor: %v %q", err, u.String())
	}
	v, _ := Parse([]byte("a: &v 1\nb: *v\n"))
	if err := v.Rename("/a", "x"); err != nil || v.String() != "x: &v 1\nb: *v\n" {
		t.Errorf("value anchor: %v %q", err, v.String())
	}
}

// The non-specific tag "!" makes a plain scalar a string; it is not a tag outside
// the core schema.
func TestYAMLPatchNonSpecificTag(t *testing.T) {
	for _, c := range []struct{ patch, want string }{
		{"- op: add\n  path: /z\n  value: ! 12\n", "a: 1\nz: '12'\n"},
		{"- op: add\n  path: /z\n  value: ! [1]\n", "a: 1\nz: [1]\n"},
	} {
		d, _ := Parse([]byte("a: 1\n"))
		if err := d.ApplyPatch([]byte(c.patch)); err != nil {
			t.Errorf("%q: %v", c.patch, err)
			continue
		}
		var got, want any
		if err := Unmarshal(d.Bytes(), &got); err != nil {
			t.Errorf("%q: result %q unreadable: %v", c.patch, d.Bytes(), err)
			continue
		}
		_ = Unmarshal([]byte(c.want), &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q, want data of %q", c.patch, d.Bytes(), c.want)
		}
	}
	// Tags outside the core schema stay refused, local or global.
	for _, tag := range []string{"!x", "!<tag:example.com,2000:x>"} {
		d, _ := Parse([]byte("a: 1\n"))
		err := d.ApplyPatch([]byte("- op: add\n  path: /z\n  value: " + tag + " 1\n"))
		if !errors.Is(err, ErrInvalidPatch) || !strings.Contains(err.Error(), "outside the core schema") || d.String() != "a: 1\n" {
			t.Errorf("%s: err %v, doc %q", tag, err, d.String())
		}
	}
}

// A mapping in a YAML patch value whose keys the decoder reads as one key is
// refused, so the document never receives text it cannot be read from.
func TestYAMLPatchCoreEqualDuplicateKeys(t *testing.T) {
	doc := "a: 1\nb: [x, y]\n"
	for _, v := range []string{"{1: a, 0x1: b}", "{null: 1, ~: 2}", "{true: 1, True: 2}", "{a: 1, a: 2}"} {
		d, _ := Parse([]byte(doc))
		err := d.ApplyPatch([]byte("- op: add\n  path: /z\n  value: " + v + "\n"))
		if !errors.Is(err, ErrInvalidPatch) {
			t.Errorf("%s: err %v, want ErrInvalidPatch", v, err)
		}
		if d.String() != doc {
			t.Errorf("%s: document changed to %q", v, d.String())
		}
	}
	// JSON objects spell every key as a string, so 1 and 0x1 cannot meet there; the
	// JSON reader refuses a repeated spelling already.
	d, _ := Parse([]byte(doc))
	if err := d.ApplyPatch([]byte(`[{"op":"add","path":"/z","value":{"1":"a","0x1":"b"}}]`)); err != nil {
		t.Errorf("JSON string keys 1 and 0x1: %v", err)
	}
	var v any
	if err := Unmarshal(d.Bytes(), &v); err != nil {
		t.Errorf("result unreadable: %v\n%s", err, d.Bytes())
	}
}

// Text that is neither JSON-looking nor YAML reports the YAML error and where it is.
func TestYAMLPatchSyntaxErrorHasPosition(t *testing.T) {
	d, _ := Parse([]byte("a: 1\n"))
	err := d.ApplyPatch([]byte("- op: add\n  path: /z\n  value: \"\\x\"\n"))
	if !errors.Is(err, ErrInvalidPatch) || !strings.Contains(err.Error(), "line 3") || strings.Contains(err.Error(), "JSON") {
		t.Errorf("err %v, want the YAML error with line 3", err)
	}
	d2, _ := Parse([]byte("a: 1\n"))
	err = d2.ApplyPatch([]byte("- op: add\n  path: /z\n  value: [1\n"))
	if !errors.Is(err, ErrInvalidPatch) || strings.Contains(err.Error(), "JSON") {
		t.Errorf("err %v, want a YAML error", err)
	}
}

// Keys that are spelled differently but read as one value are named in the error,
// with the position of the first, as the decoder does for any target.
func TestRepeatedKeysByValueNameBothKeys(t *testing.T) {
	for _, c := range []struct{ val, want string }{
		{"{1: a, 0x1: b}", `mapping key "0x1" has the same value as the key "1" at line 3, column 11`},
		{"{null: 1, ~: 2}", `mapping key "~" has the same value as the key "null" at line 3, column 11`},
		{"{true: 1, True: 2}", `mapping key "True" has the same value as the key "true" at line 3, column 11`},
	} {
		var v any
		derr := Unmarshal([]byte("v: "+c.val+"\n"), &v)
		if derr == nil || !strings.Contains(derr.Error(), "has the same value as the key") {
			t.Errorf("%s: Unmarshal error %v", c.val, derr)
		}
		d, _ := Parse([]byte("a: 1\n"))
		err := d.ApplyPatch([]byte("- op: add\n  path: /z\n  value: " + c.val + "\n"))
		if !errors.Is(err, ErrInvalidPatch) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: patch error %v, want it to contain %s", c.val, err, c.want)
		}
		if d.String() != "a: 1\n" {
			t.Errorf("%s: document changed", c.val)
		}
	}
}
