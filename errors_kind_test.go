package oneyaml_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	oneyaml "oneyaml.dev/go"
)

// Types for the hook position tests.

type hookInner struct {
	Port int    `yaml:"port"`
	Name string `yaml:"name"`
}

// hookNode reads its value with Node.Decode.
type hookNode struct{ hookInner }

func (h *hookNode) UnmarshalYAML(n oneyaml.Node) error { return n.Decode(&h.hookInner) }

// hookFunc reads its value with the func-style hook.
type hookFunc struct{ hookInner }

func (h *hookFunc) UnmarshalYAML(f func(any) error) error { return f(&h.hookInner) }

// hookOuter hands its node on to another hooked type.
type hookOuter struct{ in hookNode }

func (h *hookOuter) UnmarshalYAML(n oneyaml.Node) error { return n.Decode(&h.in) }

// hookOther decodes text of its own, which has nothing to do with the input.
type hookOther struct{}

func (h *hookOther) UnmarshalYAML(oneyaml.Node) error {
	return oneyaml.Unmarshal([]byte("\n\n\nx: [1]\n"), &struct{ X int }{})
}

type pos struct {
	path      string
	line, col int
}

func positions(err error) []pos {
	var te *oneyaml.TypeError
	if !errors.As(err, &te) {
		return nil
	}
	var out []pos
	for _, e := range te.All() {
		out = append(out, pos{e.Path, e.Line, e.Column})
	}
	return out
}

