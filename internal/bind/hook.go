package bind

import (
	"bytes"
	"reflect"
	"sort"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/resolve"
)

// NodeRef describes the subtree handed to a type that reads its own node (the
// Unmarshaler interface of the root package). The package that defines the
// interface registers it with SetNodeHook and turns a NodeRef into its node view.
type NodeRef struct {
	Tree *cst.Tree
	Src  []byte
	Node int32
	// Stream is the owner of Tree when the tree is a Document's own, and nil
	// when the subtree was built for this call from recorded events.
	Stream any
	// Doc is the index of the document in Stream.
	Doc int
	// Root is true when the node is the root of its document.
	Root bool
	// DocStart is the offset of the document's start event, for a root node.
	DocStart int
	// Cfg is the configuration of the binder that made the call.
	Cfg Config
	// Tags holds, for a built subtree, the resolved tag of every property set of
	// Tree, indexed like Tree.Props (empty where there is no tag).
	Tags []string
	// Predefs are the events of anchored nodes outside a built subtree that its
	// aliases refer to, in document order. A binder reading the subtree defines
	// them first.
	Predefs [][]parse.Event
	// Cache points at a slot that lives as long as the document being read. A
	// hook that is called many times for one document keeps what it computes
	// from the whole text there (the line index, say) instead of rebuilding it
	// for every call. It is nil for a node that did not come from a binder.
	Cache *any
	// Scope is the alias budget of the call, shared by decodes made from the Node.
	Scope *Scope
}

// NodeSource is implemented by an Input whose events come from a tree: it lets
// the binder hand a hook the real node instead of building one.
type NodeSource interface {
	// NodeRef returns the reference for the node an event came from.
	NodeRef(ev *parse.Event) (NodeRef, bool)
}

var (
	hookIface reflect.Type
	hookCall  func(inst any, ref NodeRef) error
	funcIface reflect.Type
	funcCall  func(inst any, ref NodeRef) error
)

// SetFuncHook registers the second form of self-reading type, the one whose
// UnmarshalYAML takes a function that decodes the value into any target (the
// form of yaml.v2, yaml.v3 and goccy). It must run from an init function.
func SetFuncHook(iface reflect.Type, call func(inst any, ref NodeRef) error) {
	funcIface, funcCall = iface, call
}

// SetNodeHook registers the interface whose implementations read their own node
// and the function that calls it. It must run before the first decode, from an
// init function.
func SetNodeHook(iface reflect.Type, call func(inst any, ref NodeRef) error) {
	hookIface, hookCall = iface, call
}

var (
	v3Handles func(reflect.Type) bool
	v3IsNode  func(reflect.Type) bool
	v3Call    func(inst any, ref NodeRef) error
)

// SetV3Hook registers the node hook of the yaml.v3 dialect: handles reports
// whether a pointer type reads its own node, isNode whether a type is the node
// type itself (which receives even a null node), and call hands the node over.
// It must run before the first decode, from an init function.
func SetV3Hook(handles, isNode func(reflect.Type) bool, call func(inst any, ref NodeRef) error) {
	v3Handles, v3IsNode, v3Call = handles, isNode, call
}

// Predefine defines the anchors in evs, the events of anchored nodes, as if
// they had been read before the document root. Aliases then resolve to them.
func (b *Binder) Predefine(evs []parse.Event) error {
	own := b.own
	for i := range evs {
		ev := evs[i]
		if err := b.track(&ev); err != nil {
			return err
		}
	}
	b.own = own
	return nil
}

