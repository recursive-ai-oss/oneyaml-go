package oneyaml_test

import (
	"fmt"
	"log"

	oneyaml "oneyaml.dev/go"
)

func ExampleNode_ExplicitTag() {
	doc, err := oneyaml.Parse([]byte("%TAG !e! tag:example.com,2000:\n--- !e!point\nx: !!str 1\ny: 2\n"))
	if err != nil {
		log.Fatal(err)
	}
	root := doc.Root()
	tag, ok := root.ExplicitTag()
	fmt.Println(tag, ok, root.TagAsWritten())
	x, _ := root.Lookup("x")
	fmt.Println(x.ExplicitTag())
	y, _ := root.Lookup("y")
	// Tag gives the tag the Core schema assigns; ExplicitTag says none was written.
	fmt.Println(y.Tag())
	fmt.Println(y.ExplicitTag())
	// Output:
	// tag:example.com,2000:point true !e!point
	// tag:yaml.org,2002:str true
	// tag:yaml.org,2002:int
	//  false
}

func ExampleDocument_TagDirectives() {
	doc, err := oneyaml.Parse([]byte("%YAML 1.2\n%TAG !e! tag:example.com,2000:\n---\na: 1\n...\n---\nb: 2\n"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(doc.Doc(0).ExplicitStart(), doc.Doc(0).ExplicitEnd(), doc.Doc(0).TagDirectives())
	fmt.Println(doc.Doc(0).Version())
	fmt.Println(doc.Doc(1).ExplicitStart(), doc.Doc(1).ExplicitEnd(), doc.Doc(1).TagDirectives())
	fmt.Println(doc.Doc(1).Version())
	// Output:
	// true true [{!e! tag:example.com,2000:}]
	// 1 2 true
	// true false []
	// 0 0 false
}

func ExampleNode_Items() {
	doc, err := oneyaml.Parse([]byte("- a\n- [b, c]\n- {d: e}\n"))
	if err != nil {
		log.Fatal(err)
	}
	for item := range doc.Root().Items() {
		fmt.Println(item.Kind())
	}
	doc, err = oneyaml.Parse([]byte("x: 1\ny: [2]\n"))
	if err != nil {
		log.Fatal(err)
	}
	for k, v := range doc.Root().Entries() {
		fmt.Println(k.Value(), v.Kind())
	}
	// Output:
	// scalar
	// sequence
	// mapping
	// x scalar
	// y sequence
}
