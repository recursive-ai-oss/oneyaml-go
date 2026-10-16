package oneyaml_test

import (
	"errors"
	"fmt"
	"log"
	"testing"

	oneyaml "oneyaml.dev/go"
)

const imagesDoc = "images:\n  web: nginx:1.25\n  db: postgres:15\n  cache: redis:7\n"

func TestNodePointer(t *testing.T) {
	src := "a:\n  - x: 1\n    \"b/c~d\": 2\n  - [p, q]\n? [k]\n: v\nanc: &r {z: 1}\nal: *r\n\"\": e\n"
	doc, err := oneyaml.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"": "", "/a": "/a", "/a/0/x": "/a/0/x", "/a/0/b~1c~0d": "/a/0/b~1c~0d",
		"/a/1/1": "/a/1/1", "/anc/z": "/anc/z", "/al": "/al", "/": "/",
	}
	for in, want := range cases {
		var n oneyaml.Node
		if err := doc.Get(in, &n); err != nil {
			t.Fatalf("Get %q: %v", in, err)
		}
		got, err := n.Pointer()
		if err != nil || got != want {
			t.Errorf("Pointer of %q = %q, %v; want %q", in, got, err, want)
		}
	}
	// Keys have no pointer; neither do nodes inside a complex key.
	for k := range doc.Root().Entries() {
		if _, err := k.Pointer(); !errors.Is(err, oneyaml.ErrNoPointer) {
			t.Errorf("key %q: err = %v, want ErrNoPointer", k.Source(), err)
		}
	}
	for k := range doc.Root().Entries() {
		if k.Kind() == oneyaml.SequenceNode {
			if _, err := k.Item(0).Pointer(); !errors.Is(err, oneyaml.ErrNoPointer) {
				t.Errorf("inside complex key: err = %v", err)
			}
		}
	}
	// A repeated key: the pointer would be ambiguous, so there is none.
	dup, err := oneyaml.Parse([]byte("a: 1\na: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range dup.Root().Entries() {
		if _, err := v.Pointer(); !errors.Is(err, oneyaml.ErrNoPointer) {
			t.Errorf("duplicate key: err = %v", err)
		}
	}
	if _, err := (oneyaml.Node{}).Pointer(); !errors.Is(err, oneyaml.ErrNotFound) {
		t.Errorf("zero node: %v", err)
	}
}

func TestStaleNode(t *testing.T) {
	doc, _ := oneyaml.Parse([]byte(imagesDoc))
	imgs, _ := doc.Root().Lookup("images")
	web, _ := imgs.Lookup("web")
	if web.Stale() {
		t.Fatal("fresh node is stale")
	}
	if err := doc.Set("/images/db", "x"); err != nil {
		t.Fatal(err)
	}
	if !web.Stale() || web.IsValid() || (oneyaml.Node{}).Stale() {
		t.Errorf("Stale %v IsValid %v", web.Stale(), web.IsValid())
	}
	var s string
	if err := web.Decode(&s); !errors.Is(err, oneyaml.ErrStaleNode) {
		t.Errorf("Decode: %v", err)
	}
	if _, err := web.Pointer(); !errors.Is(err, oneyaml.ErrStaleNode) {
		t.Errorf("Pointer: %v", err)
	}
	if err := (oneyaml.Node{}).Decode(&s); !errors.Is(err, oneyaml.ErrNotFound) {
		t.Errorf("zero Decode: %v", err)
	}
	if web.Value() != "" || web.Len() != 0 {
		t.Error("stale node returned data")
	}
	// Setting from a stale node is refused.
	if err := doc.Set("/images/new", web); !errors.Is(err, oneyaml.ErrStaleNode) {
		t.Errorf("Set from stale: %v", err)
	}
}

func TestCollectPointersThenEdit(t *testing.T) {
	doc, _ := oneyaml.Parse([]byte(imagesDoc))
	imgs, _ := doc.Root().Lookup("images")
	type edit struct{ ptr, val string }
	var edits []edit
	for _, v := range imgs.Entries() {
		p, err := v.Pointer()
		if err != nil {
			t.Fatal(err)
		}
		edits = append(edits, edit{p, v.Value() + "-patched"})
	}
	for _, e := range edits {
		if err := doc.Set(e.ptr, e.val); err != nil {
			t.Fatal(err)
		}
	}
	want := "images:\n  web: nginx:1.25-patched\n  db: postgres:15-patched\n  cache: redis:7-patched\n"
	if got := string(doc.Bytes()); got != want {
		t.Errorf("got:\n%s", got)
	}
}

// Collect the pointers of the images, then edit them.
func ExampleNode_Pointer() {
	doc, err := oneyaml.Parse([]byte("images:\n  web: nginx:1.25\n  db: postgres:15\n"))
	if err != nil {
		log.Fatal(err)
	}
	imgs, ok := doc.Root().Lookup("images")
	if !ok {
		log.Fatal("no images")
	}

	// First collect where each value is, then edit. Replacing values moves
	// nothing, so every pointer still names the same value.
	var ptrs, vals []string
	for _, v := range imgs.Entries() {
		p, err := v.Pointer()
		if err != nil {
			log.Fatal(err)
		}
		ptrs = append(ptrs, p)
		vals = append(vals, v.Value())
	}
	// Then edit by pointer.
	for i, p := range ptrs {
		if err := doc.Set(p, vals[i]+"-patched"); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Print(string(doc.Bytes()))

	// Nodes read before the edits are stale now.
	_, err = imgs.Pointer()
	fmt.Println(imgs.Stale(), errors.Is(err, oneyaml.ErrStaleNode))
	// Output:
	// images:
	//   web: nginx:1.25-patched
	//   db: postgres:15-patched
	// true true
}