// A type error under a hook is reported where the value is in the input, as it
// is without a hook.
func TestHookTypeErrorPositionIsTheValues(t *testing.T) {
	block := "# header\n\nfirst: 1\nsvc:\n  name: api\n\n  port: eighty\n"
	flow := "first: 1\nsvc: {name: api, port: eighty}\n"
	multi := "first: 1\nsvc:\n  name: [x]\n\n  port: eighty\n"
	type plain struct {
		First int       `yaml:"first"`
		Svc   hookInner `yaml:"svc"`
	}
	type withNode struct {
		First int      `yaml:"first"`
		Svc   hookNode `yaml:"svc"`
	}
	type withFunc struct {
		First int      `yaml:"first"`
		Svc   hookFunc `yaml:"svc"`
	}
	type withNested struct {
		First int       `yaml:"first"`
		Svc   hookOuter `yaml:"svc"`
	}
	for _, src := range []string{block, flow, multi} {
		var want plain
		wantPos := positions(oneyaml.Unmarshal([]byte(src), &want))
		if len(wantPos) == 0 {
			t.Fatalf("no error without a hook for %q", src)
		}
		check := func(name string, got []pos) {
			t.Helper()
			if !reflect.DeepEqual(got, wantPos) {
				t.Errorf("%s, %q:\n got  %v\n want %v", name, src, got, wantPos)
			}
		}
		check("Node hook", positions(oneyaml.Unmarshal([]byte(src), &withNode{})))
		check("func hook", positions(oneyaml.Unmarshal([]byte(src), &withFunc{})))
		check("nested hooks", positions(oneyaml.Unmarshal([]byte(src), &withNested{})))
		doc, err := oneyaml.Parse([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		check("Document.Decode", positions(doc.Decode(&withNode{})))
		check("Document.Decode, func hook", positions(doc.Decode(&withFunc{})))
		dec := oneyaml.NewDecoder(strings.NewReader(src))
		check("Decoder", positions(dec.Decode(&withNode{})))
	}
}

// In a stream, the line of a value under a hook counts the documents before it.
func TestHookTypeErrorPositionInLaterDocument(t *testing.T) {
	src := "a: 1\n---\nfirst: 1\nsvc:\n  name: api\n\n  port: eighty\n"
	var v struct {
		First int      `yaml:"first"`
		Svc   hookNode `yaml:"svc"`
	}
	dec := oneyaml.NewDecoder(strings.NewReader(src))
	var skip any
	if err := dec.Decode(&skip); err != nil {
		t.Fatal(err)
	}
	got := positions(dec.Decode(&v))
	want := []pos{{"/svc/port", 7, 9}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// An unknown key under a hook is reported at the key, and still matches
// ErrUnknownField.
func TestHookUnknownFieldPosition(t *testing.T) {
	src := "svc:\n  name: api\n  bogus: 1\n"
	var v struct{ Svc hookNode }
	err := oneyaml.Unmarshal([]byte(src), &v, oneyaml.KnownFields())
	got := positions(err)
	if want := []pos{{"/svc/bogus", 3, 3}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if !errors.Is(err, oneyaml.ErrUnknownField) {
		t.Errorf("not ErrUnknownField: %v", err)
	}
}

// Text a hook decodes on its own is not the input: its positions are not used.
func TestHookOtherTextKeepsTheHooksPosition(t *testing.T) {
	src := "a: 1\nb: 2\nsvc: x\n"
	var v struct{ Svc hookOther }
	err := oneyaml.Unmarshal([]byte(src), &v)
	got := positions(err)
	if len(got) != 1 || got[0].line != 3 {
		t.Errorf("got %v, want one error at line 3", got)
	}
}

// The origin mark that carries positions through a hook is not left in the
// errors a caller gets.
func TestTypeErrorHasNoHiddenState(t *testing.T) {
	var v struct{ Svc hookNode }
	var te *oneyaml.TypeError
	err := oneyaml.Unmarshal([]byte("svc: {port: x}\n"), &v)
	if !errors.As(err, &te) {
		t.Fatal(err)
	}
	again := oneyaml.Unmarshal([]byte("svc: {port: x}\n"), &v)
	var te2 *oneyaml.TypeError
	if !errors.As(again, &te2) || !reflect.DeepEqual(te, te2) {
		t.Errorf("two reads of the same text give different errors:\n%#v\n%#v", te, te2)
	}
}

// Each limit is matched with errors.Is and read from the Limit field; Option
// names the field that raises it.
func TestLimitErrorKinds(t *testing.T) {
	deep := strings.Repeat("[", 2000) + strings.Repeat("]", 2000)
	bomb := "a: &a [x, x, x, x]\nb: &b [*a, *a, *a, *a]\nc: [*b, *b, *b, *b]\n"
	cases := []struct {
		name  string
		err   error
		limit oneyaml.Limit
		is    error
		path  string
	}{
		{"depth", oneyaml.Unmarshal([]byte(deep), new(any), oneyaml.Limits(oneyaml.LimitSet{MaxDepth: 100})), oneyaml.LimitMaxDepth, oneyaml.ErrMaxDepth, ""},
		{"input", oneyaml.Unmarshal([]byte("a: 1\n"), new(any), oneyaml.Limits(oneyaml.LimitSet{MaxInputBytes: 2})), oneyaml.LimitMaxInputBytes, oneyaml.ErrMaxInputBytes, ""},
		{"alias nodes", oneyaml.Unmarshal([]byte(bomb), new(any), oneyaml.Limits(oneyaml.LimitSet{MaxAliasNodes: 20})), oneyaml.LimitMaxAliasNodes, oneyaml.ErrMaxAliasNodes, "/c/0"},
		{"alias bytes", oneyaml.Unmarshal([]byte(bomb), new(any), oneyaml.Limits(oneyaml.LimitSet{MaxAliasBytes: 8})), oneyaml.LimitMaxAliasBytes, oneyaml.ErrMaxAliasBytes, ""},
		{"scalar", oneyaml.Unmarshal([]byte("k: "+strings.Repeat("x", 100)+"\n"), new(any), oneyaml.Limits(oneyaml.LimitSet{MaxScalarBytes: 10})), oneyaml.LimitMaxScalarBytes, oneyaml.ErrMaxScalarBytes, "/k"},
		{"documents", func() error {
			d := oneyaml.NewDecoder(strings.NewReader("a: 1\n---\nb: 2\n---\nc: 3\n"), oneyaml.Limits(oneyaml.LimitSet{MaxDocuments: 1}))
			var v any
			for {
				if err := d.Decode(&v); err != nil {
					return err
				}
			}
		}(), oneyaml.LimitMaxDocuments, oneyaml.ErrMaxDocuments, ""},
		{"digits", oneyaml.Unmarshal([]byte("n: "+strings.Repeat("9", 200)+"\n"), new(any), oneyaml.Limits(oneyaml.LimitSet{MaxNumberDigits: 50})), oneyaml.LimitMaxNumberDigits, oneyaml.ErrMaxNumberDigits, "/n"},
	}
	all := []error{oneyaml.ErrMaxDepth, oneyaml.ErrMaxAliasNodes, oneyaml.ErrMaxAliasBytes, oneyaml.ErrMaxInputBytes,
		oneyaml.ErrMaxScalarBytes, oneyaml.ErrMaxDocuments, oneyaml.ErrMaxNumberDigits, oneyaml.ErrMaxPatchWorkRatio}
	for _, c := range cases {
		var le *oneyaml.LimitError
		if !errors.As(c.err, &le) {
			t.Errorf("%s: got %v, want a LimitError", c.name, c.err)
			continue
		}
		if le.Limit != c.limit {
			t.Errorf("%s: Limit %q, want %q", c.name, le.Limit, c.limit)
		}
		if !errors.Is(c.err, oneyaml.ErrLimit) || !errors.Is(c.err, c.is) {
			t.Errorf("%s: errors.Is does not match ErrLimit and %v", c.name, c.is)
		}
		for _, other := range all {
			if other != c.is && errors.Is(c.err, other) {
				t.Errorf("%s: also matches %v", c.name, other)
			}
		}
		if want := "Limits (" + string(c.limit) + ")"; le.Option != want {
			t.Errorf("%s: Option %q, want %q", c.name, le.Option, want)
		}
		if c.path != "" && le.Path != c.path {
			t.Errorf("%s: Path %q, want %q", c.name, le.Path, c.path)
		}
		if le.Max <= 0 || le.What == "" {
			t.Errorf("%s: Max %d What %q", c.name, le.Max, le.What)
		}
	}
}

// Parse and the Document operations name limits the same way.
func TestLimitErrorKindsFromDocument(t *testing.T) {
	_, err := oneyaml.Parse([]byte("a: 1\n"), oneyaml.Limits(oneyaml.LimitSet{MaxInputBytes: 2}))
	if !errors.Is(err, oneyaml.ErrMaxInputBytes) {
		t.Errorf("Parse: %v", err)
	}
	doc, _ := oneyaml.Parse([]byte("a: 1\n"))
	err = doc.ApplyPatch([]byte(`[{"op":"add","path":"/b","value":"` + strings.Repeat("x", 100) + `"}]`))
	if err != nil {
		t.Fatal(err)
	}
	small, _ := oneyaml.Parse([]byte("a: 1\n"), oneyaml.Limits(oneyaml.LimitSet{MaxInputBytes: 70000}))
	err = small.ApplyPatch([]byte(`[{"op":"add","path":"/b","value":"` + strings.Repeat("x", 80000) + `"}]`))
	var le *oneyaml.LimitError
	if !errors.As(err, &le) || le.Limit != oneyaml.LimitMaxInputBytes || !errors.Is(err, oneyaml.ErrMaxInputBytes) || !errors.Is(err, oneyaml.ErrLimit) {
		t.Errorf("patch: %v", err)
	}
}

// A syntax error matches ErrSyntax and the sentinel of its category.
func TestSyntaxErrorKinds(t *testing.T) {
	cases := []struct {
		src  string
		cat  oneyaml.Category
		is   error
		isnt []error
	}{
		{"a: [", oneyaml.CategorySyntax, oneyaml.ErrSyntax, []error{oneyaml.ErrDuplicateKey, oneyaml.ErrEncoding}},
		{"a: 1\na: 2\n", oneyaml.CategoryDuplicateKey, oneyaml.ErrDuplicateKey, []error{oneyaml.ErrEncoding, oneyaml.ErrLimit}},
		{"a: \xff\n", oneyaml.CategoryEncoding, oneyaml.ErrEncoding, []error{oneyaml.ErrDuplicateKey}},
	}
	for _, c := range cases {
		err := oneyaml.Unmarshal([]byte(c.src), new(any))
		var se *oneyaml.SyntaxError
		if !errors.As(err, &se) || se.Category != c.cat {
			t.Errorf("%q: got %v", c.src, err)
			continue
		}
		if !errors.Is(err, oneyaml.ErrSyntax) || !errors.Is(err, c.is) {
			t.Errorf("%q: does not match ErrSyntax and %v", c.src, c.is)
		}
		for _, o := range c.isnt {
			if errors.Is(err, o) {
				t.Errorf("%q: also matches %v", c.src, o)
			}
		}
	}
	// Parse reports the same kinds as Unmarshal.
	if _, err := oneyaml.Parse([]byte("a: [")); !errors.Is(err, oneyaml.ErrSyntax) {
		t.Errorf("Parse: %v", err)
	}
	if _, err := oneyaml.Parse([]byte("a: \xff\n")); !errors.Is(err, oneyaml.ErrEncoding) {
		t.Errorf("Parse encoding: %v", err)
	}
}

// GoType is the reflect.Type, so two types of one name can be told apart and a
// type can be compared.
func TestTypeErrorGoType(t *testing.T) {
	type Port int
	var v struct {
		D time.Duration `yaml:"d"`
		P Port          `yaml:"p"`
	}
	err := oneyaml.Unmarshal([]byte("d: [1]\np: [1]\n"), &v)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) || len(te.All()) != 2 {
		t.Fatalf("got %v", err)
	}
	all := te.All()
	if all[0].GoType != reflect.TypeFor[time.Duration]() {
		t.Errorf("GoType %v, want time.Duration", all[0].GoType)
	}
	if all[1].GoType != reflect.TypeFor[Port]() {
		t.Errorf("GoType %v, want Port", all[1].GoType)
	}
	if !strings.Contains(err.Error(), "time.Duration") {
		t.Errorf("message lost the type: %v", err)
	}
	// Writing: the type that has no YAML form.
	_, err = oneyaml.Marshal(map[string]any{"c": make(chan int)})
	if !errors.As(err, &te) || te.GoType != reflect.TypeFor[chan int]() {
		t.Errorf("Marshal: got %v", err)
	}
}

type badHook struct{ S string }

func (b *badHook) UnmarshalYAML(x int) error { return nil }

// A hook of the wrong shape is a type error like any other: the value is
// skipped and the rest of the document is read.
func TestBadHookSignatureIsCollected(t *testing.T) {
	var v struct {
		Before string  `yaml:"before"`
		Bad    badHook `yaml:"bad"`
		Mid    int     `yaml:"mid"`
		Seq    badHook `yaml:"seq"`
		After  string  `yaml:"after"`
	}
	src := "before: x\nbad: {S: a}\nmid: oops\nseq: [1, 2]\nafter: y\n"
	err := oneyaml.Unmarshal([]byte(src), &v)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) {
		t.Fatalf("got %v", err)
	}
	if v.Before != "x" || v.After != "y" || v.Bad.S != "" {
		t.Errorf("read %+v", v)
	}
	var paths []string
	for _, e := range te.All() {
		paths = append(paths, e.Path)
	}
	if want := []string{"/bad", "/mid", "/seq"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("paths %v, want %v", paths, want)
	}
	// A Node as a target is the same kind of mismatch.
	var w struct {
		N     oneyaml.Node `yaml:"n"`
		After int          `yaml:"after"`
	}
	err = oneyaml.Unmarshal([]byte("n: [a]\nafter: 3\n"), &w)
	if !errors.As(err, &te) || w.After != 3 {
		t.Errorf("Node target: %v, after=%d", err, w.After)
	}
}
