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

// Reading into the Node of gopkg.in/yaml.v3 must not leave it empty without a
// word: yaml.v3 fills it, the adapter cannot, so it returns an error that names
// the type, as Marshal does.
func TestYAMLv3UnmarshalIntoForeignNodeIsRefused(t *testing.T) {
	var ref v3.Node
	if err := v3.Unmarshal([]byte("a: 1\n"), &ref); err != nil || ref.Kind != v3.DocumentNode {
		t.Fatalf("yaml.v3: %v %v", err, ref.Kind)
	}
	type holder struct {
		N  v3.Node
		P  *v3.Node
		Ns []v3.Node
	}
	cases := map[string]struct {
		in  string
		out any
	}{
		"node":      {"a: 1\n", &v3.Node{}},
		"ptrptr":    {"a: 1\n", new(*v3.Node)},
		"field":     {"n: {a: 1}\n", &holder{}},
		"ptrfield":  {"p: [1]\n", &holder{}},
		"slice":     {"ns: [x]\n", &holder{}},
		"map value": {"k: v\n", &map[string]v3.Node{}},
	}
	for name, c := range cases {
		err := yaml.Unmarshal([]byte(c.in), c.out)
		if err == nil {
			t.Errorf("%s: want an error, got none", name)
			continue
		}
		if !strings.Contains(err.Error(), "yaml.v3") || !strings.Contains(err.Error(), "Node") {
			t.Errorf("%s: error does not name the type: %v", name, err)
		}
	}
	// A null value leaves a pointer to the foreign node nil.
	var h holder
	if err := yaml.Unmarshal([]byte("p: null\n"), &h); err != nil || h.P != nil {
		t.Errorf("null into *Node: %v %v", err, h.P)
	}
	// The decoder refuses it as well.
	var n v3.Node
	if err := yaml.NewDecoder(strings.NewReader("a: 1\n")).Decode(&n); err == nil {
		t.Errorf("Decode into a Node: want an error")
	}
}
