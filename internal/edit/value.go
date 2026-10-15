// Package edit plans minimal-diff edits of a lossless tree.
//
// An edit is a list of replacements of byte ranges of the source. Nothing outside
// those ranges is read back out of a tree or written again, so every byte the edit
// does not name stays as it was. The planner never changes the tree or the
// source; the caller applies the replacements and parses the result again.
package edit

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/resolve"
	"oneyaml.dev/go/internal/scan"
)

// Errors reported by the planner. They are wrapped with detail; match with errors.Is.
var (
	ErrNotFound    = errors.New("path not found")
	ErrAlias       = errors.New("write through an alias")
	ErrAmbiguous   = errors.New("ambiguous key")
	ErrAnchorInUse = errors.New("anchor in use")
	ErrUnsupported = errors.New("edit not supported")
	ErrDepth       = errors.New("value nested too deeply")
)

var sprintf = fmt.Sprintf

func notFound(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrNotFound, fmt.Sprintf(format, a...))
}

// Kind is the type of a Value.
type Kind uint8

// Value kinds.
const (
	Null Kind = iota
	Bool
	Int
	Float
	String
	Map
	Seq
	Alias // a reference to an anchor; Text is the anchor name
)

// MaxValueDepth bounds the nesting of a Value that is converted or written.
const MaxValueDepth = 10000

// Value is a YAML value to be written: typed scalars, and collections whose
// order is kept.
type Value struct {
	Kind Kind
	// Text is the value of a String, the plain text of a Null, Bool, Int or
	// Float, and the anchor name of an Alias.
	Text string
	// Tag is an explicit tag to keep (such as "!!binary"); usually empty.
	Tag string
	// Items are the elements of a Seq, or key, value, key, value... of a Map.
	Items []Value
}

// TextHint estimates the bytes v takes when written: its scalar text plus a few
// bytes of structure for each value. It is a size to reserve, not a bound.
func (v *Value) TextHint() int {
	type item struct {
		v     *Value
		depth int
	}
	n := 0
	stack := []item{{v, 0}}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		// The indentation of a nested value is part of its line.
		n += len(x.v.Text) + 6 + 2*x.depth
		for i := range x.v.Items {
			stack = append(stack, item{&x.v.Items[i], x.depth + 1})
		}
	}
	return n
}

// IsScalar reports whether v is written as a scalar.
func (v *Value) IsScalar() bool { return v.Kind <= String || v.Kind == Alias }

// Str returns a string Value.
func Str(s string) Value { return Value{Kind: String, Text: s} }

// ScalarKind classifies the scalar node n of t whose decoded text is text: quoted
// and block scalars are strings, plain ones follow the Core schema, and an
// explicit core tag decides the type.
func ScalarKind(t *cst.Tree, src []byte, n int32, text []byte) Kind {
	return scalarKind(t, src, n, text)
}

// scalarKind classifies the scalar node n of t.
func scalarKind(t *cst.Tree, src []byte, n int32, text []byte) Kind {
	nd := &t.Nodes[n]
	if p := t.PropOf(n); p != nil && p.HasTag() {
		switch CanonTag(t, src, n) {
		case "!!str", "!!binary", "!!timestamp", "!":
			// "!" is the non-specific tag: the scalar is a string (YAML 1.2.2, 6.9.1).
			return String
		case "!!int":
			return Int
		case "!!float":
			return Float
		case "!!bool":
			return Bool
		case "!!null":
			return Null
		}
		if nd.Style != scan.Plain {
			return String
		}
	}
	if nd.Style != scan.Plain {
		return String
	}
	switch resolve.Core(text).Kind {
	case resolve.Null:
		return Null
	case resolve.Bool:
		return Bool
	case resolve.Int:
		return Int
	case resolve.Float:
		return Float
	}
	return String
}

// DecodeScalar appends the value of scalar node n to dst.
func DecodeScalar(dst []byte, t *cst.Tree, src []byte, n int32) []byte {
	nd := &t.Nodes[n]
	ev := parse.Event{Kind: parse.Scalar, Style: nd.Style, Start: int(nd.Start), End: int(nd.End), Indent: int(nd.Indent)}
	out, err := parse.AppendScalar(dst, src, &ev)
	if err != nil {
		return dst
	}
	return out
}

// FromTree converts node n of t into a Value. Anchors are dropped, aliases stay
// aliases, and tags that the value's kind already implies are dropped.
func FromTree(t *cst.Tree, src []byte, n int32) (Value, error) {
	type frame struct {
		node int32
		v    *Value
		next int32 // next child to convert
	}
	var root Value
	stack := []frame{}
	var scratch []byte
	conv := func(n int32, dst *Value) {
		nd := &t.Nodes[n]
		switch nd.Kind {
		case cst.Scalar:
			scratch = DecodeScalar(scratch[:0], t, src, n)
			k := scalarKind(t, src, n, scratch)
			*dst = Value{Kind: k, Text: string(scratch)}
			if p := t.PropOf(n); p != nil && p.HasTag() {
				tag := CanonTag(t, src, n)
				if len(tag) > 2 && tag[:2] == "!!" {
					switch tag {
					case "!!str", "!!int", "!!float", "!!bool", "!!null", "!!map", "!!seq":
					default:
						dst.Tag = tag
					}
				}
			}
		case cst.Alias:
			*dst = Value{Kind: Alias, Text: string(src[nd.Start+1 : nd.End])}
		case cst.Mapping:
			*dst = Value{Kind: Map}
		case cst.Sequence:
			*dst = Value{Kind: Seq}
		}
	}
	conv(n, &root)
	if root.IsScalar() {
		return root, nil
	}
	root.Items = make([]Value, 0, t.Count(n))
	stack = append(stack, frame{node: n, v: &root, next: t.First(n)})
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.next == 0 {
			stack = stack[:len(stack)-1]
			continue
		}
		c := f.next
		f.next = t.Nodes[c].Next
		f.v.Items = append(f.v.Items, Value{})
		dst := &f.v.Items[len(f.v.Items)-1]
		conv(c, dst)
		if !dst.IsScalar() {
			if len(stack) >= MaxValueDepth {
				return Value{}, ErrDepth
			}
			// dst points into f.v.Items, which is not appended to again until
			// this child frame is done, so the pointer stays valid.
			// Room for the children up front, so that appending them leaves no
			// grown-out arrays behind.
			dst.Items = make([]Value, 0, t.Count(c))
			stack = append(stack, frame{node: c, v: dst, next: t.First(c)})
		}
	}
	return root, nil
}

