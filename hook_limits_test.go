package oneyaml

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The first document added to an empty stream is held to the limit in the bytes
// that are written, in UTF-16 and UTF-32, not in the UTF-8 form.
func TestFirstDocumentIsHeldToTheLimitInWrittenBytes(t *testing.T) {
	for _, enc := range []struct {
		name string
		mk   func(string) []byte
	}{{"utf16", utf16le}, {"utf32", utf32le}} {
		src := enc.mk("# c\n")
		for _, v := range []any{"abcdefgh", strings.Repeat("\u4e2d", 6), map[string]any{"k": "vvvvvv"}} {
			free, err := Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			if err := free.Set("", v); err != nil {
				t.Fatal(err)
			}
			want := len(free.Bytes())
			for lim := len(src); lim <= want+4; lim++ {
				d, _ := Parse(src, Limits(LimitSet{MaxInputBytes: lim}))
				err := d.Set("", v)
				if (err == nil) != (want <= lim) || (err != nil && !errors.Is(err, ErrMaxInputBytes)) {
					t.Fatalf("%s %v: limit %d, the document is %d bytes written: Set = %v", enc.name, v, lim, want, err)
				}
			}
		}
	}
}

func TestSizeArithmeticSaturates(t *testing.T) {
	// 32-bit ints: the sums that check a size must stop at the largest int instead
	// of wrapping. The helpers work on any int size; check their edges here.
	if got := addSat(1<<62, 1<<62); got < 1<<62 {
		t.Errorf("addSat wrapped: %d", got)
	}
	if err := fixedSizeErr(maxStreamBytes, 1); err == nil {
		t.Error("growing past the ceiling by one byte was allowed")
	}
	if err := fixedSizeErr(maxStreamBytes-3, 3); err != nil {
		t.Errorf("growing to the ceiling: %v", err)
	}
	// grow near the largest int must not wrap the comparison.
	if err := fixedSizeErr(10, int(^uint(0)>>1)-5); err == nil {
		t.Error("a huge growth was allowed")
	}
}

// hook options ----------------------------------------------------------------

var tightenOpts []Option

// tightenHook decodes its node into any with the options the test set.
type tightenHook struct{ v any }

func (h *tightenHook) UnmarshalYAML(n Node) error { return n.Decode(&h.v, tightenOpts...) }

func tightenBomb() []byte {
	var b strings.Builder
	b.WriteString("n0: &n0 [a, a, a, a, a, a, a, a]\n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&b, "n%d: &n%d [*n%d, *n%d, *n%d, *n%d, *n%d, *n%d, *n%d, *n%d]\n", i, i, i-1, i-1, i-1, i-1, i-1, i-1, i-1, i-1)
	}
	b.WriteString("x: *n5\n")
	return []byte(b.String())
}

// reads runs the same read four ways; the first two take the options as parse
// options, the third parses with them and the fourth gives them to the read.
func readsFour(t *testing.T, src []byte, v func() any, outer Option) map[string]error {
	t.Helper()
	out := map[string]error{}
	out["Unmarshal"] = Unmarshal(src, v(), outer)
	out["Decoder"] = NewDecoder(bytes.NewReader(src), outer).Decode(v())
	d, err := Parse(src, outer)
	if err != nil {
		t.Fatal(err)
	}
	out["Parse(opt).Decode"] = d.Decode(v())
	d, err = Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	out["Parse().Decode(opt)"] = d.Decode(v(), outer)
	return out
}

func TestHookOptionsMayTightenLimitsNeverLoosenThem(t *testing.T) {
	src := tightenBomb()
	target := func() any { return &struct{ X tightenHook }{} }
	// Without a hook the bomb is over Untrusted's bounds.
	for _, hook := range []struct {
		name string
		opts []Option
	}{{"no options", nil}, {"NoLimits", []Option{NoLimits()}}, {"Untrusted", []Option{Untrusted()}},
		{"larger limits", []Option{Limits(LimitSet{MaxAliasNodes: 1 << 30, MaxAliasBytes: 1 << 30, MaxDepth: 1 << 20})}}} {
		tightenOpts = hook.opts
		for name, err := range readsFour(t, src, target, Untrusted()) {
			if !errors.Is(err, ErrMaxAliasNodes) {
				t.Errorf("hook %s, outer Untrusted, %s: %v, want the alias node limit", hook.name, name, err)
			}
		}
	}
	tightenOpts = nil
	// A hook may tighten: under the default bounds the same read passes, and fails
	// when the hook asks for Untrusted.
	if err := Unmarshal(src, target()); err != nil {
		t.Fatalf("default read: %v", err)
	}
	tightenOpts = []Option{NoLimits()}
	if err := Unmarshal(src, target()); err != nil {
		t.Errorf("hook NoLimits under the defaults: %v", err)
	}
	tightenOpts = []Option{Untrusted()}
	d, _ := Parse(src)
	for name, err := range map[string]error{
		"Unmarshal": Unmarshal(src, target()),
		"Decoder":   NewDecoder(bytes.NewReader(src)).Decode(target()),
		"Document":  d.Decode(target()),
	} {
		if !errors.Is(err, ErrMaxAliasNodes) {
			t.Errorf("hook Untrusted, default outer, %s: %v, want the alias node limit", name, err)
		}
	}
	tightenOpts = nil
}

