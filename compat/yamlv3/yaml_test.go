package yaml_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	yaml "oneyaml.dev/go/compat/yamlv3"
)

type config struct {
	Name     string            `yaml:"name"`
	Port     int               `yaml:"port"`
	Debug    bool              `yaml:"debug"`
	Mode     int               `yaml:"mode"`
	Tags     []string          `yaml:"tags,flow"`
	Extra    map[string]string `yaml:",inline"`
	Optional string            `yaml:"optional,omitempty"`
}

func TestUnmarshalYAML11Forms(t *testing.T) {
	src := "name: svc\nport: 8_080\ndebug: yes\nmode: 0755\ntags: [a, b]\nunknown: x\n"
	var c config
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatal(err)
	}
	if c.Port != 8080 || !c.Debug || c.Mode != 0o755 || c.Extra["unknown"] != "x" || len(c.Tags) != 2 {
		t.Fatalf("got %+v", c)
	}
	var any1 interface{}
	if err := yaml.Unmarshal([]byte("a: yes\nb: 2001-01-02\nc: 0o17\nd: .5\n"), &any1); err != nil {
		t.Fatal(err)
	}
	m := any1.(map[string]interface{})
	if m["a"] != "yes" || m["c"] != 15 || m["d"] != 0.5 {
		t.Fatalf("got %#v", m)
	}
	if tm, ok := m["b"].(time.Time); !ok || tm.Year() != 2001 {
		t.Fatalf("timestamp: %#v", m["b"])
	}
}

func TestMergeKeys(t *testing.T) {
	var v map[string]map[string]int
	src := "base: &b {x: 1, y: 2}\nuse:\n  <<: *b\n  y: 3\n"
	if err := yaml.Unmarshal([]byte(src), &v); err != nil {
		t.Fatal(err)
	}
	if v["use"]["x"] != 1 || v["use"]["y"] != 3 {
		t.Fatalf("got %v", v)
	}
}

func TestTypeErrorsAreCollected(t *testing.T) {
	var v struct {
		A int
		B int
		C string
	}
	err := yaml.Unmarshal([]byte("a: x\nb: 2\nc: ok\n"), &v)
	var te *yaml.TypeError
	if !errors.As(err, &te) || len(te.Errors) != 1 || !strings.Contains(te.Errors[0], "line 1: cannot unmarshal !!str `x` into int") {
		t.Fatalf("err = %v", err)
	}
	if v.B != 2 || v.C != "ok" {
		t.Fatalf("decoding stopped: %+v", v)
	}
}

func TestMarshalLayout(t *testing.T) {
	got, err := yaml.Marshal(map[string]interface{}{
		"b": []interface{}{1, map[string]interface{}{"x": "yes"}},
		"a": nil, "c": map[string]int(nil), "d": []int(nil), "e": 1.5, "f": "multi\nline\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "a: null\nb:\n    - 1\n    - x: \"yes\"\nc: {}\nd: []\ne: 1.5\nf: |\n    multi\n    line\n"
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEncoderIndentAndDocuments(t *testing.T) {
	var buf bytes.Buffer
	e := yaml.NewEncoder(&buf)
	e.SetIndent(2)
	if err := e.Encode(map[string][]int{"a": {1}}); err != nil {
		t.Fatal(err)
	}
	if err := e.Encode("second"); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if want := "a:\n  - 1\n---\nsecond\n"; buf.String() != want {
		t.Fatalf("got %q want %q", buf.String(), want)
	}
}

func TestDecoderStreamAndKnownFields(t *testing.T) {
	dec := yaml.NewDecoder(strings.NewReader("a: 1\n---\na: 2\n---\nb: 3\n"))
	dec.KnownFields(true)
	var out []int
	for {
		var v struct{ A int }
		err := dec.Decode(&v)
		if err == io.EOF {
			break
		}
		var te *yaml.TypeError
		if errors.As(err, &te) {
			if !strings.Contains(te.Errors[0], "field b not found in type") {
				t.Fatalf("err = %v", err)
			}
			out = append(out, -1)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, v.A)
	}
	if len(out) != 3 || out[0] != 1 || out[1] != 2 || out[2] != -1 {
		t.Fatalf("got %v", out)
	}
}

type upper string

func (u *upper) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	*u = upper(strings.ToUpper(s))
	return nil
}

type legacy struct{ N int }

func (l *legacy) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	l.N = len(s)
	return nil
}

type shout string

func (s shout) MarshalYAML() (interface{}, error) { return strings.ToUpper(string(s)), nil }

func TestHooks(t *testing.T) {
	var v struct {
		U upper
		L legacy
		P *upper
	}
	if err := yaml.Unmarshal([]byte("u: abc\nl: four\np: x\n"), &v); err != nil {
		t.Fatal(err)
	}
	if v.U != "ABC" || v.L.N != 4 || v.P == nil || *v.P != "X" {
		t.Fatalf("got %+v", v)
	}
	out, err := yaml.Marshal(struct{ S shout }{"hi"})
	if err != nil || string(out) != "s: HI\n" {
		t.Fatalf("got %q, %v", out, err)
	}
	boom := errors.New("boom")
	err = yaml.Unmarshal([]byte("u: abc\n"), &struct{ U failing }{})
	if !errors.Is(err, boom) && err == nil {
		t.Fatalf("hook error lost: %v", err)
	}
}

type failing struct{}

func (failing) UnmarshalYAML(*yaml.Node) error { return errors.New("boom") }

func TestNodeRoundTrip(t *testing.T) {
	src := "# header\nname: svc # inline\nlist:\n    - a\n    - b\n"
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		t.Fatalf("unexpected tree: %+v", doc)
	}
	m := doc.Content[0]
	if m.Content[0].Value != "name" || m.Content[0].HeadComment != "# header" || m.Content[1].LineComment != "# inline" {
		t.Fatalf("comments: %q %q", m.Content[0].HeadComment, m.Content[1].LineComment)
	}
	m.Content[1].Value = "other"
	out, err := yaml.Marshal(&doc)
	if err != nil {
		t.Fatal(err)
	}
	want := "# header\nname: other # inline\nlist:\n    - a\n    - b\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	var n yaml.Node
	if err := n.Encode(map[string]int{"a": 1}); err != nil || n.Kind != yaml.MappingNode || len(n.Content) != 2 {
		t.Fatalf("Encode: %v %+v", err, n)
	}
}

