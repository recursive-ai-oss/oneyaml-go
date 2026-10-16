package bench

import (
	"strings"
	"testing"

	v3 "gopkg.in/yaml.v3"

	yaml "oneyaml.dev/go/compat/yamlv3"
)

// fhPort is a type from a package written for yaml.v3: its hook takes the real
// *yaml.v3.Node and rejects a bad fhPort.
type fhPort struct {
	N    int `yaml:"n"`
	Name string
}

func (p *fhPort) UnmarshalYAML(n *v3.Node) error {
	type plain fhPort
	var q plain
	if err := n.Decode(&q); err != nil {
		return err
	}
	if q.N > 65535 {
		return errFhPort
	}
	*p = fhPort(q)
	return nil
}

type portErr string

func (e portErr) Error() string { return string(e) }

const errFhPort = portErr("fhPort out of range")

type foreignFunc struct{ A int }

func (f *foreignFunc) UnmarshalYAML(x int) error { return nil }

func TestYAMLv3ForeignNodeHookIsRefused(t *testing.T) {
	in := []byte("n: 70000\nname: x\n")
	// yaml.v3 runs the hook and its validation.
	var ref fhPort
	if err := v3.Unmarshal(in, &ref); err != errFhPort {
		t.Fatalf("yaml.v3: %v", err)
	}
	for name, out := range map[string]any{
		"direct": new(fhPort),
		"field":  new(struct{ P fhPort }),
		"ptr":    new(struct{ P *fhPort }),
		"slice":  new([]fhPort),
	} {
		doc := in
		switch name {
		case "field", "ptr":
			doc = []byte("p:\n  n: 70000\n")
		case "slice":
			doc = []byte("- n: 70000\n")
		}
		err := yaml.Unmarshal(doc, out)
		if err == nil || !strings.Contains(err.Error(), "UnmarshalYAML") || !strings.Contains(err.Error(), "fhPort") || !strings.Contains(err.Error(), "gopkg.in/yaml.v3") {
			t.Errorf("%s: want an error naming the type, method and package, got %v", name, err)
		}
	}
	if p := new(fhPort); yaml.Unmarshal(in, p) == nil || p.N != 0 {
		t.Errorf("the value was filled field by field: %+v", p)
	}
	// Any other signature is refused as well.
	if err := yaml.Unmarshal([]byte("a: 1"), new(foreignFunc)); err == nil || !strings.Contains(err.Error(), "foreignFunc") {
		t.Errorf("odd signature: %v", err)
	}
	// A null value does not call a hook, as in yaml.v3.
	if err := yaml.Unmarshal([]byte("p: null"), new(struct{ P *fhPort })); err != nil {
		t.Errorf("null: %v", err)
	}
}

// MarshalYAML has the same shape in yaml.v3 and in the adapter, so a type
// written for yaml.v3 is written through it unchanged.
func TestYAMLv3MarshalHookOfOtherPackageShape(t *testing.T) {
	b, err := yaml.Marshal(struct{ P marshalOK }{})
	if err != nil || string(b) != "p: ok\n" {
		t.Errorf("%q %v", b, err)
	}
}

type marshalOK struct{}

func (marshalOK) MarshalYAML() (interface{}, error) { return "ok", nil }
