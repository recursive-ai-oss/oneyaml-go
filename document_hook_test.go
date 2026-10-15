package oneyaml

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// view records what an Unmarshaler sees of its node.
type view struct {
	Kind     NodeKind
	Style    NodeStyle
	Tag      string
	Anchor   string
	Value    string
	Source   string
	Line     int
	Column   int
	Len      int
	Decoded  string
	Children []string
}

func (v *view) UnmarshalYAML(n Node) error {
	v.Kind, v.Style, v.Tag, v.Anchor, v.Value, v.Source, v.Len = n.Kind(), n.Style(), n.Tag(), n.Anchor(), n.Value(), n.Source(), n.Len()
	v.Line, v.Column = n.Position()
	var x any
	if err := n.Decode(&x); err != nil {
		return err
	}
	v.Decoded = fmt.Sprintf("%#v", x)
	switch n.Kind() {
	case SequenceNode:
		for i := 0; i < n.Len(); i++ {
			v.Children = append(v.Children, n.Item(i).Tag())
		}
	case MappingNode:
		for i := 0; i < n.Len(); i++ {
			k, e := n.Entry(i)
			v.Children = append(v.Children, k.Value()+"="+e.Kind().String()+":"+e.Tag())
		}
	}
	return nil
}

type holder struct {
	Direct  view            `yaml:"direct"`
	Ptr     *view           `yaml:"ptr"`
	Null    *view           `yaml:"null"`
	List    []view          `yaml:"list"`
	Map     map[string]view `yaml:"map"`
	Alias   view            `yaml:"alias"`
	Outside view            `yaml:"outside"`
	Nested  struct {
		In view `yaml:"in"`
	} `yaml:"nested"`
}

const hookDoc = `%TAG !e! tag:example.com,2000:
---
base: &base {x: 1, y: [1, 2]}
direct: !e!thing
  k: v
  n: [a, b]
ptr: &p "quoted\ttext"
null:
list:
- 12
- |
  block
  text
- !!str 12
map:
  one: [1, 2]
  two: *base
alias: *base
outside:
  inner: *base
nested:
  in: {a: &z 1, b: *z}
`

// Both paths hand the hook the same view of the same input: the Document path
// gives the stored node, the plain path builds the node from the events.
func TestUnmarshalerSeesTheSameNodeOnBothPaths(t *testing.T) {
	var plain, viaDoc holder
	if err := Unmarshal([]byte(hookDoc), &plain); err != nil {
		t.Fatal(err)
	}
	d, err := Parse([]byte(hookDoc))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Decode(&viaDoc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plain, viaDoc) {
		t.Errorf("views differ:\nplain: %+v\n  doc: %+v", plain, viaDoc)
	}
	if plain.Null != nil {
		t.Errorf("null pointer target was called: %+v", plain.Null)
	}
	if got := plain.Direct; got.Kind != MappingNode || got.Tag != "tag:example.com,2000:thing" || got.Line != 5 || got.Len != 2 ||
		!reflect.DeepEqual(got.Children, []string{"k=scalar:tag:yaml.org,2002:str", "n=sequence:tag:yaml.org,2002:seq"}) {
		t.Errorf("direct = %+v", got)
	}
	if got := plain.Ptr; got == nil || got.Anchor != "p" || got.Value != "quoted\ttext" || got.Style != DoubleQuotedStyle {
		t.Errorf("ptr = %+v", got)
	}
	if len(plain.List) != 3 || plain.List[1].Value != "block\ntext\n" || plain.List[1].Style != LiteralStyle ||
		plain.List[2].Tag != "tag:yaml.org,2002:str" || plain.List[0].Tag != "tag:yaml.org,2002:int" {
		t.Errorf("list = %+v", plain.List)
	}
	if plain.Alias.Anchor != "base" || plain.Alias.Kind != MappingNode || plain.Map["two"].Anchor != "base" {
		t.Errorf("alias = %+v", plain.Alias)
	}
	if plain.Nested.In.Decoded != `map[string]interface {}{"a":1, "b":1}` {
		t.Errorf("nested = %+v", plain.Nested.In)
	}
}

// An alias inside a hook's subtree resolves to an anchor outside it.
func TestUnmarshalerNodeDecodeResolvesOutsideAliases(t *testing.T) {
	var h struct {
		Outside view `yaml:"outside"`
	}
	src := "base: &base {x: 1}\noutside:\n  inner: *base\n"
	if err := Unmarshal([]byte(src), &h); err != nil {
		t.Fatal(err)
	}
	if h.Outside.Decoded != `map[string]interface {}{"inner":map[string]interface {}{"x":1}}` {
		t.Errorf("plain: %+v", h.Outside)
	}
}

