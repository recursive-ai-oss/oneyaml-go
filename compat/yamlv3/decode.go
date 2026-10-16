package yaml

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"

	oneyaml "oneyaml.dev/go"
	"oneyaml.dev/go/internal/bind"
	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/scan"
)

// Decode decodes the node and stores its data into the value pointed to by v.
//
// See the documentation for Unmarshal for details about the conversion of YAML
// into a Go value.
func (n *Node) Decode(v interface{}) error {
	t, err := target(v)
	if err != nil {
		return err
	}
	l := oneyaml.DefaultLimits()
	cfg := bind.Config{
		Dialect: bind.DialectV3, MergeKeys: true, MaxDepth: l.MaxDepth,
		MaxAliasNodes: l.MaxAliasNodes, MaxAliasBytes: l.MaxAliasBytes, AliasRatio: 10,
		MaxNumberDigits: l.MaxNumberDigits,
	}
	if n != nil && n.hook != nil {
		// The node was handed to a type that reads its own node, which now decodes
		// it (or a part of it): what that costs counts against the call it is in.
		// After that call has returned a kept node starts a budget of its own.
		if sp := n.hook.scope.Spend(); sp != nil {
			cfg.Spend, cfg.InputBytes = sp, n.hook.cfg.InputBytes
		}
	}
	return n.decode(t, cfg)
}

// hookCall is a running call to a type that reads its own node. Its nodes point
// back to it, so a Node.Decode made from inside the method is charged to the
// alias budget of the call it is in.
type hookCall struct {
	cfg   bind.Config
	scope *bind.Scope
}

// decode reads n into out, which is a pointer, with the binder configured by cfg.
func (n *Node) decode(out interface{}, cfg bind.Config) error {
	t, err := target(out)
	if err != nil {
		return err
	}
	if n == nil {
		return errors.New("yaml: cannot decode a nil node")
	}
	in, err := newNodeInput(n, cfg.MaxDepth, cfg.MaxAliasNodes)
	if err != nil {
		return err
	}
	cfg.Dialect, cfg.MergeKeys = bind.DialectV3, true
	if cfg.Spend == nil {
		// A decode started from a hook keeps the size of the document it came from,
		// which the alias budget is measured against.
		cfg.InputBytes = len(in.src)
	}
	b := bind.New(cfg)
	b.Reset(in, cfg)
	ok, err := b.NextDocument()
	if err == nil && ok {
		for _, evs := range in.prelude {
			if err = b.Predefine(evs); err != nil {
				break
			}
		}
		if err == nil {
			err = b.Decode(reflect.ValueOf(t).Elem())
		}
	}
	if err != nil {
		return in.convert(err)
	}
	if c := b.Collected(); len(c) > 0 {
		te := &TypeError{}
		for _, e := range c {
			te.Errors = append(te.Errors, in.message(e))
		}
		return te
	}
	return nil
}

// message renders a collected type error with the line of the node it came from.
func (in *nodeInput) message(e *bind.Error) string {
	if e.Category == "verbatim" {
		return e.Msg
	}
	return "line " + strconv.Itoa(in.line(e.Offset)) + ": " + e.Msg
}

func (in *nodeInput) convert(err error) error {
	var be *bind.Error
	if !errors.As(err, &be) {
		return err
	}
	switch be.Kind {
	case bind.KindLimit:
		return &oneyaml.LimitError{Limit: oneyaml.Limit(be.Limit), What: be.What, Max: be.Max, Path: be.FullPath()}
	case bind.KindSyntax:
		if be.Category == "duplicate-key" {
			return &TypeError{Errors: []string{in.message(be)}}
		}
		return errors.New("yaml: " + be.Msg)
	}
	if be.Err != nil {
		return be.Err
	}
	return errors.New("yaml: " + be.Msg)
}

// nodeInput feeds the events of a Node tree to the binder, so a Node is decoded
// by the same code as text is. Every event takes one byte of a synthetic source
// (so that offsets are unique and identify the node an event came from); the
// text of an anchor, alias or tag follows its event's byte.
type nodeInput struct {
	src    []byte
	byOff  []*Node
	events []parse.Event
	pos    int

	ids      map[*Node]int
	defined  map[*Node]bool
	open     map[*Node]bool
	external []*Node
	seenExt  map[*Node]bool
	prelude  [][]parse.Event
	// owner is the external node whose definition is being generated: it always
	// carries an anchor.
	owner *Node

	// maxDepth bounds the nesting of the tree, maxExtra the nodes written again
	// because a node appears under more than one parent.
	maxDepth, maxExtra int
	extra              int
	seen               map[*Node]bool
}

// hardNodeDepth is the deepest tree that is decoded when no depth limit is set.
const hardNodeDepth = 100_000

