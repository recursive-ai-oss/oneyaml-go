package oneyaml_test

import (
	"errors"
	"fmt"

	"oneyaml.dev/go"
)

// A value that does not fit is skipped and the rest of the document is still
// read. Every mismatch is reported, with its position.
func ExampleTypeError() {
	var cfg struct {
		Port    int
		Workers int
		Name    string
	}
	err := oneyaml.Unmarshal([]byte("port: x\nworkers: y\nname: api\n"), &cfg)

	var te *oneyaml.TypeError
	if errors.As(err, &te) {
		for _, e := range te.All() {
			fmt.Printf("%d:%d %s\n", e.Line, e.Column, e.Path)
		}
	}
	fmt.Println(cfg.Name)
	// Output:
	// 1:7 /port
	// 2:10 /workers
	// api
}

// ErrUnknownField matches a key that the target struct does not have.
func ExampleErrUnknownField() {
	var cfg struct{ Port int }
	err := oneyaml.Unmarshal([]byte("prot: 80\n"), &cfg, oneyaml.KnownFields())
	fmt.Println(errors.Is(err, oneyaml.ErrUnknownField))
	// Output: true
}

// A duplicate key is a SyntaxError that names the key and where it first
// appeared.
func ExampleSyntaxError_duplicateKey() {
	var v any
	err := oneyaml.Unmarshal([]byte("name: a\nport: 1\nname: b\n"), &v)

	var se *oneyaml.SyntaxError
	if errors.As(err, &se) && se.Category == oneyaml.CategoryDuplicateKey {
		fmt.Printf("%q at line %d, first at line %d\n", se.Key, se.Line, se.FirstLine)
	}
	// Output: "name" at line 3, first at line 1
}