func TestNodeDecodeFollowsAliasesOutsideTheNode(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("base: &b {x: 1}\nuse: {<<: *b, y: 2}\n"), &doc); err != nil {
		t.Fatal(err)
	}
	use := doc.Content[0].Content[3]
	var got map[string]int
	if err := use.Decode(&got); err != nil || got["x"] != 1 || got["y"] != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestNonPointerMapTarget(t *testing.T) {
	m := map[string]int{}
	if err := yaml.Unmarshal([]byte("a: 1\n"), m); err != nil || m["a"] != 1 {
		t.Fatalf("got %v, %v", m, err)
	}
	if err := yaml.Unmarshal([]byte("a: 1\n"), 5); err == nil {
		t.Fatal("want an error for a non-pointer target")
	}
}

func TestNoPanicOnUnsupportedValue(t *testing.T) {
	if _, err := yaml.Marshal(make(chan int)); err == nil {
		t.Fatal("want an error")
	}
	type bad struct {
		A int `yaml:"x"`
		B int `yaml:"x"`
	}
	if _, err := yaml.Marshal(bad{}); err == nil {
		t.Fatal("want an error for duplicate keys")
	}
	if err := yaml.Unmarshal([]byte("x: 1"), &bad{}); err == nil {
		t.Fatal("want an error for duplicate keys")
	}
}

func TestLimitsStillApply(t *testing.T) {
	var b strings.Builder
	b.WriteString("a: &a [x, x, x, x, x, x, x, x, x, x]\n")
	prev := "a"
	for i := 0; i < 12; i++ {
		name := string(rune('b' + i))
		b.WriteString(name + ": &" + name + " [*" + prev + ", *" + prev + ", *" + prev + ", *" + prev + ", *" + prev + ", *" + prev + ", *" + prev + ", *" + prev + ", *" + prev + "]\n")
		prev = name
	}
	var v interface{}
	if err := yaml.Unmarshal([]byte(b.String()), &v); err == nil {
		t.Fatal("alias bomb was expanded")
	}
}

// A comment after a dash that has no value is the head comment of the next item,
// as yaml.v3 reads it.
func TestCommentAfterEmptyItemHeadsNext(t *testing.T) {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("- # c\n- x\n"), &n); err != nil {
		t.Fatal(err)
	}
	items := n.Content[0].Content
	if len(items) != 2 || items[0].HeadComment != "" || items[0].LineComment != "" || items[1].HeadComment != "# c" {
		t.Errorf("comments: %q %q / %q", items[0].HeadComment, items[0].LineComment, items[1].HeadComment)
	}
}

// A dash with no space after it starts a plain scalar, so what follows is not a
// comment; and a comment is never written twice when comment lines sit above a
// dash line.
func TestDashLineCommentsStayWhole(t *testing.T) {
	for _, c := range []struct{ in, out string }{
		{"- -# c\n- x\n", "- -# c\n- x\n"},
		{"# a\n- # c\n- x\n", "# a\n-\n# c\n- x\n"},
		{"- # c\n- x\n", "-\n# c\n- x\n"},
		{"top:\n  # a\n\n  - # c\n  - x\n", "top:\n    -\n    # c\n    - x\n# a\n"},
	} {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(c.in), &n); err != nil {
			t.Fatal(err)
		}
		out, err := yaml.Marshal(&n)
		if err != nil || string(out) != c.out {
			t.Errorf("%q: wrote %q (%v), want %q", c.in, out, err, c.out)
		}
	}
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("- -# c\n- x\n"), &n); err != nil {
		t.Fatal(err)
	}
	for _, it := range n.Content[0].Content {
		if it.HeadComment != "" || it.LineComment != "" || it.FootComment != "" {
			t.Errorf("item %q has comments %q %q %q", it.Value, it.HeadComment, it.LineComment, it.FootComment)
		}
	}
}