func newNodeInput(root *Node, maxDepth, maxExtra int) (*nodeInput, error) {
	if maxDepth <= 0 || maxDepth > hardNodeDepth {
		maxDepth = hardNodeDepth
	}
	in := &nodeInput{ids: map[*Node]int{}, defined: map[*Node]bool{}, open: map[*Node]bool{}, seenExt: map[*Node]bool{},
		seen: map[*Node]bool{}, maxDepth: maxDepth, maxExtra: maxExtra}
	in.put(parse.Event{Kind: parse.StreamStart}, nil)
	in.put(parse.Event{Kind: parse.DocumentStart}, nil)
	top := root
	if root.Kind == DocumentNode {
		if len(root.Content) != 1 {
			// A document with no single root has nothing to decode.
			in.events = in.events[:1]
			in.put(parse.Event{Kind: parse.StreamEnd}, nil)
			return in, nil
		}
		top = root.Content[0]
		if top == nil {
			return nil, errors.New("yaml: cannot decode a nil node")
		}
	}
	evs, err := in.gen(top)
	if err != nil {
		return nil, err
	}
	in.events = append(in.events, evs...)
	in.put(parse.Event{Kind: parse.DocumentEnd}, nil)
	in.put(parse.Event{Kind: parse.StreamEnd}, nil)
	ext := in.external
	in.external = nil
	for _, t := range ext {
		if err := in.define(t); err != nil {
			return nil, err
		}
	}
	return in, nil
}

// define adds the events of an anchored node outside the tree being decoded to
// the prelude, after those of the nodes it needs itself.
func (in *nodeInput) define(t *Node) error {
	if in.seenExt[t] {
		return nil
	}
	in.seenExt[t] = true
	before := len(in.external)
	saveOpen, saveDef, saveOwner := in.open, in.defined, in.owner
	in.open, in.defined, in.owner = map[*Node]bool{}, map[*Node]bool{}, t
	evs, err := in.gen(t)
	in.open, in.defined, in.owner = saveOpen, saveDef, saveOwner
	if err != nil {
		return err
	}
	need := in.external[before:]
	in.external = in.external[:before]
	for _, d := range need {
		if err := in.define(d); err != nil {
			return err
		}
	}
	in.prelude = append(in.prelude, evs)
	return nil
}

func (in *nodeInput) id(n *Node) int {
	if id, ok := in.ids[n]; ok {
		return id
	}
	id := len(in.ids) + 1
	in.ids[n] = id
	return id
}

// put adds an event and returns it. The event takes one byte of the synthetic
// source, or the text given: a plain scalar keeps its own text there, as the
// binder reads a merge key from the source.
func (in *nodeInput) put(ev parse.Event, n *Node, text ...string) *parse.Event {
	ev.Start = len(in.src)
	if len(text) == 1 && text[0] != "" {
		in.src = append(in.src, text[0]...)
		in.byOff = append(in.byOff, n)
		for i := 1; i < len(text[0]); i++ {
			in.byOff = append(in.byOff, nil)
		}
	} else {
		in.src = append(in.src, '\n')
		in.byOff = append(in.byOff, n)
	}
	ev.End = len(in.src)
	in.events = append(in.events, ev)
	return &in.events[len(in.events)-1]
}

// text appends s to the synthetic source and returns its span.
func (in *nodeInput) text(s string) (int, int) {
	start := len(in.src)
	in.src = append(in.src, s...)
	for range s {
		in.byOff = append(in.byOff, nil)
	}
	return start, len(in.src)
}

func (in *nodeInput) line(off int) int {
	if off >= 0 && off < len(in.byOff) && in.byOff[off] != nil {
		return in.byOff[off].Line
	}
	return 0
}

type genFrame struct {
	n *Node
	i int
}