func TestHookOptionsKeepNonLimitSettings(t *testing.T) {
	src := []byte("x: {a: 1, extra: 2}\n")
	tightenOpts = nil
	var v struct{ X optNodeHook }
	if err := Unmarshal(src, &v, Limits(LimitSet{MaxDepth: 50})); err != nil {
		t.Fatal(err)
	}
	// KnownFields given by the hook applies, and a looser depth from the hook does
	// not undo the caller's tighter one.
	h := &depthHook{opts: []Option{KnownFields(), Limits(LimitSet{MaxDepth: 1 << 20})}}
	var w struct{ X *depthHook }
	w.X = h
	err := Unmarshal([]byte("x: {a: [[[[[1]]]]], extra: 2}\n"), &w, Limits(LimitSet{MaxDepth: 4}))
	if !errors.Is(err, ErrMaxDepth) {
		t.Errorf("a hook widened the caller's depth limit: %v", err)
	}
	h = &depthHook{opts: []Option{KnownFields()}}
	w.X = h
	err = Unmarshal([]byte("x: {a: 1, extra: 2}\n"), &w)
	if !errors.Is(err, ErrUnknownField) {
		t.Errorf("KnownFields given by the hook = %v, want an unknown field", err)
	}
}

type depthHook struct {
	opts []Option
	A    any
}

func (h *depthHook) UnmarshalYAML(n Node) error {
	var tmp struct{ A any }
	err := n.Decode(&tmp, h.opts...)
	h.A = tmp.A
	return err
}

// A limit hit by a decode in a hook has the pointer in the whole document, like a
// type error.
type limitPathHook struct{ v any }

func (h *limitPathHook) UnmarshalYAML(n Node) error {
	return n.Decode(&h.v, Limits(LimitSet{MaxDepth: 3}))
}

type limitPathOuter struct{ In limitPathInner }

func (o *limitPathOuter) UnmarshalYAML(n Node) error {
	var tmp struct{ In limitPathInner }
	err := n.Decode(&tmp)
	o.In = tmp.In
	return err
}

type limitPathInner struct {
	Y []limitPathHook
}

func TestHookLimitErrorPathIsAbsolute(t *testing.T) {
	var one struct {
		A int
		B []struct{ Y limitPathHook }
	}
	err := Unmarshal([]byte("a: 1\nb:\n  - y: 1\n  - y: [[[[1]]]]\n"), &one)
	var le *LimitError
	if !errors.As(err, &le) {
		t.Fatalf("got %T %v", err, err)
	}
	if le.Path != "/b/1/y/0/0/0" {
		t.Errorf("Path = %q, want /b/1/y/0/0/0", le.Path)
	}
	if !strings.Contains(err.Error(), "at /b/1/y/0/0/0") {
		t.Errorf("message %q does not carry the whole path", err)
	}
	var te *TypeError
	if !errors.As(err, &te) || te.Path != "/b/1/y" {
		t.Errorf("TypeError path = %v", te)
	}
	// Hooks inside hooks, through a Document and from a node below the hook's own.
	var two struct{ Top []limitPathOuter }
	err = Unmarshal([]byte("top:\n  - in: {y: [[[[[1]]]]]}\n"), &two)
	le = nil
	if !errors.As(err, &le) || le.Path != "/top/0/in/y/0/0/0/0" {
		t.Errorf("nested hooks: %v, path %v", err, le)
	}
	d, _ := Parse([]byte("top:\n  - in: {y: [[[[[1]]]]]}\n"))
	err = d.Decode(&two)
	le = nil
	if !errors.As(err, &le) || le.Path != "/top/0/in/y/0/0/0/0" {
		t.Errorf("nested hooks through a Document: %v, path %v", err, le)
	}
}