// A document with a "---" marker and no content has nothing to hold its comments,
// so the comment on the marker line and the ones after it are its foot comment, as
// yaml.v3 reads it.
func TestCommentsOfEmptyDocumentAreItsFoot(t *testing.T) {
	for _, c := range []struct{ in, foot string }{
		{"--- # c\n", "# c"},
		{"--- # c", "# c"},
		{"--- # c\n\n# d\n", "# c\n\n# d"},
		{"--- # c\n# d\n", "# c\n# d"},
		{"# a\n--- # c\n", "# a\n# c"},
	} {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(c.in), &n); err != nil {
			t.Fatal(err)
		}
		r := n.Content[0]
		if n.FootComment != c.foot || r.HeadComment != "" || r.FootComment != "" || r.LineComment != "" {
			t.Errorf("%q: foot %q, root comments %q %q %q", c.in, n.FootComment, r.HeadComment, r.LineComment, r.FootComment)
		}
	}
}

// commentsOf lists the comments of every node that has one, in document order,
// as "value H=... L=... F=...".
func commentsOf(n *yaml.Node) []string {
	var out []string
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.HeadComment != "" || n.LineComment != "" || n.FootComment != "" {
			out = append(out, fmt.Sprintf("%q H=%q L=%q F=%q", n.Value, n.HeadComment, n.LineComment, n.FootComment))
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(n)
	return out
}

// The expected lists are what yaml.v3 itself reads from the same text.
func TestCommentPlacementLikeYAMLv3(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"marker comment heads the first key", "--- # c\na: 1\n", `"a" H="# c" L="" F=""`},
		{"comments above a tagged marker", "# top\n--- !!map\na: 1\n", `"a" H="# top" L="" F=""`},
		{"item comment then a blank line", "- # c\n\n- b\n", `"b" H="# c\n" L="" F=""`},
		{"dedent splits a foot from a head", "g:\n  p: {}\n  # f1\n  # f2\n# h\nq: 1\n",
			`"p" H="" L="" F="# f1\n# f2"` + "\n" + `"q" H="# h" L="" F=""`},
		{"a block after an empty value is the next head", "a:\n  # x\n\n# y\nb: 1\n", `"b" H="# x\n\n# y" L="" F=""`},
		{"blocks across a blank line: foot, then head", "a: 1\n# f\n\n# h\nb: 2\n",
			`"a" H="" L="" F="# f"` + "\n" + `"b" H="# h" L="" F=""`},
		{"a value after a comment line has the head", "key: # k\n  # lines\n  value\n",
			`"key" H="" L="# k" F=""` + "\n" + `"value" H="# lines" L="" F=""`},
		{"comment after an anchor goes to the first key", "top: &n # c\n  k: v\n", `"k" H="" L="# c" F=""`},
		{"comment after a tag goes to the first key", "- !!map # c\n  foo: bar\n", `"foo" H="" L="# c" F=""`},
		{"a key written alone after a question mark", "? k # c\n", `"k" H="" L="# c" F=""`},
		{"comment in a flow sequence", "[a,\n# c\nb]\n", `"b" H="# c" L="" F=""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n yaml.Node
			if err := yaml.Unmarshal([]byte(tc.src), &n); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(commentsOf(&n), "\n"); got != tc.want {
				t.Fatalf("%q:\n got %s\nwant %s", tc.src, got, tc.want)
			}
		})
	}
}

// A foot comment and the head comment of the next entry stay a blank line apart,
// and a value's head comment is written below its line, as yaml.v3 writes them.
func TestCommentLayoutLikeYAMLv3(t *testing.T) {
	for _, src := range []string{
		"a: 1\n# f\n\n# h\nb: 2\n",
		"key: value # c\n# lines\n",
	} {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(src), &n); err != nil {
			t.Fatal(err)
		}
		out, err := yaml.Marshal(&n)
		if err != nil || string(out) != src {
			t.Fatalf("%q written as %q (%v)", src, out, err)
		}
	}
	// The tree yaml.v3 reads from "key:  # c\n  # lines\n  value" puts the head
	// comment on the value.
	n := yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "key", LineComment: "# c"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value", HeadComment: "# lines"},
	}}}}
	out, err := yaml.Marshal(&n)
	if want := "key: value # c\n# lines\n"; err != nil || string(out) != want {
		t.Fatalf("got %q (%v), want %q", out, err, want)
	}
}
