package oneyaml_test

import (
	"errors"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
)

func TestBoolOptionsCanBeTurnedOff(t *testing.T) {
	type cfg struct{ A int }
	src := []byte("a: 1\nb: 2\n")
	var v cfg
	if err := oneyaml.Unmarshal(src, &v, oneyaml.KnownFields()); !errors.Is(err, oneyaml.ErrUnknownField) {
		t.Fatalf("KnownFields(): %v", err)
	}
	if err := oneyaml.Unmarshal(src, &v, oneyaml.KnownFields(true)); !errors.Is(err, oneyaml.ErrUnknownField) {
		t.Fatalf("KnownFields(true): %v", err)
	}
	if err := oneyaml.Unmarshal(src, &v, oneyaml.KnownFields(false)); err != nil {
		t.Fatalf("KnownFields(false): %v", err)
	}
	// A wrapper's defaults are overridden by a later option; the last one wins.
	defaults := []oneyaml.Option{oneyaml.KnownFields(), oneyaml.MergeKeys()}
	if err := oneyaml.Unmarshal(src, &v, append(defaults, oneyaml.KnownFields(false))...); err != nil {
		t.Fatalf("override: %v", err)
	}
	if err := oneyaml.Unmarshal(src, &v, oneyaml.KnownFields(false), oneyaml.KnownFields()); !errors.Is(err, oneyaml.ErrUnknownField) {
		t.Fatalf("last wins: %v", err)
	}

	merge := []byte("base: &b {x: 1}\nuse:\n  <<: *b\n")
	var m map[string]map[string]any
	if err := oneyaml.Unmarshal(merge, &m, oneyaml.MergeKeys(false)); err != nil || m["use"]["x"] != nil {
		t.Fatalf("MergeKeys(false): %v %v", m, err)
	}
	if err := oneyaml.Unmarshal(merge, &m, oneyaml.MergeKeys(true)); err != nil || m["use"]["x"] != 1 {
		t.Fatalf("MergeKeys(true): %v %v", m, err)
	}

	d, err := oneyaml.Parse([]byte("a: 1\n"), oneyaml.YAML11Strings(), oneyaml.YAML11Strings(false))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Set("/b", "yes"); err != nil || d.String() != "a: 1\nb: yes\n" {
		t.Fatalf("YAML11Strings(false): %q %v", d.String(), err)
	}
	d, _ = oneyaml.Parse([]byte("a: 1\n"), oneyaml.YAML11Strings(true))
	if err := d.Set("/b", "yes"); err != nil || strings.Contains(d.String(), "b: yes") {
		t.Fatalf("YAML11Strings(true): %q %v", d.String(), err)
	}
}

func TestReadOptionsPerCall(t *testing.T) {
	type spec struct{ Port int }
	src := []byte("spec: {port: 1, extra: 2}\nbase: &b {x: 1}\nuse:\n  <<: *b\n")
	doc, err := oneyaml.Parse(src) // parsed without KnownFields
	if err != nil {
		t.Fatal(err)
	}
	var s spec
	if err := doc.Get("/spec", &s); err != nil {
		t.Fatalf("default read is loose: %v", err)
	}
	if err := doc.Get("/spec", &s, oneyaml.KnownFields()); !errors.Is(err, oneyaml.ErrUnknownField) {
		t.Fatalf("Get with KnownFields: %v", err)
	}
	n, _ := doc.Root().Lookup("spec")
	if err := n.Decode(&s, oneyaml.KnownFields()); !errors.Is(err, oneyaml.ErrUnknownField) {
		t.Fatalf("Node.Decode with KnownFields: %v", err)
	}
	if err := n.Decode(&s); err != nil {
		t.Fatalf("the option leaked into the next read: %v", err)
	}
	var top struct {
		Spec spec
	}
	if err := doc.Decode(&top, oneyaml.KnownFields()); !errors.Is(err, oneyaml.ErrUnknownField) {
		t.Fatalf("Document.Decode with KnownFields: %v", err)
	}

	// The Document's own options are the default, and a read can turn one off.
	strict, _ := oneyaml.Parse(src, oneyaml.KnownFields())
	if err := strict.Get("/spec", &s); !errors.Is(err, oneyaml.ErrUnknownField) {
		t.Fatalf("parsed with KnownFields: %v", err)
	}
	if err := strict.Get("/spec", &s, oneyaml.KnownFields(false)); err != nil {
		t.Fatalf("read with KnownFields(false): %v", err)
	}

	// MergeKeys applies to the lookup of a pointer too.
	var x int
	if err := doc.Get("/use/x", &x); !errors.Is(err, oneyaml.ErrNotFound) {
		t.Fatalf("merge key without the option: %v", err)
	}
	if err := doc.Get("/use/x", &x, oneyaml.MergeKeys()); err != nil || x != 1 {
		t.Fatalf("merge key with the option: %v %d", err, x)
	}

	// A limit given to a read applies to it.
	var anyv any
	err = doc.Decode(&anyv, oneyaml.Limits(oneyaml.LimitSet{MaxDepth: 1}))
	var le *oneyaml.LimitError
	if !errors.As(err, &le) {
		t.Fatalf("Limits on a read: %v", err)
	}

	// An option that does not concern reading is ignored, not an error.
	if err := doc.Get("/spec", &s, oneyaml.Indent(4), oneyaml.YAML11Strings(), nil); err != nil {
		t.Fatalf("ignored options: %v", err)
	}
}

