package oneyaml_test

import (
	"fmt"
	"log"

	oneyaml "oneyaml.dev/go"
	yaml "oneyaml.dev/go/compat/yamlv3"
)

// legacySize was written for yaml.v3: its UnmarshalYAML takes a *yaml.Node.
// The oneyaml package does not call that form; it reports it, so the type is
// never filled by its fields without a word.
type legacySize struct{ Bytes int }

func (s *legacySize) UnmarshalYAML(n *yaml.Node) error { return n.Decode(&s.Bytes) }

// Size is the same type moved to the oneyaml package: the method takes a
// oneyaml.Node, which has the same Decode method.
type Size struct{ Bytes int }

func (s *Size) UnmarshalYAML(n oneyaml.Node) error { return n.Decode(&s.Bytes) }

func ExampleUnmarshaler_fromYAMLv3() {
	src := []byte("limit: 512\n")

	var before struct {
		Limit legacySize `yaml:"limit"`
	}
	err := oneyaml.Unmarshal(src, &before)
	fmt.Println(err)

	var after struct {
		Limit Size `yaml:"limit"`
	}
	if err := oneyaml.Unmarshal(src, &after); err != nil {
		log.Fatal(err)
	}
	fmt.Println(after.Limit.Bytes)
	// Output:
	// oneyaml: line 1, column 8: /limit: oneyaml_test.legacySize has an UnmarshalYAML method that is neither UnmarshalYAML(oneyaml.Node) error nor UnmarshalYAML(func(any) error) error; change its signature, or remove it to fill the value by its fields
	// 512
}
