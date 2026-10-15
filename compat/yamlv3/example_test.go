package yaml_test

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	yaml "oneyaml.dev/go/compat/yamlv3"
)

// Code written for yaml.v3 keeps working after the import path changes: plain
// scalars resolve the yaml.v3 way, so 0755 is an octal number and "yes" is
// accepted by a bool field.
func Example() {
	type Service struct {
		Name  string `yaml:"name"`
		Mode  int    `yaml:"mode"`
		Debug bool   `yaml:"debug"`
	}
	var s Service
	if err := yaml.Unmarshal([]byte("name: api\nmode: 0755\ndebug: yes\n"), &s); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("%+v\n", s)

	out, err := yaml.Marshal(s)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(out))
	// Output:
	// {Name:api Mode:493 Debug:true}
	// name: api
	// mode: 493
	// debug: true
}

// A type error does not stop the decode: every value that does not fit is
// listed, and the rest of the document is read.
func ExampleTypeError() {
	var v struct {
		Port  int
		Name  string
		Count int
	}
	err := yaml.Unmarshal([]byte("port: eighty\nname: api\ncount: x\n"), &v)
	var te *yaml.TypeError
	if e, ok := err.(*yaml.TypeError); ok {
		te = e
	}
	for _, msg := range te.Errors {
		fmt.Println(msg)
	}
	fmt.Println(v.Name)
	// Output:
	// line 1: cannot unmarshal !!str `eighty` into int
	// line 3: cannot unmarshal !!str `x` into int
	// api
}

// A Node keeps comments and styles, so a document can be changed and written
// again.
func ExampleNode() {
	src := "# service settings\nname: api # public name\nreplicas: 2\n"
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		fmt.Println(err)
		return
	}
	mapping := doc.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "replicas" {
			mapping.Content[i+1].Value = "3"
		}
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		log.Fatal(err)
	}
	os.Stdout.Write(out)
	// Output:
	// # service settings
	// name: api # public name
	// replicas: 3
}

// A decoder reads a stream of documents and reports io.EOF at the end.
func ExampleDecoder() {
	dec := yaml.NewDecoder(strings.NewReader("id: 1\n---\nid: 2\n"))
	for {
		var v struct{ ID int }
		if err := dec.Decode(&v); err == io.EOF {
			break
		} else if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(v.ID)
	}
	// Output:
	// 1
	// 2
}

// An encoder writes several documents, separated by "---".
func ExampleEncoder() {
	enc := yaml.NewEncoder(os.Stdout)
	enc.SetIndent(2)
	if err := enc.Encode(map[string][]int{"ports": {80, 443}}); err != nil {
		log.Fatal(err)
	}
	if err := enc.Encode("done"); err != nil {
		log.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		log.Fatal(err)
	}
	// Output:
	// ports:
	//   - 80
	//   - 443
	// ---
	// done
}