// FromYAML parses src, a single document as the encoder writes it, and returns
// its root as a Value.
func FromYAML(src []byte) (Value, error) {
	var t cst.Tree
	if err := cst.Build(&t, src, 0); err != nil {
		return Value{}, err
	}
	if len(t.Docs) == 0 {
		return Value{Kind: Null, Text: "null"}, nil
	}
	return FromTree(&t, src, t.Docs[0].Root)
}

// Equal reports whether node n of t reads as exactly v. Aliases are compared as
// aliases and nothing is followed. Tags other than the core scalar tags are
// ignored.
func Equal(t *cst.Tree, src []byte, n int32, v *Value) bool {
	type pair struct {
		n int32
		v *Value
	}
	var scratch []byte
	work := []pair{{n, v}}
	for len(work) > 0 {
		p := work[len(work)-1]
		work = work[:len(work)-1]
		nd := &t.Nodes[p.n]
		switch nd.Kind {
		case cst.Alias:
			if p.v.Kind != Alias || p.v.Text != string(src[nd.Start+1:nd.End]) {
				return false
			}
		case cst.Scalar:
			scratch = DecodeScalar(scratch[:0], t, src, p.n)
			k := scalarKind(t, src, p.n, scratch)
			if k != p.v.Kind {
				return false
			}
			switch k {
			case String:
				if string(scratch) != p.v.Text {
					return false
				}
			case Bool, Int, Float:
				if !sameNumber(scratch, []byte(p.v.Text), k) {
					return false
				}
			}
		case cst.Mapping, cst.Sequence:
			want := Seq
			if nd.Kind == cst.Mapping {
				want = Map
			}
			if p.v.Kind != want {
				return false
			}
			i := 0
			for c := t.First(p.n); c != 0; c = t.Nodes[c].Next {
				if i >= len(p.v.Items) {
					return false
				}
				work = append(work, pair{c, &p.v.Items[i]})
				i++
			}
			if i != len(p.v.Items) {
				return false
			}
		}
	}
	return true
}

func sameNumber(a, b []byte, k Kind) bool {
	if string(a) == string(b) {
		return true
	}
	ra, rb := resolve.Core(a), resolve.Core(b)
	if k == Float {
		// The kind comes from the tag or the text, and decides the type: "-1" under
		// !!float is the float -1, so it equals -1.0. Both sides are read as floats.
		fa, oka := floatOf(ra)
		fb, okb := floatOf(rb)
		return oka && okb && (fa == fb || (fa != fa && fb != fb))
	}
	if ra.Kind != rb.Kind {
		return false
	}
	switch ra.Kind {
	case resolve.Bool:
		return ra.B == rb.B
	case resolve.Int:
		if ra.W != rb.W {
			return false
		}
		switch ra.W {
		case resolve.Fits64:
			return ra.I == rb.I
		case resolve.FitsU64:
			return ra.U == rb.U
		}
		if ra.Base == 10 && rb.Base == 10 {
			// Converting a long decimal to a big integer takes time quadratic in its
			// digits, so compare the digit strings instead.
			return ra.Neg == rb.Neg && string(decimalDigits(ra.Digits)) == string(decimalDigits(rb.Digits))
		}
		return ra.Big().Cmp(rb.Big()) == 0
	case resolve.Float:
		return ra.F == rb.F || (ra.F != ra.F && rb.F != rb.F)
	}
	return false
}

// decimalDigits returns the digits of a decimal integer without separators and
// leading zeros.
func decimalDigits(d []byte) []byte {
	out := make([]byte, 0, len(d))
	for i, c := range d {
		if c == '_' || (c == '0' && len(out) == 0 && i < len(d)-1) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// floatOf reads an integer or a float result as a float64.
func floatOf(r resolve.Result) (float64, bool) {
	switch r.Kind {
	case resolve.Float:
		return r.F, true
	case resolve.Int:
		switch r.W {
		case resolve.Fits64:
			return float64(r.I), true
		case resolve.FitsU64:
			return float64(r.U), true
		}
		if r.Base == 10 {
			// Reading the digits as a float is linear; a big integer is not.
			txt := string(decimalDigits(r.Digits))
			if r.Neg {
				txt = "-" + txt
			}
			f, _ := strconv.ParseFloat(txt, 64)
			return f, true
		}
		f, _ := new(big.Float).SetInt(r.Big()).Float64()
		return f, true
	}
	return 0, false
}
