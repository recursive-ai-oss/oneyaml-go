package oneyaml_test

import (
	"fmt"
	"log"
	"strings"

	oneyaml "oneyaml.dev/go"
)

// Tags reads either a sequence or a comma-separated string. It uses the
// UnmarshalYAML form of yaml.v2, yaml.v3 and goccy, so code written for them
// keeps working.
type Tags []string

func (t *Tags) UnmarshalYAML(unmarshal func(any) error) error {
	var list []string
	if err := unmarshal(&list); err == nil {
		*t = list
		return nil
	}
	var one string
	if err := unmarshal(&one); err != nil {
		return err
	}
	*t = strings.Split(one, ",")
	return nil
}

func ExampleUnmarshal_funcHook() {
	var doc struct {
		A Tags `yaml:"a"`
		B Tags `yaml:"b"`
	}
	if err := oneyaml.Unmarshal([]byte("a: [x, y]\nb: \"p,q,r\"\n"), &doc); err != nil {
		log.Fatal(err)
	}
	fmt.Println(doc.A, doc.B)
	// Output:
	// [x y] [p q r]
}
