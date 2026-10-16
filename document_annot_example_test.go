package oneyaml_test

import (
	"errors"
	"fmt"
	"log"

	oneyaml "oneyaml.dev/go"
)

// Rename changes a key where it stands. The value, the place in the mapping and
// the comments stay; a key that already exists is refused.
func ExampleDocument_Rename() {
	doc, err := oneyaml.Parse([]byte(`# chart values
image: nginx
replicas: 2 # scaled by the autoscaler
port: 80
`))
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.Rename("/replicas", "replicaCount"); err != nil {
		log.Fatal(err)
	}
	fmt.Print(doc)

	err = doc.Rename("/port", "image")
	fmt.Println(errors.Is(err, oneyaml.ErrKeyExists))
	// Output:
	// # chart values
	// image: nginx
	// replicaCount: 2 # scaled by the autoscaler
	// port: 80
	// true
}

// SetHeadComment writes comment lines above an entry at its indentation and
// replaces the ones that were there; an empty text removes them.
func ExampleDocument_SetHeadComment() {
	doc, err := oneyaml.Parse([]byte(`service:
  # old note
  port: 80
  host: example.org
`))
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.SetHeadComment("/service/port", "the public port"); err != nil {
		log.Fatal(err)
	}
	if err := doc.SetHeadComment("/service/host", "first line\nsecond line"); err != nil {
		log.Fatal(err)
	}
	fmt.Print(doc)

	if err := doc.SetHeadComment("/service/host", ""); err != nil {
		log.Fatal(err)
	}
	fmt.Print(doc)
	// Output:
	// service:
	//   # the public port
	//   port: 80
	//   # first line
	//   # second line
	//   host: example.org
	// service:
	//   # the public port
	//   port: 80
	//   host: example.org
}

// SetLineComment writes the comment after a value on its line, after the header
// of a block scalar, or after the colon of an entry whose value is a collection.
func ExampleDocument_SetLineComment() {
	doc, err := oneyaml.Parse([]byte(`replicas: 2
script: |
  make build
env:
  mode: prod
`))
	if err != nil {
		log.Fatal(err)
	}
	for ptr, text := range map[string]string{
		"/replicas": "scaled by the autoscaler",
		"/script":   "run by CI",
		"/env":      "per environment",
	} {
		if err := doc.SetLineComment(ptr, text); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Print(doc)

	replicas, _ := doc.Root().Lookup("replicas")
	fmt.Println(replicas.LineComment())
	// Output:
	// replicas: 2 # scaled by the autoscaler
	// script: | # run by CI
	//   make build
	// env: # per environment
	//   mode: prod
	// # scaled by the autoscaler
}

// A patch kept as a YAML file (a block list of operations) gives the same result
// as the JSON form.
func ExampleDocument_ApplyPatch_yaml() {
	doc, err := oneyaml.Parse([]byte("replicas: 2\nimage: nginx:1.25\n"))
	if err != nil {
		log.Fatal(err)
	}
	patch := `
- op: replace
  path: /replicas
  value: 5
- op: add
  path: /labels
  value:
    tier: web
`
	if err := doc.ApplyPatch([]byte(patch)); err != nil {
		log.Fatal(err)
	}
	fmt.Print(doc)
	// Output:
	// replicas: 5
	// image: nginx:1.25
	// labels:
	//   tier: web
}

// A Document prints as its text and a Node as its source text.
func ExampleDocument_String() {
	doc, err := oneyaml.Parse([]byte("name: api # the service\nports: [80, 443]\n"))
	if err != nil {
		log.Fatal(err)
	}
	ports, _ := doc.Root().Lookup("ports")
	fmt.Println(ports)
	fmt.Print(doc)
	// Output:
	// [80, 443]
	// name: api # the service
	// ports: [80, 443]
}
