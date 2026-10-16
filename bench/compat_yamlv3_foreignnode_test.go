package bench

import (
	"strings"
	"testing"

	v3 "gopkg.in/yaml.v3"

	yaml "oneyaml.dev/go/compat/yamlv3"
)

// nodeHook returns a gopkg.in/yaml.v3 Node from MarshalYAML. yaml.v3 writes it
// as the node; the adapter cannot read that type, so it must refuse rather than
// write its fields (kind, style, ...) as if it were a struct.
type nodeHook struct{}

func (nodeHook) MarshalYAML() (interface{}, error) {
	return &v3.Node{Kind: v3.ScalarNode, Tag: "!!str", Value: "from node", LineComment: "# c"}, nil
}

type nodeValueHook struct{}

func (nodeValueHook) MarshalYAML() (interface{}, error) {
	return v3.Node{Kind: v3.ScalarNode, Tag: "!!str", Value: "x"}, nil
}

func TestYAMLv3MarshalYAMLReturningForeignNodeIsRefused(t *testing.T) {
	if b, err := v3.Marshal(nodeHook{}); err != nil || string(b) != "from node # c\n" {
		t.Fatalf("yaml.v3: %q %v", b, err)
	}
	for name, v := range map[string]any{
		"pointer": nodeHook{},
		"value":   nodeValueHook{},
		"field":   struct{ P nodeHook }{},
		"slice":   []nodeHook{{}},
		"map":     map[string]nodeValueHook{"k": {}},
		"node":    &v3.Node{Kind: v3.ScalarNode, Tag: "!!str", Value: "x"},
		"nodeval": v3.Node{Kind: v3.ScalarNode, Tag: "!!str", Value: "x"},
	} {
		b, err := yaml.Marshal(v)
		if err == nil {
			t.Errorf("%s: want an error, got %q", name, b)
			continue
		}
		if !strings.Contains(err.Error(), "yaml.v3") || !strings.Contains(err.Error(), "Node") {
			t.Errorf("%s: error does not name the type: %v", name, err)
		}
	}
}