type failing struct{}

var errHook = errors.New("hook says no")

func (failing) UnmarshalYAML(Node) error { return errHook }

type nester struct {
	Inner []view
	Self  *nester
}

func (n *nester) UnmarshalYAML(node Node) error {
	var raw struct {
		Inner []view  `yaml:"inner"`
		Self  *nester `yaml:"self"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	n.Inner, n.Self = raw.Inner, raw.Self
	return nil
}

func TestUnmarshalerErrorsAndNesting(t *testing.T) {
	var v struct {
		F failing `yaml:"f"`
	}
	for name, run := range map[string]func() error{
		"plain": func() error { return Unmarshal([]byte("a: 1\nf: x\n"), &v) },
		"doc": func() error {
			d, _ := Parse([]byte("a: 1\nf: x\n"))
			return d.Decode(&v)
		},
	} {
		err := run()
		var te *TypeError
		if !errors.As(err, &te) || !errors.Is(err, errHook) || te.Path != "/f" || te.Line != 2 {
			t.Errorf("%s: err = %#v", name, err)
		}
	}
	var n nester
	src := "inner: [1, {a: b}]\nself:\n  inner: [x]\n  self:\n    inner: []\n"
	if err := Unmarshal([]byte(src), &n); err != nil {
		t.Fatal(err)
	}
	if len(n.Inner) != 2 || n.Self == nil || len(n.Self.Inner) != 1 || n.Self.Self == nil || n.Self.Self.Self != nil {
		t.Errorf("nested = %+v", n)
	}
	d, _ := Parse([]byte(src))
	var m nester
	if err := d.Decode(&m); err != nil || !reflect.DeepEqual(n, m) {
		t.Errorf("document path: %v %+v", err, m)
	}
}

func TestDocumentUTF16AndGet(t *testing.T) {
	text := "# note\nname: \"é𝄞\"\nn: 1\n"
	wire := func(bom bool, be bool) []byte {
		var out []byte
		put := func(r rune) {
			var us []uint16
			if r >= 0x10000 {
				r -= 0x10000
				us = []uint16{uint16(0xD800 + r>>10), uint16(0xDC00 + r&0x3FF)}
			} else {
				us = []uint16{uint16(r)}
			}
			for _, u := range us {
				if be {
					out = append(out, byte(u>>8), byte(u))
				} else {
					out = append(out, byte(u), byte(u>>8))
				}
			}
		}
		if bom {
			put(0xFEFF)
		}
		for _, r := range text {
			put(r)
		}
		return out
	}
	for _, c := range []struct{ bom, be bool }{{true, false}, {true, true}, {false, false}, {false, true}} {
		in := wire(c.bom, c.be)
		d, err := Parse(in)
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if !bytes.Equal(d.Bytes(), in) {
			t.Errorf("%+v: unedited bytes changed", c)
		}
		var cfg struct {
			Name string `yaml:"name"`
			N    int    `yaml:"n"`
		}
		if err := d.Decode(&cfg); err != nil || cfg.Name != "é𝄞" {
			t.Errorf("%+v: decode %v %+v", c, err, cfg)
		}
		if err := d.Set("/n", 2); err != nil {
			t.Fatal(err)
		}
		text2 := strings.Replace(text, "n: 1", "n: 2", 1)
		saved := text
		text = text2
		want := wire(c.bom, c.be)
		text = saved
		if !bytes.Equal(d.Bytes(), want) {
			t.Errorf("%+v: edited bytes = %q, want %q", c, d.Bytes(), want)
		}
	}
	if _, err := Parse([]byte("a\x00b")); err == nil {
		t.Error("odd-length UTF-16 accepted")
	} else if se, ok := err.(*SyntaxError); !ok || se.Category != "encoding" {
		t.Errorf("err = %#v", err)
	}
	d, _ := Parse([]byte("a: {b: [1, 2]}\nc: x\n"))
	if err := d.Get("/a/b", &[]int{}); err != nil {
		t.Errorf("Get slice: %v", err)
	}
	var ptr *string
	if err := d.Get("/c", &ptr); err != nil || ptr == nil || *ptr != "x" {
		t.Errorf("Get pointer: %v %v", err, ptr)
	}
	var te *TypeError
	if err := d.Get("/c", new(int)); !errors.As(err, &te) || te.Path != "/c" {
		t.Errorf("Get type error: %#v", err)
	}
	if err := d.Get("/zzz", new(int)); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing: %v", err)
	}
}
