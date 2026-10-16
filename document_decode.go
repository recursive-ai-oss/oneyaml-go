package oneyaml

import (
	"io"
	"reflect"
	"sort"

	"oneyaml.dev/go/internal/bind"
	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/parse"
)

// Unmarshaler is implemented by types that read their own YAML. Unmarshal,
// Decoder.Decode, Document.Decode and Node.Decode call UnmarshalYAML with a view
// of the node that holds the value, instead of decoding into the type's fields.
//
// The Node is read-only. Node.Decode on it decodes the subtree into any target,
// with the same rules as Unmarshal. When the value comes from Unmarshal or a
// Decoder there is no Document, so the Node covers only that subtree and is
// valid only during the call; aliases inside it still resolve. A null value
// leaves a pointer target nil without calling the method. An error the method
// returns comes back from the decode as a *TypeError whose Unwrap gives it.
type Unmarshaler interface {
	UnmarshalYAML(node Node) error
}

func init() {
	bind.SetNodeHook(reflect.TypeFor[Unmarshaler](), func(inst any, ref bind.NodeRef) error {
		return inst.(Unmarshaler).UnmarshalYAML(nodeFromRef(ref))
	})
}

// decodeState is the part of a docStream that decoding needs beyond the tree.
type decodeState struct {
	// propTags holds resolved tags by property index, for a stream built from
	// recorded events by the decoder; such a stream has no directives to consult.
	propTags []string
	// predefs are events of anchored nodes outside the stream's one document that
	// its aliases refer to.
	predefs [][]parse.Event

	// tp resolves tags against the %TAG directives of document tpDoc, as of tpGen.
	tp     *parse.Parser
	tpDoc  int
	tpGen  uint32
	tpFrom int

	// cur remembers the last child found by Node.Item and Node.Entry.
	cur childCursor
}

type childCursor struct {
	parent int32
	gen    uint32
	pos    int
	node   int32
}

func nodeFromRef(ref bind.NodeRef) Node {
	if s, ok := ref.Stream.(*docStream); ok {
		return Node{s: s, i: ref.Node, doc: int32(ref.Doc), gen: s.epoch(), sc: ref.Scope}
	}
	s := &docStream{src: ref.Src, tree: *ref.Tree, cfg: configFromBind(ref.Cfg)}
	s.dec.propTags, s.dec.predefs = ref.Tags, ref.Predefs
	return Node{s: s, i: ref.Node, sc: ref.Scope}
}

func configFromBind(c bind.Config) config {
	return config{
		knownFields: c.KnownFields, mergeKeys: c.MergeKeys, aliasRatio: c.AliasRatio, exactNodes: c.ExactNodes, exactBytes: c.ExactBytes,
		limits: LimitSet{MaxDepth: c.MaxDepth, MaxAliasNodes: c.MaxAliasNodes, MaxAliasBytes: c.MaxAliasBytes,
			MaxScalarBytes: c.MaxScalarBytes, MaxNumberDigits: c.MaxNumberDigits},
	}
}

// Decode reads the document into the value v points to, with the rules and
// errors of Unmarshal: the same binder reads the stored tree that Unmarshal
// reads from the parser, so the two always agree. Aliases are expanded within the
// alias and depth limits the Document was parsed with, or those the options of
// the call set. A Document with no
// documents leaves v unchanged. If the view addresses a document that does not
// exist, the error is an *EditError matching ErrNotFound.
//
// Options given to Decode apply to this read only, on top of those the Document
// was parsed with: Decode(&cfg, KnownFields()) reads strictly from a Document
// that was parsed without the option. Options that do not concern reading are
// ignored; see Option.
func (d *Document) Decode(v any, opts ...Option) error {
	if d.isNil() {
		return &EditError{Op: "decode", Kind: ErrNilDocument}
	}
	if len(d.s.tree.Docs) == 0 && d.idx == 0 {
		return checkTarget(v)
	}
	if !d.valid() {
		return &EditError{Op: "decode", Kind: ErrNotFound}
	}
	return d.s.decode(d.idx, d.s.tree.Docs[d.idx].Root, v, nil, opts)
}

// Decode reads the node and everything below it into the value v points to, as
// Document.Decode does for a whole document. A *TypeError's Path is relative to
// the node. An alias inside the subtree is expanded even when its anchor lies
// outside it. A Node made stale by an edit reports an *EditError matching
// ErrStaleNode; the zero Node reports one matching ErrNotFound. Options apply to
// this read only, as for Document.Decode.
func (n Node) Decode(v any, opts ...Option) error {
	if n.nd() == nil {
		return n.errInvalid("decode")
	}
	return n.s.decode(int(n.doc), n.i, v, n.sc, opts)
}

func checkTarget(v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &TypeError{GoType: reflect.TypeOf(v), Msg: "Decode needs a non-nil pointer, got " + goTypeName(v)}
	}
	return nil
}

