package oneyaml_test

import (
	"errors"
	"fmt"
	"log"
	"strings"

	oneyaml "oneyaml.dev/go"
)

// Parse keeps the whole file. Set rewrites one value in its own style and leaves
// the comments, the blank lines and every other byte alone.
func ExampleParse() {
	src := []byte(`# deploy settings
name: api
replicas: 2   # raised by the release job
script: |
  make build
`)

	doc, err := oneyaml.Parse(src)
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.Set("/replicas", 3); err != nil {
		log.Fatal(err)
	}
	if err := doc.Set("/script", "make build\nmake test\n"); err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))

	var replicas int
	if err := doc.Get("/replicas", &replicas); err != nil {
		log.Fatal(err)
	}
	fmt.Println(replicas)
	// Output:
	// # deploy settings
	// name: api
	// replicas: 3   # raised by the release job
	// script: |
	//   make build
	//   make test
	// 3
}

// A string that cannot stay plain is quoted, a value added to a mapping goes at
// its end in the style of its siblings, and removing the last entry of a nested
// mapping leaves {}.
func ExampleDocument_Set() {
	doc, err := oneyaml.Parse([]byte("a: 'x'\nb:\n  c: 1\n"))
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.Set("/a", "true"); err != nil {
		log.Fatal(err)
	}
	if err := doc.Set("/d", "new"); err != nil {
		log.Fatal(err)
	}
	if err := doc.Remove("/b/c"); err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// a: 'true'
	// b: {}
	// d: 'new'
}

// Writing below an alias would change every other place that shares the anchored
// node, so it is refused and nothing changes.
func ExampleDocument_Set_alias() {
	doc, err := oneyaml.Parse([]byte("base: &b\n  x: 1\ncopy: *b\n"))
	if err != nil {
		log.Fatal(err)
	}
	err = doc.Set("/copy/x", 2)
	fmt.Println(errors.Is(err, oneyaml.ErrAliasWrite))
	err = doc.Set("/base/x", 2)
	fmt.Println(err)
	fmt.Print(string(doc.Bytes()))
	// Output:
	// true
	// <nil>
	// base: &b
	//   x: 2
	// copy: *b
}

// A stream of several documents is one Document; Doc addresses one of them.
func ExampleDocument_Doc() {
	doc, err := oneyaml.Parse([]byte("a: 1\n---\nb: 2\n"))
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.Doc(1).Set("/c", 3); err != nil {
		log.Fatal(err)
	}
	fmt.Println(doc.Len())
	fmt.Print(string(doc.Bytes()))
	// Output:
	// 2
	// a: 1
	// ---
	// b: 2
	// c: 3
}

// Remove deletes an entry together with the comment on its line, the comment
// directly above it and any comment inside its value. A comment above the next
// entry stays.
func ExampleDocument_Remove() {
	doc, err := oneyaml.Parse([]byte("a: 1\n# about b\nb: 2 # two\n# about c\nc: 3\n"))
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.Remove("/b"); err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// a: 1
	// # about c
	// c: 3
}

// Get reads a value by pointer; aliases are followed.
func ExampleDocument_Get() {
	doc, err := oneyaml.Parse([]byte("base: &b\n  port: 8080\nreplica: *b\n"))
	if err != nil {
		log.Fatal(err)
	}
	var port int
	if err := doc.Get("/replica/port", &port); err != nil {
		log.Fatal(err)
	}
	fmt.Println(port)
	// Output: 8080
}

// A Node is a read-only view of the document: kind, style, text, anchors, tags,
// comments and position.
func ExampleNode() {
	doc, err := oneyaml.Parse([]byte("# the name\nname: api # service\nitems: [1, two]\n"))
	if err != nil {
		log.Fatal(err)
	}
	root := doc.Root()
	name, _ := root.Lookup("name")
	line, col := name.Position()
	fmt.Println(name.Value(), name.Tag(), name.LineComment(), line, col)
	fmt.Println(strings.TrimSpace(name.HeadComment()))
	items, _ := root.Lookup("items")
	fmt.Println(items.Style() == oneyaml.FlowStyle, items.Len(), items.Item(1).Value())
	// Output:
	// api tag:yaml.org,2002:str # service 2 7
	// # the name
	// true 2 two
}

// The quick start: read a file into a struct, then edit it in place. The edit
// changes one value and leaves the comments and every other byte as they were.
func Example_quickStart() {
	src := []byte(`# deploy settings
name: api
replicas: 2 # raised by the release job
`)
	// Read into a struct, as with encoding/json.
	var cfg struct {
		Name     string `yaml:"name"`
		Replicas int    `yaml:"replicas"`
	}
	if err := oneyaml.Unmarshal(src, &cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Println(cfg.Name, cfg.Replicas)

	// Edit in place: everything else, comments included, stays byte for byte.
	doc, err := oneyaml.Parse(src)
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.Set("/replicas", cfg.Replicas+1); err != nil {
		log.Fatal(err)
	}
	fmt.Print(doc)
	// Output:
	// api 2
	// # deploy settings
	// name: api
	// replicas: 3 # raised by the release job
}

func ExampleDocument_Decode() {
	doc, err := oneyaml.Parse([]byte("server:\n  host: example.org\n  ports: [80, 443]\n"))
	if err != nil {
		log.Fatal(err)
	}
	var cfg struct {
		Server struct {
			Host  string `yaml:"host"`
			Ports []int  `yaml:"ports"`
		} `yaml:"server"`
	}
	if err := doc.Decode(&cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Println(cfg.Server.Host, cfg.Server.Ports)
	// Output: example.org [80 443]
}

// Decode on a node reads that part of the document; an alias inside it is
// expanded even when its anchor is defined outside.
func ExampleNode_Decode() {
	doc, err := oneyaml.Parse([]byte("defaults: &d {retries: 3}\njob:\n  settings: *d\n"))
	if err != nil {
		log.Fatal(err)
	}
	job, _ := doc.Root().Lookup("job")
	var v map[string]map[string]int
	if err := job.Decode(&v); err != nil {
		log.Fatal(err)
	}
	fmt.Println(v)
	// Output: map[settings:map[retries:3]]
}

// Port reads itself from a node: a bare number, or a mapping with a number.
type Port int

func (p *Port) UnmarshalYAML(n oneyaml.Node) error {
	if n.Kind() == oneyaml.MappingNode {
		var m struct {
			Number int `yaml:"number"`
		}
		if err := n.Decode(&m); err != nil {
			return err
		}
		*p = Port(m.Number)
		return nil
	}
	var v int
	if err := n.Decode(&v); err != nil {
		return err
	}
	*p = Port(v)
	return nil
}

func ExampleUnmarshaler() {
	var cfg struct {
		Ports []Port `yaml:"ports"`
	}
	if err := oneyaml.Unmarshal([]byte("ports: [80, {number: 443}]\n"), &cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Println(cfg.Ports)
	// Output: [80 443]
}

// A failed Parse returns a nil Document; editing it reports ErrNilDocument
// instead of panicking.
func ExampleErrNilDocument() {
	d, err := oneyaml.Parse([]byte("a: ["))
	fmt.Println(err != nil, d == nil)
	fmt.Println(errors.Is(d.Set("/a", 1), oneyaml.ErrNilDocument))
	// Output:
	// true true
	// true
}
