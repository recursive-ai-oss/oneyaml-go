package yaml_test

import (
	"encoding/json"
	"fmt"

	"oneyaml.dev/go/compat/k8syaml"
)

type Container struct {
	Name  string            `json:"name"`
	Image string            `json:"image"`
	Args  []string          `json:"args,omitempty"`
	Env   map[string]string `json:"env,omitempty"`
}

// Unmarshal reads YAML through the json tags of the target. Scalars read into
// string fields become strings, so a version written as 1.10 or a flag written
// as yes does not make the decoding fail.
func ExampleUnmarshal() {
	var c Container
	err := yaml.Unmarshal([]byte(`
name: web
image: nginx:1.25
args: [--port, 8080, --verbose]
env:
  DEBUG: yes
  RETRIES: 3
unknown: ignored
`), &c)
	fmt.Println(err)
	fmt.Printf("%s %s %q %v\n", c.Name, c.Image, c.Args, c.Env)
	// Output:
	// <nil>
	// web nginx:1.25 ["--port" "8080" "--verbose"] map[DEBUG:true RETRIES:3]
}

// UnmarshalStrict also rejects duplicate keys and fields the target does not have.
func ExampleUnmarshalStrict() {
	var c Container
	err := yaml.UnmarshalStrict([]byte("name: web\nimage: nginx\nreplicas: 3\n"), &c)
	fmt.Println(err != nil)
	// Output:
	// true
}

// Options for the JSON decoder keep integers above 2^53 exact.
func ExampleUnmarshal_useNumber() {
	var v map[string]any
	useNumber := func(d *json.Decoder) *json.Decoder { d.UseNumber(); return d }
	if err := yaml.Unmarshal([]byte("id: 12345678901234567890\n"), &v, useNumber); err != nil {
		fmt.Println(err)
	}
	fmt.Println(v["id"])
	// Output:
	// 12345678901234567890
}

// Marshal writes a value through its json tags. Keys come out sorted, and
// sequences are not indented below their key.
func ExampleMarshal() {
	out, err := yaml.Marshal(Container{
		Name:  "web",
		Image: "nginx:1.25",
		Args:  []string{"--port", "8080"},
		Env:   map[string]string{"DEBUG": "yes", "RETRIES": "3"},
	})
	fmt.Println(err)
	fmt.Print(string(out))
	// Output:
	// <nil>
	// args:
	// - --port
	// - "8080"
	// env:
	//   DEBUG: "yes"
	//   RETRIES: "3"
	// image: nginx:1.25
	// name: web
}

// YAMLToJSON turns a YAML 1.1 document into JSON, expanding merge keys and
// aliases.
func ExampleYAMLToJSON() {
	out, err := yaml.YAMLToJSON([]byte(`
defaults: &defaults
  cpu: 500m
  debug: no
service:
  <<: *defaults
  cpu: 1
`))
	fmt.Println(err)
	fmt.Println(string(out))
	// Output:
	// <nil>
	// {"defaults":{"cpu":"500m","debug":false},"service":{"cpu":1,"debug":false}}
}

func ExampleYAMLToJSONStrict() {
	_, err := yaml.YAMLToJSONStrict([]byte("a: 1\na: 2\n"))
	fmt.Println(err != nil)
	// Output:
	// true
}

// JSONToYAML writes JSON as YAML in the layout sigs.k8s.io/yaml uses.
func ExampleJSONToYAML() {
	out, err := yaml.JSONToYAML([]byte(`{"b":[1,2],"a":{"on":"yes"},"a10":1,"a2":2}`))
	fmt.Println(err)
	fmt.Print(string(out))
	// Output:
	// <nil>
	// a:
	//   "on": "yes"
	// a2: 2
	// a10: 1
	// b:
	// - 1
	// - 2
}

// DisallowUnknownFields is a JSONOpt that makes a field the target does not
// have an error.
func ExampleDisallowUnknownFields() {
	var c Container
	err := yaml.Unmarshal([]byte("name: web\nreplicas: 3\n"), &c, yaml.DisallowUnknownFields)
	fmt.Println(err != nil)
	// Output:
	// true
}
