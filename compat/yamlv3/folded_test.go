package yaml_test

import (
	"testing"

	yaml "oneyaml.dev/go/compat/yamlv3"
)

// Every short string over a small alphabet of text, white space and line breaks,
// written as a folded scalar at the top level and in a mapping, reads back whole.
func TestFoldedScalarExhaustive(t *testing.T) {
	if testing.Short() {
		t.Skip("exhaustive")
	}
	alphabet := []byte{'a', 'b', ' ', '\t', '\n'}
	const maxLen = 7
	var bad, total int
	var walk func(s []byte)
	check := func(v string) {
		for _, nested := range []bool{false, true} {
			val := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.FoldedStyle, Value: v}
			root := val
			if nested {
				root = &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "k"}, val}}
			}
			total++
			out, err := yaml.Marshal(root)
			if err != nil {
				bad++
				t.Errorf("%q: %v", v, err)
				continue
			}
			var got string
			if nested {
				var m map[string]string
				err = yaml.Unmarshal(out, &m)
				got = m["k"]
			} else {
				err = yaml.Unmarshal(out, &got)
			}
			if err != nil || got != v {
				bad++
				if bad <= 10 {
					t.Errorf("%q (nested %v): wrote %q, read back %q (%v)", v, nested, out, got, err)
				}
			}
		}
	}
	walk = func(s []byte) {
		if len(s) > 0 {
			check(string(s))
		}
		if len(s) == maxLen {
			return
		}
		for _, c := range alphabet {
			walk(append(s, c))
		}
	}
	walk(nil)
	if bad > 0 {
		t.Errorf("%d of %d folded scalars did not read back", bad, total)
	}
}
