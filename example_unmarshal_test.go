package oneyaml_test

import (
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	"oneyaml.dev/go"
)

func ExampleUnmarshal() {
	src := []byte(`# deploy settings
name: api
replicas: 2
ports: [80, 443]
labels:
  tier: web
`)
	var cfg struct {
		Name     string            `yaml:"name"`
		Replicas int               `yaml:"replicas"`
		Ports    []int             `yaml:"ports"`
		Labels   map[string]string `yaml:"labels"`
	}
	if err := oneyaml.Unmarshal(src, &cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Println(cfg.Name, cfg.Replicas, cfg.Ports, cfg.Labels)
	// Output: api 2 [80 443] map[tier:web]
}

func ExampleUnmarshal_generic() {
	var v any
	err := oneyaml.Unmarshal([]byte("a: 1\nb: [x, 2.5, yes, null]\n"), &v)
	fmt.Println(v, err)
	// Output: map[a:1 b:[x 2.5 yes <nil>]] <nil>
}

func ExampleUnmarshal_typeError() {
	var cfg struct {
		Port int `yaml:"port"`
	}
	err := oneyaml.Unmarshal([]byte("name: api\nport: eighty\n"), &cfg)
	var te *oneyaml.TypeError
	if errors.As(err, &te) {
		fmt.Printf("%s at line %d, column %d: %s\n", te.Path, te.Line, te.Column, te.Msg)
	}
	// Output: /port at line 2, column 7: cannot unmarshal !!str `eighty` into int
}

func ExampleKnownFields() {
	var cfg struct {
		Name string `yaml:"name"`
	}
	err := oneyaml.Unmarshal([]byte("name: api\nnmae: typo\n"), &cfg, oneyaml.KnownFields())
	var te *oneyaml.TypeError
	if errors.As(err, &te) {
		fmt.Println(te.Path, te.Line, te.Column)
	}
	// Output: /nmae 2 1
}

func ExampleMergeKeys() {
	src := []byte("base: &base {x: 1, y: 2}\nchild:\n  <<: *base\n  y: 3\n")
	var v map[string]map[string]int
	err := oneyaml.Unmarshal(src, &v, oneyaml.MergeKeys())
	fmt.Println(v["child"], err)
	// Output: map[x:1 y:3] <nil>
}

func ExampleLimits() {
	bomb := "a: &a [x, x, x, x]\nb: &b [*a, *a, *a, *a]\nc: [*b, *b, *b, *b]\n"
	var v any
	err := oneyaml.Unmarshal([]byte(bomb), &v, oneyaml.Limits(oneyaml.LimitSet{MaxAliasNodes: 20}))
	var le *oneyaml.LimitError
	if errors.As(err, &le) {
		fmt.Println(le.Limit, le.Max)
	}
	// Output: alias expansion nodes 20
}

func ExampleDecoder() {
	stream := "name: first\n---\nname: second\n---\nname: third\n"
	dec := oneyaml.NewDecoder(strings.NewReader(stream))
	for {
		var doc struct {
			Name string `yaml:"name"`
		}
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(doc.Name)
	}
	// Output:
	// first
	// second
	// third
}
