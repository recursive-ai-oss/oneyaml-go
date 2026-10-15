package oneyaml_test

import (
	"errors"
	"fmt"
	"log"

	oneyaml "oneyaml.dev/go"
)

// ApplyPatch applies an RFC 6902 patch in one step. Comments and layout outside
// the changed values stay, and if any operation fails nothing changes.
func ExampleDocument_ApplyPatch() {
	doc, err := oneyaml.Parse([]byte(`# service
name: api
replicas: 2   # raised by the release job
tags: [web]
`))
	if err != nil {
		log.Fatal(err)
	}
	err = doc.ApplyPatch([]byte(`[
		{"op": "test", "path": "/name", "value": "api"},
		{"op": "replace", "path": "/replicas", "value": 3},
		{"op": "add", "path": "/tags/-", "value": "edge"},
		{"op": "copy", "from": "/name", "path": "/title"}
	]`))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))

	// A failing test rejects the whole patch.
	err = doc.ApplyPatch([]byte(`[
		{"op": "replace", "path": "/replicas", "value": 9},
		{"op": "test", "path": "/name", "value": "web"}
	]`))
	var ee *oneyaml.EditError
	fmt.Println(errors.Is(err, oneyaml.ErrTestFailed), errors.As(err, &ee) && ee.Index == 1)
	fmt.Print(string(doc.Bytes()))
	// Output:
	// # service
	// name: api
	// replicas: 3   # raised by the release job
	// tags: [web, edge]
	// title: api
	// true true
	// # service
	// name: api
	// replicas: 3   # raised by the release job
	// tags: [web, edge]
	// title: api
}

// Apply takes the same operations as Go values.
func ExampleDocument_Apply() {
	doc, err := oneyaml.Parse([]byte("a: 1\nb: 2\n"))
	if err != nil {
		log.Fatal(err)
	}
	err = doc.Apply(
		oneyaml.PatchTest("/a", 1),
		oneyaml.PatchMove("/b", "/c"),
		oneyaml.PatchAdd("/d", []string{"x", "y"}),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// a: 1
	// c: 2
	// d:
	//   - x
	//   - y
}

// ApplyMergePatch applies an RFC 7396 merge patch: null deletes a member, an
// object merges into the mapping it meets, and anything else replaces.
func ExampleDocument_ApplyMergePatch() {
	doc, err := oneyaml.Parse([]byte(`# settings
log:
  level: info   # default
  file: app.log
features: [a, b]
`))
	if err != nil {
		log.Fatal(err)
	}
	err = doc.ApplyMergePatch([]byte(`{"log": {"level": "debug", "file": null}, "features": ["c"], "debug": true}`))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// # settings
	// log:
	//   level: debug   # default
	// features: [c]
	// debug: true
}