// gen returns the events of the tree below n. Nodes already defined in the
// stream, or being defined, may be aliased; any other alias target is noted as
// external.
func (in *nodeInput) gen(n *Node) ([]parse.Event, error) {
	// Events are built in a scratch slice so that put's bookkeeping, which appends
	// to in.events, does not mix prelude events into the main stream.
	saved := in.events
	in.events = nil
	defer func() { in.events = saved }()
	var stack []genFrame
	emit := func(c *Node) error {
		if c == nil {
			return errors.New("yaml: cannot decode a nil node")
		}
		switch c.Kind {
		case ScalarNode, AliasNode:
			if _, err := in.leaf(c); err != nil {
				return err
			}
			if c.Kind == ScalarNode && c.Anchor != "" {
				in.defined[c] = true
			}
		case SequenceNode, MappingNode:
			if in.open[c] {
				return errors.New("yaml: cannot decode a node that contains itself")
			}
			if len(stack) >= in.maxDepth {
				return &oneyaml.LimitError{Limit: oneyaml.LimitMaxDepth, What: "depth", Max: in.maxDepth}
			}
			if in.seen[c] {
				// The same node under a second parent is written out again.
				in.extra += 1 + len(c.Content)
				if in.maxExtra > 0 && in.extra > in.maxExtra {
					return &oneyaml.LimitError{Limit: oneyaml.LimitMaxAliasNodes, What: "alias expansion nodes", Max: in.maxExtra}
				}
			}
			in.seen[c] = true
			k := parse.SequenceStart
			if c.Kind == MappingNode {
				k = parse.MappingStart
			}
			ev := in.put(parse.Event{Kind: k, Flow: c.Style&FlowStyle != 0}, c)
			if c.Anchor != "" || c == in.owner {
				ev.AnchorStart, ev.AnchorEnd = in.text(strconv.Itoa(in.id(c)))
			}
			in.open[c] = true
			stack = append(stack, genFrame{n: c})
		default:
			return fmt.Errorf("yaml: cannot decode node with unknown kind %d", c.Kind)
		}
		return nil
	}
	if err := emit(n); err != nil {
		return nil, err
	}
	for len(stack) > 0 {
		f := &stack[len(stack)-1]
		if f.i < len(f.n.Content) {
			c := f.n.Content[f.i]
			f.i++
			if err := emit(c); err != nil {
				return nil, err
			}
			continue
		}
		k := parse.SequenceEnd
		if f.n.Kind == MappingNode {
			k = parse.MappingEnd
		}
		in.put(parse.Event{Kind: k}, f.n)
		delete(in.open, f.n)
		if f.n.Anchor != "" {
			in.defined[f.n] = true
		}
		stack = stack[:len(stack)-1]
	}
	out := in.events
	return out, nil
}

// leaf adds the event of a scalar or an alias.
func (in *nodeInput) leaf(c *Node) (*parse.Event, error) {
	if c.Kind == AliasNode {
		t := c.Alias
		if t == nil {
			return nil, fmt.Errorf("yaml: unknown anchor '%s' referenced", c.Value)
		}
		ev := in.put(parse.Event{Kind: parse.Alias}, c)
		// The event's span is "*name", which the binder reads the name from.
		in.src = in.src[:ev.Start]
		in.byOff = in.byOff[:ev.Start]
		start, end := in.text("*" + strconv.Itoa(in.id(t)))
		for len(in.byOff) < len(in.src) {
			in.byOff = append(in.byOff, nil)
		}
		in.byOff[start] = c
		ev.Start, ev.End = start, end
		if !in.defined[t] && !in.open[t] && !in.seenExt[t] {
			in.external = append(in.external, t)
		}
		return ev, nil
	}
	var ev *parse.Event
	if c.Style&(DoubleQuotedStyle|SingleQuotedStyle|LiteralStyle|FoldedStyle) == 0 && !strings.ContainsAny(c.Value, "\r\n") {
		ev = in.put(parse.Event{Kind: parse.Scalar}, c, c.Value)
	} else {
		ev = in.put(parse.Event{Kind: parse.Scalar}, c)
	}
	switch {
	case c.Style&DoubleQuotedStyle != 0:
		ev.Style = scan.DoubleQuoted
	case c.Style&SingleQuotedStyle != 0:
		ev.Style = scan.SingleQuoted
	case c.Style&LiteralStyle != 0:
		ev.Style = scan.Literal
	case c.Style&FoldedStyle != 0:
		ev.Style = scan.Folded
	default:
		ev.Style = scan.Plain
	}
	if c.Tag != "" && c.Tag != "!" && c.Tag != mergeTag {
		ev.TagStart, ev.TagEnd = in.text(longTag(c.Tag))
	}
	if c.Anchor != "" || c == in.owner {
		ev.AnchorStart, ev.AnchorEnd = in.text(strconv.Itoa(in.id(c)))
	}
	return ev, nil
}

// Next implements bind.Input.
func (in *nodeInput) Next() (parse.Event, error) {
	if in.pos >= len(in.events) {
		return parse.Event{}, io.EOF
	}
	ev := in.events[in.pos]
	in.pos++
	return ev, nil
}

// Source implements bind.Input.
func (in *nodeInput) Source() []byte { return in.src }

// AppendScalar implements bind.Input.
func (in *nodeInput) AppendScalar(dst []byte, ev *parse.Event) ([]byte, error) {
	if ev.Start < len(in.byOff) && in.byOff[ev.Start] != nil {
		return append(dst, in.byOff[ev.Start].Value...), nil
	}
	return dst, nil
}

// AppendTag implements bind.Input.
func (in *nodeInput) AppendTag(dst []byte, ev *parse.Event) ([]byte, error) {
	return append(dst, in.src[ev.TagStart:ev.TagEnd]...), nil
}
