package oneyaml_test

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	oneyaml "oneyaml.dev/go"
)

// Level is written for encoding/json: it reads and writes the words "low" and
// "high" and nothing else.
type Level int

func (l *Level) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	switch strings.ToLower(s) {
	case "low":
		*l = 1
	case "high":
		*l = 2
	default:
		return fmt.Errorf("unknown level %q", s)
	}
	return nil
}

func (l Level) MarshalJSON() ([]byte, error) {
	return json.Marshal([]string{"none", "low", "high"}[l])
}

// A type that implements json.Unmarshaler or json.Marshaler and no YAML hook is
// read and written through that hook: the YAML value becomes JSON text first.
func ExampleUnmarshal_jsonHook() {
	var cfg struct {
		Level Level `yaml:"level"`
	}
	if err := oneyaml.Unmarshal([]byte("level: high\n"), &cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Println(cfg.Level)

	out, err := oneyaml.Marshal(cfg)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(out))

	// The error from UnmarshalJSON comes back inside a *TypeError.
	err = oneyaml.Unmarshal([]byte("level: loud\n"), &cfg)
	fmt.Println(err)
	// Output:
	// 2
	// level: high
	// oneyaml: line 1, column 8: /level: cannot unmarshal "loud" into oneyaml_test.Level: unknown level "loud"
}
