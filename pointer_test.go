package oneyaml_test

import (
	"fmt"
	"log"
	"testing"

	oneyaml "oneyaml.dev/go"
)

func ExamplePointer() {
	doc, err := oneyaml.Parse([]byte("metadata:\n  labels:\n    app.kubernetes.io/name: web\n"))
	if err != nil {
		log.Fatal(err)
	}
	ptr := oneyaml.Pointer("metadata", "labels", "app.kubernetes.io/name")
	fmt.Println(ptr)
	if err := doc.Set(ptr, "api"); err != nil {
		log.Fatal(err)
	}
	fmt.Print(doc)
	// Output:
	// /metadata/labels/app.kubernetes.io~1name
	// metadata:
	//   labels:
	//     app.kubernetes.io/name: api
}

func TestPointerEscapes(t *testing.T) {
	for _, c := range []struct {
		toks []string
		want string
	}{
		{nil, ""},
		{[]string{""}, "/"},
		{[]string{"a", "0"}, "/a/0"},
		{[]string{"a/b", "c~d", "~1"}, "/a~1b/c~0d/~01"},
	} {
		if got := oneyaml.Pointer(c.toks...); got != c.want {
			t.Errorf("Pointer(%q) = %q, want %q", c.toks, got, c.want)
		}
	}
}

// Node.Pointer and Pointer agree, and a pointer built from awkward keys finds
// the value it was built for.
func TestPointerMatchesNodePointer(t *testing.T) {
	doc, err := oneyaml.Parse([]byte("\"a/b\":\n  \"c~d\": 1\n  e: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	n, ok := doc.Root().Lookup("a/b")
	if !ok {
		t.Fatal("no a/b")
	}
	leaf, ok := n.Lookup("c~d")
	if !ok {
		t.Fatal("no c~d")
	}
	got, err := leaf.Pointer()
	if err != nil {
		t.Fatal(err)
	}
	if want := oneyaml.Pointer("a/b", "c~d"); got != want {
		t.Fatalf("Node.Pointer = %q, Pointer = %q", got, want)
	}
	var v int
	if err := doc.Get(got, &v); err != nil || v != 1 {
		t.Fatalf("Get(%q) = %d, %v", got, v, err)
	}
}