// readConfig returns the Document's options with opts applied on top, for one
// read. It is a separate function so that the common call, with no options,
// does not move a config to the heap.
func (s *docStream) readConfig(opts []Option) config {
	c := s.cfg
	for _, o := range opts {
		if o != nil {
			o(&c)
		}
	}
	return c
}

func (s *docStream) decode(doc int, root int32, v any, sc *bind.Scope, opts []Option) error {
	if err := checkTarget(v); err != nil {
		return err
	}
	s.tree.Settle()
	defer func(was bool) { s.decoding = was }(s.decoding)
	s.decoding = true
	in := &treeInput{s: s, doc: doc}
	in.w.reset(&s.tree, root)
	sess := sessionPool.Get().(*session)
	cfg := s.cfg
	if len(opts) > 0 {
		cfg = s.readConfig(opts)
	}
	cfg.spend = sc.Spend()
	sess.b.Reset(in, bindConfig(&cfg, len(s.src)))
	_, err := sess.b.NextDocument()
	if err == nil {
		err = s.prelude(sess.b, doc, root)
	}
	if err == nil {
		if ap, isAny := v.(*any); isAny {
			var x any
			if x, err = sess.b.DecodeAny(); err == nil {
				*ap = x
			}
		} else {
			err = sess.b.Decode(reflect.ValueOf(v).Elem())
		}
	}
	if err != nil {
		if sc != nil {
			// Inside a hook: the read around it uses the positions as they are.
			err = convertErrorKeep(err, s.src, &cfg, 0, 0)
		} else {
			err = convertError(err, s.src, &cfg, 0, 0)
		}
	}
	sess.b.Release()
	sessionPool.Put(sess)
	return err
}

// treeInput replays a stored tree as parse events, for the binder.
type treeInput struct {
	s     *docStream
	doc   int
	w     walker
	state uint8
}

func (in *treeInput) Next() (parse.Event, error) {
	switch in.state {
	case 0:
		in.state = 1
		return parse.Event{Kind: parse.StreamStart}, nil
	case 1:
		in.state = 2
		return parse.Event{Kind: parse.DocumentStart}, nil
	case 2:
		if ev, ok := in.w.next(); ok {
			return ev, nil
		}
		in.state = 3
		return parse.Event{Kind: parse.DocumentEnd}, nil
	case 3:
		in.state = 4
		return parse.Event{Kind: parse.StreamEnd}, nil
	}
	return parse.Event{}, io.EOF
}

func (in *treeInput) Source() []byte { return in.s.src }

func (in *treeInput) AppendScalar(dst []byte, ev *parse.Event) ([]byte, error) {
	return parse.AppendScalar(dst, in.s.src, ev)
}

func (in *treeInput) AppendTag(dst []byte, ev *parse.Event) ([]byte, error) {
	if in.s.dec.propTags != nil {
		if n := ev.VersionMajor; n > 0 && n < len(in.s.tree.Nodes) {
			if p := in.s.tree.Nodes[n].Prop; int(p) < len(in.s.dec.propTags) {
				return append(dst, in.s.dec.propTags[p]...), nil
			}
		}
		return dst, nil
	}
	return in.s.appendTag(dst, in.doc, ev.TagStart, ev.TagEnd)
}

// NodeRef implements bind.NodeSource: an event carries the number of its node.
func (in *treeInput) NodeRef(ev *parse.Event) (bind.NodeRef, bool) {
	if ev.VersionMajor <= 0 {
		return bind.NodeRef{}, false
	}
	return bind.NodeRef{Tree: &in.s.tree, Src: in.s.src, Node: int32(ev.VersionMajor), Stream: in.s, Doc: in.doc}, true
}

// appendTag appends the resolved form of the tag written at src[ts:te] in
// document doc. Resolution uses the parser's own directive handling on the text
// from the end of the previous document, so a %TAG directive applies to its
// document only, as when the text is parsed.
func (s *docStream) appendTag(dst []byte, doc, ts, te int) ([]byte, error) {
	d := &s.dec
	if d.tp == nil || d.tpDoc != doc || d.tpGen != s.gen {
		from := s.docTextFrom(doc)
		if d.tp == nil {
			d.tp = parse.New(nil, -1)
		}
		d.tp.Reset(s.src[from:], -1)
		for {
			ev, err := d.tp.Next()
			if err != nil {
				d.tp.Reset(nil, -1)
				d.tp = nil
				return dst, err
			}
			if ev.Kind == parse.DocumentStart {
				break
			}
		}
		d.tpDoc, d.tpGen, d.tpFrom = doc, s.gen, from
	}
	ev := parse.Event{TagStart: ts - d.tpFrom, TagEnd: te - d.tpFrom}
	return d.tp.AppendTag(dst, &ev)
}

// prelude defines the anchors a subtree's aliases need from outside it.
func (s *docStream) prelude(b *bind.Binder, doc int, root int32) error {
	for _, evs := range s.dec.predefs {
		if err := b.Predefine(evs); err != nil {
			return err
		}
	}
	if len(s.dec.predefs) > 0 || root == s.tree.Docs[doc].Root {
		return nil
	}
	for _, evs := range s.externalAnchors(root) {
		if err := b.Predefine(evs); err != nil {
			return err
		}
	}
	return nil
}

