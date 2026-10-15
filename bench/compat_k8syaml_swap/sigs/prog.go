// Package swapprog is a small program written against sigs.k8s.io/yaml. The copy
// in the sibling directory differs from this file in the import line only.
package swapprog

import (
	"encoding/json"
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

// Config is the kind of type a controller reads from a file.
type Config struct {
	Name     string            `json:"name"`
	Replicas int               `json:"replicas"`
	Labels   map[string]string `json:"labels,omitempty"`
	Image    *Image            `json:"image,omitempty"`
	Ports    []int             `json:"ports"`
	Debug    bool              `json:"debug"`
}

// Image names a container image.
type Image struct {
	Repo string `json:"repo"`
	Tag  string `json:"tag"`
}

const input = `
name: demo
replicas: 3
labels:
  app: demo
  version: 1.10
  canary: yes
image: {repo: registry.example/demo, tag: 2}
ports: [80, 443]
debug: on
unknown: ignored
`

// Run exercises every function of the package and returns a transcript.
func Run() (string, error) {
	var out strings.Builder

	var c Config
	if err := yaml.Unmarshal([]byte(input), &c); err != nil {
		return "", err
	}
	fmt.Fprintf(&out, "unmarshal: %s %d %v %v %v image=%+v\n", c.Name, c.Replicas, c.Labels, c.Ports, c.Debug, *c.Image)

	err := yaml.UnmarshalStrict([]byte(input), &Config{})
	fmt.Fprintf(&out, "strict (unknown field): error=%t\n", err != nil)

	var withOpt Config
	var opt yaml.JSONOpt = yaml.DisallowUnknownFields
	err = yaml.Unmarshal([]byte(input), &withOpt, opt)
	fmt.Fprintf(&out, "disallow unknown fields: error=%t\n", err != nil)

	var generic map[string]interface{}
	useNumber := func(d *json.Decoder) *json.Decoder { d.UseNumber(); return d }
	if err := yaml.Unmarshal([]byte("big: 12345678901234567890\nlist: [1, 2.5, x]\n"), &generic, useNumber); err != nil {
		return "", err
	}
	fmt.Fprintf(&out, "generic: %v\n", generic)

	y, err := yaml.Marshal(c)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&out, "marshal:\n%s", y)

	j, err := yaml.YAMLToJSON([]byte(input))
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&out, "json: %s\n", j)

	if _, err := yaml.YAMLToJSONStrict([]byte("a: 1\na: 2\n")); err == nil {
		return "", fmt.Errorf("YAMLToJSONStrict accepted a duplicate key")
	}

	back, err := yaml.JSONToYAML(j)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&out, "yaml:\n%s", back)

	return out.String(), nil
}
