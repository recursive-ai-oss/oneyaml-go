package oneyaml_test

import (
	"fmt"
	"log"
	"os"

	oneyaml "oneyaml.dev/go"
)

// Strings that another reader could take for a number, boolean or null are
// quoted, so the text reads back as the same string everywhere.
func ExampleMarshal() {
	type Server struct {
		Name    string   `yaml:"name"`
		Port    int      `yaml:"port"`
		Enabled bool     `yaml:"enabled"`
		Tags    []string `yaml:"tags,omitempty"`
		Version string   `yaml:"version"`
		Notes   string   `yaml:"notes"`
	}
	out, err := oneyaml.Marshal(Server{
		Name:    "api",
		Port:    8080,
		Enabled: true,
		Tags:    []string{"web", "yes"},
		Version: "1.10",
		Notes:   "first line\nsecond line\n",
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Print(string(out))
	// Output:
	// name: api
	// port: 8080
	// enabled: true
	// tags:
	//   - web
	//   - 'yes'
	// version: '1.10'
	// notes: |
	//   first line
	//   second line
}

// Map keys are written in sorted order, so output is the same on every run.
func ExampleMarshal_map() {
	out, err := oneyaml.Marshal(map[string]any{
		"zebra": 1,
		"apple": []any{1.5, nil, "x: y"},
		"empty": map[string]any{},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(out))
	// Output:
	// apple:
	//   - 1.5
	//   - null
	//   - 'x: y'
	// empty: {}
	// zebra: 1
}

func ExampleIndent() {
	out, err := oneyaml.Marshal(map[string]any{"a": map[string]any{"b": []any{1, 2}}}, oneyaml.Indent(4))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(out))
	// Output:
	// a:
	//     b:
	//         - 1
	//         - 2
}

// An Encoder writes several documents to one stream, separated by "---".
func ExampleEncoder() {
	enc := oneyaml.NewEncoder(os.Stdout)
	if err := enc.Encode(map[string]any{"kind": "first"}); err != nil {
		log.Fatal(err)
	}
	if err := enc.Encode([]int{1, 2}); err != nil {
		log.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		log.Fatal(err)
	}
	// Output:
	// kind: first
	// ---
	// - 1
	// - 2
}

type celsius float64

// MarshalYAML lets a type choose its own YAML form.
func (c celsius) MarshalYAML() (any, error) { return fmt.Sprintf("%.1fC", float64(c)), nil }

func ExampleMarshaler() {
	out, err := oneyaml.Marshal(map[string]any{"temp": celsius(21.5)})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(out))
	// Output:
	// temp: 21.5C
}