type anchorWork struct {
	name   string
	before int32
}

// externalAnchors returns, in document order, the events of the anchored nodes
// before root that aliases in the subtree of root refer to, directly or through
// aliases inside those nodes.
func (s *docStream) externalAnchors(root int32) [][]parse.Event {
	t := &s.tree
	var byName map[string][]int32
	build := func() {
		byName = map[string][]int32{}
		anc := map[int32]bool{}
		for a := t.Nodes[root].Parent; a != 0; a = t.Nodes[a].Parent {
			anc[a] = true
		}
		for j := int32(1); j < root; j++ {
			if p := t.PropOf(j); p != nil && p.HasAnchor() && !anc[j] {
				name := string(s.src[p.AnchorStart:p.AnchorEnd])
				byName[name] = append(byName[name], j)
			}
		}
	}
	// needs lists the alias names in the subtree of n that are not defined earlier
	// inside it.
	needs := func(n int32) []string {
		var w walker
		w.reset(t, n)
		defined := map[string]bool{}
		var out []string
		for {
			ev, ok := w.next()
			if !ok {
				return out
			}
			if ev.HasAnchor() {
				defined[string(s.src[ev.AnchorStart:ev.AnchorEnd])] = true
			}
			if ev.Kind == parse.Alias {
				if name := string(s.src[ev.Start+1 : ev.End]); !defined[name] {
					out = append(out, name)
				}
			}
		}
	}
	var found []int32
	seen := map[int32]bool{}
	var work []anchorWork
	push := func(n int32) {
		for _, name := range needs(n) {
			work = append(work, anchorWork{name, n})
		}
	}
	push(root)
	for len(work) > 0 {
		it := work[len(work)-1]
		work = work[:len(work)-1]
		if byName == nil {
			build()
		}
		cands := byName[it.name]
		k := sort.Search(len(cands), func(i int) bool { return cands[i] >= it.before })
		if k == 0 {
			continue
		}
		j := cands[k-1]
		if seen[j] {
			continue
		}
		seen[j] = true
		found = append(found, j)
		push(j)
	}
	sort.Slice(found, func(i, j int) bool { return found[i] < found[j] })
	out := make([][]parse.Event, 0, len(found))
	for _, j := range found {
		var w walker
		w.reset(t, j)
		var evs []parse.Event
		for {
			ev, ok := w.next()
			if !ok {
				break
			}
			evs = append(evs, ev)
		}
		out = append(out, evs)
	}
	return out
}

// walker produces the events of a subtree of a Tree, in order, on an explicit
// stack. Each node event carries its node number in VersionMajor, which no event
// below a document start uses otherwise.
type walker struct {
	t       *cst.Tree
	root    int32
	started bool
	stack   []wframe
}

type wframe struct{ node, cur int32 }

func (w *walker) reset(t *cst.Tree, root int32) {
	w.t, w.root, w.started, w.stack = t, root, false, w.stack[:0]
}

func (w *walker) next() (parse.Event, bool) {
	if !w.started {
		w.started = true
		return w.open(w.root), true
	}
	if len(w.stack) == 0 {
		return parse.Event{}, false
	}
	f := &w.stack[len(w.stack)-1]
	if f.cur == 0 {
		nd := &w.t.Nodes[f.node]
		kind := parse.SequenceEnd
		if nd.Kind == cst.Mapping {
			kind = parse.MappingEnd
		}
		ev := parse.Event{Kind: kind, Start: int(nd.End), End: int(nd.End), VersionMajor: int(f.node)}
		w.stack = w.stack[:len(w.stack)-1]
		return ev, true
	}
	c := f.cur
	f.cur = w.t.Nodes[c].Next
	return w.open(c), true
}

func (w *walker) open(n int32) parse.Event {
	t := w.t
	nd := &t.Nodes[n]
	ev := parse.Event{Start: int(nd.Start), End: int(nd.End), VersionMajor: int(n)}
	if p := t.PropOf(n); p != nil {
		ev.AnchorStart, ev.AnchorEnd = int(p.AnchorStart), int(p.AnchorEnd)
		ev.TagStart, ev.TagEnd = int(p.TagStart), int(p.TagEnd)
	}
	switch nd.Kind {
	case cst.Scalar:
		ev.Kind, ev.Style, ev.Indent = parse.Scalar, nd.Style, int(nd.Indent)
	case cst.Alias:
		ev.Kind = parse.Alias
	default:
		ev.Kind, ev.Flow, ev.Pair = parse.SequenceStart, nd.Flow, nd.Pair
		if nd.Kind == cst.Mapping {
			ev.Kind = parse.MappingStart
		}
		w.stack = append(w.stack, wframe{node: n, cur: w.t.First(n)})
	}
	return ev
}
