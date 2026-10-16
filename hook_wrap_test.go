package oneyaml

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// A hook usually adds context to the error of its decode with %w. The limit
// error inside must still carry the pointer in the whole document.

var wrapHookSub bool

type wrapInner struct{ v []float64 }

func (x *wrapInner) UnmarshalYAML(n Node) error {
	var err error
	if wrapHookSub {
		var one float64
		err = n.Item(1).Decode(&one, Limits(LimitSet{MaxNumberDigits: 3}))
	} else {
		err = n.Decode(&x.v, Limits(LimitSet{MaxNumberDigits: 3}))
	}
	if err != nil {
		return fmt.Errorf("inner: %w", err)
	}
	return nil
}

// wrapOuterChild makes wrapOuter decode only the child "in" of its Node, so the
// path from its Node to where the inner limit was hit has a step the inner
// hook cannot know. wrapOuterJoin wraps with errors.Join instead of %w.
var wrapOuterChild, wrapOuterJoin bool

type wrapOuter struct{ In struct{ Y wrapInner } }

func (x *wrapOuter) UnmarshalYAML(n Node) error {
	var tmp struct{ In struct{ Y wrapInner } }
	var err error
	if wrapOuterChild {
		var in Node
		if in, err = n.Find("in"); err == nil {
			err = in.Decode(&tmp.In)
		}
	} else {
		err = n.Decode(&tmp)
	}
	x.In = tmp.In
	if err != nil {
		if wrapOuterJoin {
			return errors.Join(errors.New("outer"), err)
		}
		return fmt.Errorf("outer: %w", err)
	}
	return nil
}

// wrapDeep adds a third level: it decodes its child "in" into a wrapOuter.
type wrapDeep struct{ In wrapOuter }

func (x *wrapDeep) UnmarshalYAML(n Node) error {
	in, err := n.Find("in")
	if err == nil {
		err = in.Decode(&x.In)
	}
	if err != nil {
		return fmt.Errorf("deep: %w", err)
	}
	return nil
}

type wrapTop struct {
	A int
	B []struct{ Y wrapInner }
	C []wrapOuter
	D []wrapDeep
}

func TestWrappedHookLimitErrorPathIsAbsolute(t *testing.T) {
	cases := []struct{ src, path string }{
		{"a: 1\nb:\n  - y: [1, 2]\n  - y: [1, 12.5678]\n", "/b/1/y/1"},
		{"a: 1\nc:\n  - in: {y: [1, 2]}\n  - in: {y: [1, 12.5678]}\n", "/c/1/in/y/1"},
		{"z: &z [1, 12.5678]\nb:\n  - y: *z\n", "/b/0/y/1"},
		{"a: 1\nd:\n  - in: {in: {y: [1, 2]}}\n  - in: {in: {y: [1, 12.5678]}}\n", "/d/1/in/in/y/1"},
	}
	defer func() { wrapHookSub, wrapOuterChild, wrapOuterJoin = false, false, false }()
	for _, c := range cases {
		for mode := 0; mode < 8; mode++ {
			sub, child, join := mode&1 != 0, mode&2 != 0, mode&4 != 0
			wrapHookSub, wrapOuterChild, wrapOuterJoin = sub, child, join
			src := []byte(c.src)
			errs := map[string]error{}
			var v1, v2, v3, v4 wrapTop
			errs["Unmarshal"] = Unmarshal(src, &v1)
			errs["Decoder"] = NewDecoder(bytes.NewReader(src)).Decode(&v2)
			d, err := Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			errs["Document.Decode"] = d.Decode(&v3)
			errs["Node.Decode"] = d.Root().Decode(&v4)
			for name, err := range errs {
				var le *LimitError
				if !errors.As(err, &le) {
					t.Errorf("%q sub=%v child=%v join=%v %s: got %v, want a limit error", c.src[:12], sub, child, join, name, err)
					continue
				}
				if le.Path != c.path {
					t.Errorf("%q sub=%v child=%v join=%v %s: Path %q, want %q", c.src[:12], sub, child, join, name, le.Path, c.path)
				}
			}
		}
	}
}

// A hook may only tighten the limits of the read that called it. These run the
// settings that the parser does not enforce first, so that the check is the
// hook's own, on every route that reads a Document.

type looseHook struct{ v any }

func (h *looseHook) UnmarshalYAML(n Node) error { return n.Decode(&h.v, NoLimits()) }

func TestHookNoLimitsKeepsTheCallersDepthDigitsAndScalarBounds(t *testing.T) {
	deep := "x: " + strings.Repeat("[", 20) + "1" + strings.Repeat("]", 20) + "\n"
	cases := []struct {
		name string
		src  string
		opts LimitSet
		want error
	}{
		{"depth", deep, LimitSet{MaxDepth: 8}, ErrMaxDepth},
		{"digits", "x: 1234567890123456789012345\n", LimitSet{MaxNumberDigits: 5}, ErrMaxNumberDigits},
		{"scalar bytes", "x: " + strings.Repeat("a", 64) + "\n", LimitSet{MaxScalarBytes: 16}, ErrMaxScalarBytes},
	}
	target := func() any { return &struct{ X looseHook }{} }
	for _, c := range cases {
		src := []byte(c.src)
		// The parser reads the whole text under the defaults, so only the read's
		// own bounds decide: give them to the read, not to Parse.
		d, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		errs := map[string]error{
			"Unmarshal":       Unmarshal(src, target(), Limits(c.opts)),
			"Decoder":         NewDecoder(bytes.NewReader(src), Limits(c.opts)).Decode(target()),
			"Document.Decode": d.Decode(target(), Limits(c.opts)),
		}
		var v struct{ X looseHook }
		errs["Node.Decode"] = d.Root().Decode(&v, Limits(c.opts))
		for name, err := range errs {
			if !errors.Is(err, c.want) {
				t.Errorf("%s, %s: %v, want %v", c.name, name, err, c.want)
			}
		}
		// Without the caller's bound the hook's NoLimits reads it.
		if err := d.Decode(target()); err != nil {
			t.Errorf("%s: default read: %v", c.name, err)
		}
	}
}