// hook hands the node at ev to a value whose type reads its own node. It reports
// whether it did.
func (b *Binder) hook(v reflect.Value, ev *parse.Event) (bool, error) {
	if b.cfg.Dialect == DialectV3 {
		return b.hookV3(v, ev)
	}
	if hookIface == nil {
		return false, nil
	}
	t := v.Type()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.PkgPath() == "" && t.Kind() != reflect.Struct {
		return false, nil
	}
	dp := planOf(t)
	if !dp.hooked {
		return false, nil
	}
	if dp.isNode {
		if v.Kind() == reflect.Pointer && b.isNull(ev) {
			return false, nil
		}
		return true, b.typeErr(ev, t, "cannot decode into "+t.String()+": a Node is a view of a Document, not a value; read it with Document.Get(pointer, &node)")
	}
	if dp.badUnm {
		return true, b.typeErr(ev, t, t.String()+" has an UnmarshalYAML method that is neither UnmarshalYAML(oneyaml.Node) error nor UnmarshalYAML(func(any) error) error; change its signature, or remove it to fill the value by its fields")
	}
	if dp.jsonUnm {
		return b.jsonHook(v, ev, dp)
	}
	call := hookCall
	if dp.funcUnm {
		call = funcCall
	}
	if v.Kind() == reflect.Pointer && b.isNull(ev) {
		v.SetZero()
		return true, b.skip(ev)
	}
	if dp.funcUnm && b.isNull(ev) {
		// An older library never hands null to the method.
		return true, b.skip(ev)
	}
	v = deref(v)
	var ref NodeRef
	var err error
	if ns, ok := b.in.(NodeSource); ok && ev.VersionMajor > 0 {
		ref, _ = ns.NodeRef(ev)
		err = b.skip(ev)
	} else {
		ref, err = b.buildNode(ev, 0)
	}
	if err != nil {
		return true, err
	}
	ref.Cfg, ref.Cache = b.nestedConfig(), &b.hookCache
	ref.Scope = &Scope{spend: ref.Cfg.Spend}
	b.hooks++
	herr := call(v.Addr().Interface(), ref)
	ref.Scope.Close()
	if herr != nil {
		return true, &Error{Kind: KindType, Offset: ev.Start, GoType: v.Type().String(), Msg: herr.Error(), Err: herr}
	}
	return true, nil
}

// isNull reports whether ev is a scalar that resolves to null.
func (b *Binder) isNull(ev *parse.Event) bool {
	if ev.Kind != parse.Scalar {
		return false
	}
	tag, err := b.tagOf(ev)
	if err != nil {
		return false
	}
	text, err := b.scalar(ev)
	if err != nil {
		return false
	}
	if b.cfg.Dialect == DialectV3 {
		r, ok := classifyV3(text, ev.Style, tag)
		return ok && r.Kind == resolve.Null
	}
	r, ok := classify(text, ev.Style, tag)
	return ok && r.Kind == resolve.Null
}

// buildNode reads the node that starts with ev into a tree of its own. This is
// the only place a plain load builds tree nodes, and only for a value whose
// type asked for them.
func (b *Binder) buildNode(first *parse.Event, hint int) (NodeRef, error) {
	tree := new(cst.Tree)
	// A tree grows by appending, and growing a large slice many times costs several
	// times its final size in garbage, so a caller that knows the subtree is large
	// says how many nodes to expect.
	if hint > 0 {
		tree.Nodes = make([]cst.Node, 0, hint)
	}
	var cb cst.Builder
	cb.Reset(tree, b.src)
	cb.Event(&parse.Event{Kind: parse.DocumentStart})
	var tags []string
	var aliases []logRange
	add := func(e *parse.Event) error {
		before := len(tree.Props)
		cb.Event(e)
		if len(tree.Props) > before && e.HasTag() {
			tb, err := b.in.AppendTag(nil, e)
			if err != nil {
				return err
			}
			for len(tags) < len(tree.Props) {
				tags = append(tags, "")
			}
			tags[len(tree.Props)-1] = string(tb)
		}
		if e.Kind == parse.Alias {
			aliases = append(aliases, logRange{e.AnchorStart, e.AnchorEnd})
		}
		return nil
	}
	if err := add(first); err != nil {
		return NodeRef{}, err
	}
	if first.Kind == parse.SequenceStart || first.Kind == parse.MappingStart {
		// One event variable for the whole loop: passing the address of a variable
		// declared inside it would allocate for every event.
		var e parse.Event
		for depth := 1; depth > 0; {
			var err error
			e, err = b.next()
			if err != nil {
				return NodeRef{}, err
			}
			switch e.Kind {
			case parse.SequenceStart, parse.MappingStart:
				depth++
			case parse.SequenceEnd, parse.MappingEnd:
				depth--
			}
			if err := add(&e); err != nil {
				return NodeRef{}, err
			}
		}
	}
	cb.Event(&parse.Event{Kind: parse.DocumentEnd})
	for len(tags) < len(tree.Props) {
		tags = append(tags, "")
	}
	ref := NodeRef{Tree: tree, Src: b.src, Node: tree.Docs[0].Root, Tags: tags}
	ref.Predefs = b.predefs(aliases)
	return ref, nil
}

