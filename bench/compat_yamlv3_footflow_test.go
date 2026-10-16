package bench

import (
	"strings"
	"testing"

	yv3 "go.yaml.in/yaml/v3"
	compat "oneyaml.dev/go/compat/yamlv3"
)

// A comment line before the closing bracket of a flow collection is the foot
// comment of its last key or item, and the comments after a flow collection on
// the first line of the text are the document's foot. The shim reads them where
// yaml.v3 does, so a read-then-write keeps them and writes the same text.
var footFlowCases = []string{
	"[a, b,\n# foot\n]\n",
	"[a, b\n# foot\n]\n",
	"[a, b, # l\n# foot\n]\n",
	"[a, b,\n# foot\n\n]\n",
	"[a, b,\n# foot\n# two\n]\n",
	"{a: 1,\n# foot\n}\n",
	"{a: 1, b: 2\n# foot\n}\n",
	"{a: 1,\n# foot\n# two\n\n# three\n}\n",
	"[a: 1,\n# foot\n]\n",
	"[[a,\n# f\n]]\n",
	"[[a,\n# f\n],\n# g\n]\n",
	"{a: [1,\n# f\n]}\n",
	"k: [a,\n  # foot\n  ]\n",
	"- [a,\n  # f\n  ]\n- x\n",
	"[a, b]\n# foot\n",
	"{a: 1}\n# foot\n",
	"a: [1, 2]\n# foot\n",
	"a: &x [1]\n# foot\n",
	"a: [1] # l\n# foot\n",
	"a: [1]\n# foot\n# two\n",
	"a: {b: [1]}\n# foot\n",
	"- - [1]\n# foot\n",
	// Not on the first line: the key or item keeps the foot.
	"a: 1\nb: [1]\n# foot\n",
	"# h\na: [1]\n# foot\n",
	"a: [\n 1]\n# foot\n",
	"[a, b,\n# foot\n\n\n]\n",
	"a: 1\n# foot\n",
}

func footsOf(n *yv3.Node, d string, out *strings.Builder) {
	if n.FootComment != "" {
		out.WriteString(d + "[" + n.Value + "]/" + n.FootComment + "\n")
	}
	for _, c := range n.Content {
		footsOf(c, d+".", out)
	}
}

func footsOfCompat(n *compat.Node, d string, out *strings.Builder) {
	if n.FootComment != "" {
		out.WriteString(d + "[" + n.Value + "]/" + n.FootComment + "\n")
	}
	for _, c := range n.Content {
		footsOfCompat(c, d+".", out)
	}
}

func TestCompatV3FlowFootRead(t *testing.T) {
	for _, in := range footFlowCases {
		var y yv3.Node
		var c compat.Node
		if err := yv3.Unmarshal([]byte(in), &y); err != nil {
			t.Fatalf("%q: yaml.v3: %v", in, err)
		}
		if err := compat.Unmarshal([]byte(in), &c); err != nil {
			t.Fatalf("%q: shim: %v", in, err)
		}
		var a, b strings.Builder
		footsOf(&y, "", &a)
		footsOfCompat(&c, "", &b)
		if a.String() != b.String() {
			t.Errorf("%q: foot comments\nyaml.v3: %s\nshim:    %s", in, a.String(), b.String())
		}
		yb, _ := yv3.Marshal(&y)
		cb, _ := compat.Marshal(&c)
		// Where the shim cannot read the text yaml.v3 writes (a closing bracket at the
		// start of a line inside a block collection) the shim indents it, and the
		// bytes differ on purpose; the foot comments must still match.
		if compat.Unmarshal(yb, new(compat.Node)) != nil {
			continue
		}
		if string(yb) != string(cb) {
			t.Errorf("%q: written\nyaml.v3: %q\nshim:    %q", in, yb, cb)
		}
	}
}
