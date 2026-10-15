package oneyaml

import (
	"fmt"
	"testing"
)

type fuzzHook struct{ V view }

// FuzzDocumentDecode checks that no input panics Document.Decode, Node.Decode or
// the Unmarshaler hook, and that a Document and the plain decoder read the first
// document the same way.
func FuzzDocumentDecode(f *testing.F) {
	for _, s := range []string{
		"", "a: 1", "- [1, 2]\n- {a: b}", "a: &x [1]\nb: *x", "a: &a [x]\nb: &b [*a, *a]\nc: [*b, *b]",
		"%TAG !e! tag:e.com,2000:\n--- !e!t v\n...\n--- w", "? [a]\n: b", "! 12", "v: {a: &z 1, b: *z}",
		"\xff\xfea\x00:\x001\x00", "a: |\n  x\n",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := Parse(data)
		if err != nil {
			return
		}
		var a any
		derr := d.Decode(&a)
		var h fuzzHook
		_ = d.Decode(&h)
		var s []fuzzHook
		_ = d.Decode(&s)
		if d.Len() > 0 {
			root := d.Root()
			var walk func(n Node, depth int)
			walk = func(n Node, depth int) {
				if depth > 6 || !n.IsValid() {
					return
				}
				var x any
				_ = n.Decode(&x)
				_ = n.Tag()
				switch n.Kind() {
				case SequenceNode:
					for i := 0; i < n.Len() && i < 4; i++ {
						walk(n.Item(i), depth+1)
					}
				case MappingNode:
					for i := 0; i < n.Len() && i < 4; i++ {
						k, v := n.Entry(i)
						walk(k, depth+1)
						walk(v, depth+1)
					}
				}
			}
			walk(root, 0)
		}
		var u any
		uerr := Unmarshal(data, &u)
		if d.Len() > 0 && (derr == nil) != (uerr == nil) {
			// Unmarshal reads only the first document; the Document read it too.
			t.Fatalf("Decode error %v, Unmarshal error %v", derr, uerr)
		}
		if derr == nil && uerr == nil && fmt.Sprintf("%#v", a) != fmt.Sprintf("%#v", u) {
			t.Fatalf("Decode %#v, Unmarshal %#v", a, u)
		}
	})
}