func TestSetAndCommentsHonourMaxInputBytes(t *testing.T) {
	lim := oneyaml.Limits(oneyaml.LimitSet{MaxInputBytes: 64})
	long := strings.Repeat("y", 200)
	for name, f := range map[string]func(d *oneyaml.Document) error{
		"Set new key":    func(d *oneyaml.Document) error { return d.Set("/b", long) },
		"Set replace":    func(d *oneyaml.Document) error { return d.Set("/a", long) },
		"Set append":     func(d *oneyaml.Document) error { return d.Set("/l/-", long) },
		"Rename":         func(d *oneyaml.Document) error { return d.Rename("/a", long) },
		"SetHeadComment": func(d *oneyaml.Document) error { return d.SetHeadComment("/a", long) },
		"SetLineComment": func(d *oneyaml.Document) error { return d.SetLineComment("/a", long) },
	} {
		d, err := oneyaml.Parse([]byte("a: 1\nl: [1]\n"), lim)
		if err != nil {
			t.Fatal(err)
		}
		before := d.String()
		err = f(d)
		var le *oneyaml.LimitError
		ok := errors.As(err, &le)
		if !errors.Is(err, oneyaml.ErrMaxInputBytes) || !errors.Is(err, oneyaml.ErrLimit) {
			t.Errorf("%s: err = %v, want ErrMaxInputBytes and ErrLimit", name, err)
		}
		if !ok || le.Max != 64 || le.Limit != oneyaml.LimitMaxInputBytes {
			t.Errorf("%s: err = %v, want a *LimitError with Max 64", name, err)
		}
		if d.String() != before {
			t.Errorf("%s: the document changed", name)
		}
	}
	// A first document written into an empty stream is held to the bound too.
	empty, _ := oneyaml.Parse(nil, lim)
	if err := empty.Set("", long); !errors.Is(err, oneyaml.ErrMaxInputBytes) || empty.String() != "" {
		t.Errorf("first document: err = %v, text %q", err, empty.String())
	}
	// An edit inside the bound still works, and so does one that shrinks.
	d, _ := oneyaml.Parse([]byte("a: 1\n"), lim)
	if err := d.Set("/b", "ok"); err != nil {
		t.Fatalf("small edit: %v", err)
	}
	big, _ := oneyaml.Parse([]byte("a: "+long+"\n"), oneyaml.Limits(oneyaml.LimitSet{MaxInputBytes: 300}))
	if err := big.Set("/a", "s"); err != nil {
		t.Fatalf("shrinking edit: %v", err)
	}
	// The same bound as the patch path.
	p, _ := oneyaml.Parse([]byte("a: 1\n"), lim)
	var ple *oneyaml.LimitError
	if !errors.As(p.Apply(oneyaml.PatchAdd("/b", long)), &ple) {
		t.Fatal("patch path does not refuse")
	}
}
