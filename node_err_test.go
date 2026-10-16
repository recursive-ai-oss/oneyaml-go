package oneyaml_test

import (
	"errors"
	"fmt"
	"log"
	"testing"

	oneyaml "oneyaml.dev/go"
)

func TestNodeErr(t *testing.T) {
	doc, err := oneyaml.Parse([]byte("a: ''\nb: x\n"))
	if err != nil {
		t.Fatal(err)
	}
	var a, b oneyaml.Node
	if err := doc.Get("/a", &a); err != nil {
		t.Fatal(err)
	}
	if err := doc.Get("/b", &b); err != nil {
		t.Fatal(err)
	}
	if a.Err() != nil || a.Value() != "" {
		t.Fatalf("empty scalar: Err %v", a.Err())
	}
	if err := doc.Set("/b", "y"); err != nil {
		t.Fatal(err)
	}
	// A stale Node reads as "", like the empty scalar; Err tells them apart.
	if a.Value() != "" || !errors.Is(a.Err(), oneyaml.ErrStaleNode) {
		t.Fatalf("stale: value %q, Err %v", a.Value(), a.Err())
	}
	var zero oneyaml.Node
	if err := zero.Err(); !errors.Is(err, oneyaml.ErrNotFound) || errors.Is(err, oneyaml.ErrStaleNode) {
		t.Fatalf("zero Node: %v", err)
	}
	var fresh oneyaml.Node
	if err := doc.Get("/a", &fresh); err != nil || fresh.Err() != nil {
		t.Fatalf("fresh node: %v", err)
	}
}

func TestNodeErrDoesNotAllocate(t *testing.T) {
	doc, _ := oneyaml.Parse([]byte("a: 1\n"))
	var n oneyaml.Node
	_ = doc.Get("/a", &n)
	if got := testing.AllocsPerRun(100, func() { _ = n.Err() }); got != 0 {
		t.Fatalf("Err allocates %v", got)
	}
}

func ExampleNode_Err() {
	doc, err := oneyaml.Parse([]byte("title: ''\nsize: 3\n"))
	if err != nil {
		log.Fatal(err)
	}
	var title oneyaml.Node
	if err := doc.Get("/title", &title); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%q %v\n", title.Value(), title.Err())

	if err := doc.Set("/size", 4); err != nil {
		log.Fatal(err)
	}
	// The edit made title stale. Value is "" again, as for an empty scalar,
	// and only Err says the node can no longer be read.
	fmt.Printf("%q %v\n", title.Value(), errors.Is(title.Err(), oneyaml.ErrStaleNode))
	// Output:
	// "" <nil>
	// "" true
}

func ExampleMergeKeys_pointer() {
	src := "base: &base {image: nginx, port: 80}\nweb:\n  <<: *base\n  port: 8080\n"
	doc, err := oneyaml.Parse([]byte(src), oneyaml.MergeKeys())
	if err != nil {
		log.Fatal(err)
	}
	var image string
	var port int
	if err := doc.Get("/web/image", &image); err != nil { // from the merge
		log.Fatal(err)
	}
	if err := doc.Get("/web/port", &port); err != nil { // the mapping's own key wins
		log.Fatal(err)
	}
	fmt.Println(image, port)

	// Remove would have to write into the merge source, so it is refused.
	err = doc.Remove("/web/image")
	fmt.Println(errors.Is(err, oneyaml.ErrAliasWrite))

	// Set adds a key of its own to web, which overrides the merged one.
	if err := doc.Set("/web/image", "caddy"); err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// nginx 8080
	// true
	// base: &base {image: nginx, port: 80}
	// web:
	//   <<: *base
	//   port: 8080
	//   image: caddy
}
