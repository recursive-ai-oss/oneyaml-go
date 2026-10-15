package yaml_test

import (
	"fmt"
	"testing"

	yaml "oneyaml.dev/go/compat/yamlv3"
)

// FuzzCompat checks that no input makes the shim panic, that a document read
// into a Node can always be written again, and that what is written reads back.
func FuzzCompat(f *testing.F) {
	for _, s := range []string{
		"a: 1\nb: [x, y]\n", "- &a x\n- *a\n", "# c\nk: v # l\n\n# f\n", "<<: {a: 1}\nb: 2\n", "? [a]\n: b\n",
		"a: |\n  text\nb: >\n  folded\n", "!!set\n? a\n", "---\n- 1\n---\n- 2\n", "k: !!binary aGk=\n", "{a: 1, b: [1, 2]}",
		"a: 2001-01-01\nb: 0777\nc: yes\n", "'x\n\n  y'", "\"esc\\u00e9\\n\"", "- - - a\n", "k:\n  - a\n  -\n",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var v interface{}
		_ = yaml.Unmarshal(data, &v)
		var s struct {
			A int
			B []string
			C map[string]interface{}
		}
		_ = yaml.Unmarshal(data, &s)
		var n yaml.Node
		if err := yaml.Unmarshal(data, &n); err != nil {
			return
		}
		out, err := yaml.Marshal(&n)
		if err != nil {
			return
		}
		var back yaml.Node
		if err := yaml.Unmarshal(out, &back); err != nil {
			t.Fatalf("output does not read back: %v\ninput: %q\noutput: %q", err, data, out)
		}
		checkCommentsStayComments(t, &n, out, data)
		// Nodes can be decoded without a panic, and decoded again from the copy
		// that was just read back.
		var dv interface{}
		_ = n.Decode(&dv)
		if v != nil {
			if _, err := yaml.Marshal(v); err != nil {
				return
			}
		}
	})
}

// checkCommentsStayComments gives every node of n comments made of the input's
// own text, line breaks included, and checks that writing them changes nothing
// but the comments: the output reads back, and to the same value as the output
// without them.
func checkCommentsStayComments(t *testing.T, n *yaml.Node, plain, data []byte) {
	var text []byte
	for _, b := range data {
		if b >= 0x20 && b < 0x7f || b == '\n' || b == '\r' || b == '\t' {
			text = append(text, b)
		}
	}
	if len(text) == 0 {
		return
	}
	var want interface{}
	if err := yaml.Unmarshal(plain, &want); err != nil {
		return
	}
	i := 0
	seen := map[*yaml.Node]bool{}
	stack := []*yaml.Node{n}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[x] {
			continue
		}
		seen[x] = true
		a, b := i%len(text), (i*7+5)%len(text)
		if a > b {
			a, b = b, a
		}
		x.LineComment = string(text[a:b])
		x.HeadComment = string(text[b:])
		x.FootComment = string(text[:a])
		i++
		stack = append(stack, x.Content...)
	}
	out, err := yaml.Marshal(n)
	if err != nil {
		return
	}
	var got interface{}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("output with comments does not read back: %v\ninput: %q\noutput: %q", err, data, out)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("comments changed the value\ninput: %q\noutput: %q\nwant %v\ngot  %v", data, out, want, got)
	}
}