// predefs copies the recorded events the given alias targets need, and the
// targets of the aliases inside them, in the order they were defined.
func (b *Binder) predefs(aliases []logRange) [][]parse.Event {
	if len(aliases) == 0 {
		return nil
	}
	seen := map[int]bool{}
	var ranges []logRange
	for len(aliases) > 0 {
		r := aliases[len(aliases)-1]
		aliases = aliases[:len(aliases)-1]
		if seen[r.start] || r.end > len(b.log) || r.start >= r.end {
			continue
		}
		seen[r.start] = true
		ranges = append(ranges, r)
		for _, e := range b.log[r.start:r.end] {
			if e.Kind == parse.Alias {
				aliases = append(aliases, logRange{e.AnchorStart, e.AnchorEnd})
			}
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	out := make([][]parse.Event, len(ranges))
	for i, r := range ranges {
		out[i] = append([]parse.Event(nil), b.log[r.start:r.end]...)
	}
	return out
}

// hookV3 is hook for the yaml.v3 dialect: a null node does not reach a type that
// reads its own node (only the node type itself takes it), and the call is told
// whether the node is the document root.
func (b *Binder) hookV3(v reflect.Value, ev *parse.Event) (bool, error) {
	root := !b.rootSeen
	b.rootSeen = true
	if v3Call == nil {
		return false, nil
	}
	t := v.Type()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.PkgPath() == "" && t.Kind() != reflect.Struct {
		return false, nil
	}
	dp := b.plan(t)
	if !dp.v3Unm {
		return false, nil
	}
	if !(dp.v3Node && v.Kind() != reflect.Pointer) && b.isNull(ev) {
		return false, nil
	}
	v = deref(v)
	var ref NodeRef
	var err error
	if ns, ok := b.in.(NodeSource); ok && ev.VersionMajor > 0 {
		ref, _ = ns.NodeRef(ev)
		err = b.skip(ev)
	} else {
		hint := 0
		if root {
			hint = nodeHint(b.src)
		}
		ref, err = b.buildNode(ev, hint)
	}
	if err != nil {
		return true, err
	}
	ref.Cfg, ref.Root, ref.DocStart, ref.Cache = b.nestedConfig(), root, b.docStart.Start, &b.hookCache
	ref.Scope = &Scope{spend: ref.Cfg.Spend}
	b.hooks++
	herr := v3Call(v.Addr().Interface(), ref)
	ref.Scope.Close()
	if herr != nil {
		if se, ok := herr.(interface{ SoftMessages() []string }); ok {
			// A type error from the hook is collected like any other.
			for _, m := range se.SoftMessages() {
				b.terrs = append(b.terrs, &Error{Kind: KindType, Offset: ev.Start, Msg: m, Category: "verbatim"})
			}
			return true, nil
		}
		return true, &Error{Kind: KindType, Offset: ev.Start, GoType: v.Type().String(), Msg: herr.Error(), Err: herr}
	}
	return true, nil
}

// nodeHint estimates how many tree nodes a document has: about two (and a little more for slack) for every
// line that is neither blank nor a comment (a key and its value, or a sequence
// item and the scalar in it). Text is mostly comments or mostly short flow
// lines in places, so the guess can be off either way; a low one costs a regrow
// and a high one wasted memory, and counting lines beats guessing from the byte
// count, which is far too high for a commented document.
func nodeHint(src []byte) int {
	lines := 0
	for len(src) > 0 {
		line := src
		if i := bytes.IndexByte(src, '\n'); i >= 0 {
			line, src = src[:i], src[i+1:]
		} else {
			src = nil
		}
		for len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			line = line[1:]
		}
		if len(line) > 0 && line[0] != '#' && line[0] != '\r' {
			lines++
		}
	}
	return lines*9/4 + 8
}
