package oneyaml

import (
	"bytes"
	"errors"
	"io"
	"math"
	"strconv"
	"unicode/utf8"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/emit"
	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/pointer"
	"oneyaml.dev/go/internal/scan"
)

// A Document is a YAML stream held losslessly: every byte of the input,
// including comments, blank lines, indentation and line breaks, comes back from
// Bytes unless an edit changed it.
//
// The methods address the first document of the stream; Len and Doc reach the
// others. A Document returned by Doc is a view that shares the stream: an edit
// through it changes the stream, and every view sees the change.
//
// Edits are exact and minimal. Set writes the new value in the style of the
// value it replaces when that style can hold it, and falls back to the nearest
// style that can otherwise; text outside the edited value is not touched. An
// edit that fails leaves the document as it was. An edit makes every Node read
// before it stale; Node.Stale reports it. An edit, patch or merge patch that
// fails is rolled back completely and leaves the Nodes read before it valid. Read
// nodes again after editing, or collect their pointers first (Node.Pointer).
// A Document is not safe for concurrent use.
//
// A nil *Document, which is what a failed Parse returns, is safe to call: getters
// return zero values (Len is 0, Bytes is nil, Root is the zero Node) and methods
// that decode, edit or patch return an error matching ErrNilDocument.
type Document struct {
	s   *docStream
	idx int
}

// docStream is the state shared by all views of one stream.
type docStream struct {
	src  []byte // current text, UTF-8
	tree cst.Tree
	cfg  config

	// orig is the input exactly as given when it was not UTF-8; it is returned
	// as long as no edit has changed the stream. enc describes how to write the
	// edited text back in the original encoding.
	orig []byte
	enc  any

	// The flags and the counter share one word, which keeps the struct small: one
	// is made for every Document that is parsed.
	dirty bool
	// shared reports that a snapshot holds the slices of tree, so an edit that
	// changes the tree in place must copy them first, and the slices must not be
	// reused for the next parse.
	shared bool
	// During a patch (batch) the intermediate texts are nobody's but the stream's,
	// so a buffer the stream allocated itself (owned) can be edited in place or
	// reused for the next text, instead of allocating a copy of the whole stream per
	// operation. A buffer a snapshot refers to is never owned, and Bytes is never
	// reachable in the middle of a patch, so no text a caller holds is changed.
	batch bool
	// decoding is set while a Decode reads the tree: an Unmarshaler it calls may
	// edit the Document, and then no offset may be left stale for the reader.
	decoding bool
	// owned reports that src is a buffer the stream allocated and nothing else
	// refers to: Bytes has not handed it out and no snapshot holds it. Such a
	// buffer is edited in place, so an edit costs the text it changes and not a
	// copy of the stream; any reader of the buffer clears the flag, and the next
	// edit then writes a new buffer.
	owned bool
	// gen counts changes to the tree; a Node from an earlier generation is stale.
	gen uint32

	dec decodeState // see document_decode.go
	// rng holds the loops over Items and Entries that are running (see node.go),
	// and rngID numbers them. The first live in the stream, in a few fixed slots,
	// so that a loop that does not edit costs no allocation; the rest are in the edit
	// state (rngMore), which only a fourth running loop makes.
	rng   [rangeSlots]rangeSlot
	rngID uint32

	// ed is made by the first edit: a stream that is only read does not carry it.
	ed *editState
}

// editState is what edits keep between them.
type editState struct {
	// spare holds the slices of an earlier tree that nothing refers to any more,
	// so the next edit can parse into them instead of allocating.
	spare cst.Tree

	// rngPath is where the collection of each running loop is, noted before an
	// edit; see rangeSlot.
	rngPath [rangeSlots]rangePath

	// rngMore holds the loops past the fixed slots; see node_range.go. It is here
	// so that a Document that never runs four loops at once does not carry it.
	rngMore []rangeMore

	// spareSrc is the previous text during a patch, ready to be reused.
	spareSrc []byte

	// ovEpoch, valid while the stream's gen is ovGen, replaces gen as the epoch
	// Nodes carry. A rolled-back change restores the tree it started from, so the
	// Nodes read before it stay valid; gen itself still moves on, because caches
	// are keyed by it.
	ovEpoch, ovGen uint32

	// rs is scratch for parsing the edited region of an edit, made by the first
	// region edit and reused, so later ones allocate nothing for it.
	rs *regionScratch

	// lost reports that the tree was lost and the text could not be parsed again;
	// see lose.
	lost bool

	// cache holds the indexes of the anchors and of the keys of large mappings.
	cache edit.Cache

	// work counts what edits had to parse; tests use it to show that an edit
	// costs the size of its region and not of the stream.
	work editWork
}

// es returns the edit state, making it on first use.
func (s *docStream) es() *editState {
	if s.ed == nil {
		s.ed = new(editState)
	}
	return s.ed
}

// editWork is the parsing done on behalf of edits.
type editWork struct {
	bytes, nodes int // text scanned and nodes built
	regions      int // edits committed through a region
	shifted      int // nodes that came after the edited run in those edits, and were moved
	moved        int // bytes of text that came after the edited run in those edits, and were moved
	inline       int // edits that replaced one scalar in place
	wholes       int // edits that parsed the whole stream
	batches      int // runs of removals made in one pass
	batchTries   int // runs of removals offered to the one-pass path that could pay
	cursorWalks  int // times a child was looked up by walking from the first child of its collection
}

// Parse reads data as a YAML stream and returns it as a Document. The data is
// copied; later changes to the slice do not affect the Document.
//
// A stream with no documents (empty input) yields a Document of length 0.
// Syntax errors are reported as *SyntaxError and exceeded limits as *LimitError.
func Parse(data []byte, opts ...Option) (*Document, error) {
	cfg := newConfig(opts)
	if m := cfg.limits.MaxInputBytes; m > 0 && len(data) > m {
		return nil, &LimitError{Limit: "input size", Max: m, Option: "Limits or NoLimits"}
	}
	if len(data) > math.MaxInt32-1 {
		// Node offsets are 32-bit to keep the tree small.
		return nil, &LimitError{Limit: "input size", Max: math.MaxInt32 - 1, Option: "none: split the stream"}
	}
	s := &docStream{cfg: cfg}
	if decodeDocInput != nil {
		u, enc, err := decodeDocInput(data)
		if err != nil {
			return nil, err
		}
		if enc != nil {
			s.src, s.enc, s.orig = u, enc, append([]byte(nil), data...)
		}
	}
	if s.src == nil {
		// No spare room: a stream that is only read should not pay for edits. The
		// first edit that adds text copies the text once, with room for the next.
		s.src = append([]byte(nil), data...)
	}
	s.owned = true
	if err := s.build(); err != nil {
		return nil, err
	}
	return &Document{s: s}, nil
}

func (s *docStream) depth() int {
	if d := s.cfg.limits.MaxDepth; d > 0 {
		return d
	}
	return -1
}

// buildTree parses src into t and counts the work.
func (s *docStream) buildTree(t *cst.Tree, src []byte, maxDepth int) error {
	if s.ed == nil {
		return cst.Build(t, src, maxDepth)
	}
	s.ed.work.bytes += len(src)
	err := cst.Build(t, src, maxDepth)
	s.ed.work.nodes += len(t.Nodes)
	return err
}

func (s *docStream) build() error {
	if max := s.cfg.limits.MaxDocuments; max > 0 {
		// Stop at the first document over the limit instead of building a tree
		// for a stream of millions of them.
		var b cst.Builder
		b.ResetWhole(&s.tree, s.src)
		p := parse.New(s.src, s.depth())
		for {
			ev, err := p.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return docSyntaxError(err, s.cfg.limits)
			}
			b.Event(&ev)
			if len(s.tree.Docs) > max {
				return &LimitError{Limit: "documents", Max: max, Option: "Limits or NoLimits"}
			}
		}
		b.Finish()
		return s.checkLimits()
	}
	if err := s.buildTree(&s.tree, s.src, s.depth()); err != nil {
		return docSyntaxError(err, s.cfg.limits)
	}
	return s.checkLimits()
}

func (s *docStream) checkLimits() error {
	lim := s.cfg.limits
	if lim.MaxDocuments > 0 && len(s.tree.Docs) > lim.MaxDocuments {
		return &LimitError{Limit: "documents", Max: lim.MaxDocuments, Option: "Limits or NoLimits"}
	}
	if lim.MaxScalarBytes > 0 {
		for i := range s.tree.Nodes {
			nd := &s.tree.Nodes[i]
			if nd.Kind == cst.Scalar && int(nd.End-nd.Start) > lim.MaxScalarBytes {
				return &LimitError{Limit: "scalar size", Max: lim.MaxScalarBytes, Option: "Limits or NoLimits"}
			}
		}
	}
	return nil
}

// Len returns the number of documents in the stream.
func (d *Document) Len() int {
	if d.isNil() {
		return 0
	}
	return len(d.s.tree.Docs)
}

// isNil reports whether d is nil or has no stream (the zero Document).
func (d *Document) isNil() bool { return d == nil || d.s == nil }

// Doc returns a view of document i of the stream. The view shares the stream with
// d. If i is out of range every operation on the view reports ErrNotFound.
func (d *Document) Doc(i int) *Document {
	if d.isNil() {
		return nil
	}
	return &Document{s: d.s, idx: i}
}

// Bytes returns the stream as text. The slice must not be modified. It stays
// valid after later edits, which build new text instead of changing it.
func (d *Document) Bytes() []byte {
	if d.isNil() {
		return nil
	}
	return d.bytes(true)
}

// bytes is Bytes; expose says the caller keeps the slice, so the stream must not
// change it in place any more.
func (d *Document) bytes(expose bool) []byte {
	s := d.s
	if s.enc != nil {
		if !s.dirty {
			return s.orig
		}
		if encodeDocOutput != nil {
			return encodeDocOutput(s.src, s.enc)
		}
	}
	if expose {
		s.owned = false
	}
	// The capacity is the stream's room to grow in place; a caller that appends
	// to the slice must not write into it.
	return s.src[:len(s.src):len(s.src)]
}

// String returns the text of the stream, so that printing a *Document prints its
// YAML. A nil Document gives "".
func (d *Document) String() string {
	if d.isNil() {
		return ""
	}
	return string(d.bytes(false))
}

// WriteTo writes the stream to w.
func (d *Document) WriteTo(w io.Writer) (int64, error) {
	if d.isNil() {
		return 0, ErrNilDocument
	}
	n, err := w.Write(d.bytes(false))
	return int64(n), err
}

// valid reports whether the view addresses an existing document.
func (d *Document) valid() bool { return !d.isNil() && d.idx >= 0 && d.idx < len(d.s.tree.Docs) }

// Root returns the root node of the document. The zero Node is returned if the
// document does not exist.
func (d *Document) Root() Node {
	if !d.valid() {
		return Node{}
	}
	return d.node(d.s.tree.Docs[d.idx].Root)
}

// Set replaces the value at the RFC 6901 pointer ptr with v, or adds it when the
// pointer names a mapping key that does not exist or the end of a sequence ("-" or
// the length).
//
// v is anything Marshal accepts. The value that is replaced keeps its style (plain,
// quoted, literal or folded block, flow or block) when that style can hold the new
// value; otherwise only that value falls back to the nearest style that can. A
// new member goes at the end of its mapping and copies the style of its
// siblings. Reading the document back gives exactly v.
//
// Set refuses to write below an alias (ErrAliasWrite) and a key that occurs twice
// (ErrAmbiguousKey). Replacing the alias node itself is allowed. On any error the
// document is unchanged and the error is an *EditError.
func (d *Document) Set(ptr string, v any) error {
	if d.isNil() {
		return &EditError{Op: "set", Pointer: ptr, Kind: ErrNilDocument}
	}
	val, err := valueOf(v, &d.s.cfg)
	if err != nil {
		return staleAsEdit("set", ptr, err)
	}
	return d.s.edit(d.idx, opSet, ptr, &val)
}

// Remove deletes the value at ptr, with its key or its place in the sequence. A
// comment on the line of the entry, a comment directly above it and a comment
// inside its value go with it. Removing the last entry of a nested collection
// leaves {} or []. Removing an anchored node that an alias still uses is refused
// (ErrAnchorInUse).
func (d *Document) Remove(ptr string) error {
	if d.isNil() {
		return &EditError{Op: "remove", Pointer: ptr, Kind: ErrNilDocument}
	}
	return d.s.edit(d.idx, opRemove, ptr, nil)
}

// valueOf converts a Go value to the form the edit engine writes.
func valueOf(v any, cfg *config) (edit.Value, error) {
	maxDepth := cfg.limits.MaxDepth
	switch x := v.(type) {
	case nil:
		return edit.Value{Kind: edit.Null, Text: "null"}, nil
	case bool:
		return edit.Value{Kind: edit.Bool, Text: strconv.FormatBool(x)}, nil
	case string:
		return edit.Str(x), nil
	case int:
		return edit.Value{Kind: edit.Int, Text: strconv.Itoa(x)}, nil
	case int64:
		return edit.Value{Kind: edit.Int, Text: strconv.FormatInt(x, 10)}, nil
	case uint64:
		return edit.Value{Kind: edit.Int, Text: strconv.FormatUint(x, 10)}, nil
	case Node:
		if x.s == nil {
			return edit.Value{Kind: edit.Null, Text: "null"}, nil
		}
		if x.nd() == nil {
			return edit.Value{}, ErrStaleNode
		}
		// Aliases are written out: the anchor they name may not exist where the
		// value is going, or may mean something else there. The expansion is
		// charged to the alias bounds, at the size of the document the node is from.
		c := edit.NewCtx(&x.s.tree, x.s.src, int(x.doc))
		cp := x.s.copier(cfg)
		cp.Old, cp.New, cp.Dest = c, c, -1
		v, err := cp.Value(x.i)
		if errors.Is(err, edit.ErrDepth) {
			return v, editKind(err)
		}
		return v, aliasError(err, "")
	}
	enc := encoderPool.Get().(*emit.Encoder)
	enc.Configure(emit.Config{MaxDepth: maxDepth})
	enc.Reset()
	err := enc.Document(v)
	var out []byte
	if err == nil {
		out = append(out, enc.Bytes()...)
	}
	if cap(enc.Bytes()) <= poolLimit {
		encoderPool.Put(enc)
	}
	if err != nil {
		return edit.Value{}, convertEmitError(err)
	}
	return edit.FromYAML(out)
}

// ---- the edit pipeline ----

type editKindOp uint8

const (
	opSet     editKindOp = iota // replace or add, whichever applies
	opAdd                       // RFC 6902 add
	opReplace                   // RFC 6902 replace
	opRemove                    // RFC 6902 remove
)

func (o editKindOp) name() string {
	return [...]string{"set", "add", "replace", "remove"}[o]
}

// edit applies one operation to document doc and commits it only if the result
// parses and reads back as intended.
func (s *docStream) edit(doc int, op editKindOp, ptr string, v *edit.Value) error {
	s.freezeRanges()
	gen := s.gen
	err := s.editTree(doc, op, ptr, v)
	if err == nil && s.gen != gen && s.ranging() {
		s.noteEdit(doc, op, ptr)
	}
	return err
}

func (s *docStream) editTree(doc int, op editKindOp, ptr string, v *edit.Value) error {
	fail := func(kind error) error { return &EditError{Op: op.name(), Pointer: ptr, Kind: kind} }
	if op != opSet || s.batch || s.decoding {
		s.tree.Settle() // only a Set outside a patch works on stale offsets
	}
	if len(s.tree.Docs) == 0 && doc == 0 && ptr == "" && op != opRemove && op != opReplace {
		return s.firstDocument(v, fail)
	}
	if doc < 0 || doc >= len(s.tree.Docs) {
		return fail(ErrNotFound)
	}
	toks, err := pointer.Parse(ptr)
	if err != nil {
		return fail(ErrInvalidPointer)
	}
	if s.es().lost {
		return fail(errInternal)
	}
	var limitErr error
	genBefore := s.gen
	for attempt := 0; attempt < 2; attempt++ {
		opt := edit.Options{Safe: attempt == 1}
		// A run of Sets leaves the offsets far down the tree stale (see
		// cst.ShiftAfterNode). The next Set is planned on such a tree, which is fine
		// when its target is among the nodes that were true when planning began; if
		// it is not, or the plan fails, it is planned again on a settled tree.
		lazy := attempt == 0 && op == opSet && !s.batch && !s.decoding && s.tree.Lagging() && v.IsScalar() && v.Kind != edit.Alias
		front := s.tree.Frontier()
		var c *edit.Ctx
		if lazy {
			// Only the replacement of a scalar near the nodes that are true is
			// planned on stale offsets: other plans (a new key, a collection) read
			// the tree wherever they like. Resolving reads keys, which settle
			// themselves.
			c = s.ctxLagging(doc)
			c.Merge = s.cfg.mergeKeys
			c.MergeAddOnly = op == opSet || op == opAdd
			loc, rerr := c.Resolve(toks, true)
			if rerr != nil || loc.Node == 0 || loc.Merged || loc.Node+lagMargin > front || s.tree.Nodes[loc.Node].Kind != cst.Scalar {
				lazy = false
			}
		}
		if !lazy {
			s.tree.Settle()
			c = s.ctx(doc)
			c.Merge = s.cfg.mergeKeys
			c.MergeAddOnly = op == opSet || op == opAdd
		}
		pl, err := s.plan(c, op, toks, v, opt)
		if err != nil {
			return fail(editKind(err))
		}
		repls, check := pl.repls, pl.check
		if len(repls) == 0 {
			return nil
		}
		if attempt == 0 && s.patchScalar(pl.target, repls, v) {
			s.ed.cache.KeepKeys(genBefore, s.gen)     // a scalar value changed; every key is where it was
			s.keepCursor(genBefore, 0, 0, 0, 0, 0, 0) // and every node
			return nil
		}
		if lazy {
			// Not a plain scalar replacement after all: take the general way on true
			// offsets.
			s.tree.Settle()
			c = s.ctx(doc)
			c.Merge = s.cfg.mergeKeys
			c.MergeAddOnly = op == opSet || op == opAdd
			if pl, err = s.plan(c, op, toks, v, opt); err != nil {
				return fail(editKind(err))
			}
			repls, check = pl.repls, pl.check
			if len(repls) == 0 {
				return nil
			}
		}
		var gone removedEntry
		if op == opRemove && s.ed.cache.Keys != nil && s.ed.cache.Keys.Built() > 0 {
			gone = s.removedEntry(c, &pl)
		}
		if s.editRegion(c, &pl) {
			s.keysAfterAdd(doc, op, &pl, toks, genBefore)
			s.keysAfterRemove(gone, genBefore)
			return nil
		}
		done, lerr, err := s.applyRepls(repls, check)
		if err != nil {
			return fail(err)
		}
		if lerr != nil && limitErr == nil {
			limitErr = lerr
		}
		if done {
			s.keysAfterAdd(doc, op, &pl, toks, genBefore)
			return nil
		}
	}
	if limitErr != nil {
		return fail(limitErr)
	}
	return fail(errEdit)
}

// applyRepls writes repls into the text, parses the result and keeps it if check
// accepts the new tree. It reports done when the text was kept. When the text was
// refused the stream is as it was and lerr holds a depth limit error if that is
// why. err is an error that ends the edit.
func (s *docStream) applyRepls(repls []edit.Repl, check func(nt *cst.Tree, out []byte, rg *regionInfo) bool) (done bool, lerr, err error) {
	out, err := edit.Apply(s.takeBuf(), s.src, repls)
	if err != nil {
		return false, nil, editKind(err)
	}
	// Parse into the slices of the current tree when nothing else holds them:
	// the old tree is not needed once the new text is written, and if the new
	// text is refused it is parsed again from the old one. A shared tree (a
	// snapshot holds it) gets slices of its own.
	inPlace := !s.shared
	var nt cst.Tree
	if inPlace {
		nt = s.tree
	} else {
		nt = s.takeSpare()
	}
	s.ed.work.wholes++
	growNodes(&nt, len(out), len(s.src), len(s.tree.Nodes))
	if err := s.buildTree(&nt, out, s.depth()); err != nil || !check(&nt, out, nil) {
		if err != nil {
			// Text that nests past the depth limit is a limit error, not a failed edit.
			if le, ok := docSyntaxError(err, s.cfg.limits).(*LimitError); ok {
				lerr = le
			}
		}
		if inPlace {
			if err := s.buildTree(&nt, s.src, s.depth()); err != nil {
				// The text parsed when the edit began, so this does not happen. The
				// old tree is gone, though: stop, keep the text, and refuse
				// further edits instead of going on with a tree that does not match.
				s.lose()
				return false, nil, errInternal
			}
			s.tree = nt
		} else {
			s.es().spare = nt // nothing else has seen it
		}
		return false, lerr, nil
	}
	s.setSrc(out)
	s.tree, s.dirty, s.shared = nt, true, false
	s.gen++
	return true, nil, nil
}

// growNodes gives t room for the nodes that text of n bytes is likely to hold,
// judged from a document of oldLen bytes with oldNodes nodes. Building a large
// tree by appends alone leaves arrays of every size on the way behind it.
func growNodes(t *cst.Tree, n, oldLen, oldNodes int) {
	if oldLen == 0 || n < 1<<20 {
		return
	}
	if want := int(int64(oldNodes)*int64(n)/int64(oldLen)) + oldNodes/8; cap(t.Nodes) < want {
		t.Nodes = make([]cst.Node, 0, want)
	}
}

// keysAfterAdd keeps the key index of the mapping an edit added a key to. An edit
// that put a new key at the end of its mapping leaves every other key where it was
// (nodes before the end keep their place), so the index only gains the new key;
// any other edit leaves the index at the old version, which makes it stale.
func (s *docStream) keysAfterAdd(doc int, op editKindOp, pl *planned, toks []string, from uint32) {
	if s.ed.cache.Keys != nil && (op == opAdd || op == opSet) && pl.target == 0 && pl.parent != 0 && len(toks) > 0 {
		s.ed.cache.AddKey(s.ctx(doc), pl.parent, toks[len(toks)-1], from, s.gen)
	}
}

// removedEntry names the nodes of a mapping entry about to be removed, to keep the
// key index of its mapping when a region edit removes just those nodes.
type removedEntry struct {
	cn, lo, hi int32
	nodes      int // nodes of the tree before the edit
}

func (s *docStream) removedEntry(c *edit.Ctx, pl *planned) removedEntry {
	// Taken from the plan: looking the key up again would count as one more scan
	// of the mapping.
	t := &s.tree
	if pl.anchor == 0 || pl.parent == 0 || t.Nodes[pl.parent].Kind != cst.Mapping || t.Nodes[pl.anchor].Parent != pl.parent {
		return removedEntry{}
	}
	key := t.Prev(pl.anchor)
	if key == 0 || t.Nodes[key].Parent != pl.parent {
		return removedEntry{}
	}
	return removedEntry{cn: pl.parent, lo: key, hi: c.LastDescendant(pl.anchor), nodes: len(t.Nodes)}
}

// keysAfterRemove keeps the key index of the mapping an edit removed an entry
// from, when the edit took out those nodes and no others.
func (s *docStream) keysAfterRemove(r removedEntry, from uint32) {
	if r.cn == 0 {
		return
	}
	if r.nodes-len(s.tree.Nodes) != int(r.hi-r.lo)+1 {
		return // the edit changed more than the entry; the index stays at its old version
	}
	s.ed.cache.RemoveKey(r.cn, r.lo, r.hi, from, s.gen)
}

// neighbour is the value of an entry beside a change and where it must be after.
type neighbour struct {
	newIdx int
	want   edit.Value
}

// neighbours records the entries on both sides of an insertion (removed false,
// at entry pos) or a removal (removed true) in container par, so that the edited
// text can be checked to leave them as they were. Text added after a block
// scalar can change its value (a final line break appears); this catches it.
func (s *docStream) neighbours(par int32, pos int, removed bool) []neighbour {
	t := &s.tree
	mapping := t.Nodes[par].Kind == cst.Mapping
	step := 1
	entries := t.Count(par)
	if mapping {
		step = 2
		entries /= 2
	}
	// One walk to the entry before pos, then on through the one after.
	lo := max(pos-1, 0)
	var cur int32
	if back := entries - 1 - lo; entries > 0 && back >= 0 && back <= 4 {
		// Near the end (an append, or the removal of the last entries): count back
		// from the last entry, which does not depend on the size of the container.
		cur = t.Last(par)
		if mapping {
			cur = t.Prev(cur)
		}
		for ; back > 0 && cur != 0; back-- {
			for i := 0; i < step && cur != 0; i++ {
				cur = t.Prev(cur)
			}
		}
	} else {
		cur = t.First(par)
		for i := lo * step; i > 0 && cur != 0; i-- {
			cur = t.Nodes[cur].Next
		}
	}
	var out []neighbour
	for e := lo; e <= pos+1 && e < entries && cur != 0; e++ {
		val := cur
		if mapping {
			if val = t.Nodes[cur].Next; val == 0 {
				break
			}
		}
		newIdx := -1
		switch {
		case e == pos-1:
			newIdx = e
		case removed && e == pos+1:
			newIdx = pos
		case !removed && e == pos:
			newIdx = pos + 1
		}
		if newIdx >= 0 {
			if v, err := edit.FromTree(t, s.src, val); err == nil {
				out = append(out, neighbour{newIdx, v})
			}
		}
		cur = t.Nodes[val].Next
	}
	return out
}

// regionInfo tells a check that it is looking at the tree of one region of the
// stream, not the whole: the region's root is the container that was edited.
type regionInfo struct {
	skip    int // tokens of the pointer that lead to the container
	before  int // entries of the container that come before the region
	outside int // children of the container that are not in the region

	// The old nodes the region replaces, for a check that compares the run with
	// what it was: nodes lo..hi of the stream's tree, children of cn; shift is what
	// the region's offsets lack to be stream offsets.
	lo, hi, cn int32
	shift      int32
}

// neighboursOK checks the recorded neighbours against the new tree.
func neighboursOK(nt *cst.Tree, out []byte, par int32, ns []neighbour, rg *regionInfo) bool {
	mapping := nt.Nodes[par].Kind == cst.Mapping
	for _, nb := range ns {
		i := nb.newIdx
		if rg != nil && par == 1 {
			i -= rg.before
		}
		if i < 0 {
			return false
		}
		if mapping {
			i = 2*i + 1
		}
		n := nt.First(par)
		for ; i > 0 && n != 0; i-- {
			n = nt.Nodes[n].Next
		}
		if n == 0 || !edit.Equal(nt, out, n, &nb.want) {
			return false
		}
	}
	return true
}

// planned is an edit ready to be written.
type planned struct {
	repls []edit.Repl
	// check verifies the edited text: the value reads back and the entries beside
	// it are as they were. It works on the whole new tree (rg nil) or on the tree
	// of the region that holds the edit.
	check  func(nt *cst.Tree, out []byte, rg *regionInfo) bool
	target int32 // the node replaced in place, if any
	parent int32 // the container the edit changes
	// margin asks for the entries beside the edit to be part of the region, because
	// the check reads them.
	margin bool
	// anchor is a node of the edit's entry in the container it changes: the node
	// replaced or removed, or the last child when the edit appends.
	anchor int32
}

// checkCtx makes the context to read the edited text with: for a region, the
// region's tree and the tokens below its root.
func checkCtx(nt *cst.Tree, out []byte, rg *regionInfo, doc int, toks []string) (*edit.Ctx, []string) {
	if rg == nil {
		return edit.NewCtxLagging(nt, out, doc), toks
	}
	if rg.skip > len(toks) {
		return nil, nil
	}
	rest := toks[rg.skip:]
	if nt.Nodes[1].Kind == cst.Sequence && len(rest) > 0 {
		// The region holds only the entries from number rg.before on, so an index
		// into the container counts from there.
		i, err := strconv.Atoi(rest[0])
		if err != nil || i < rg.before {
			return nil, nil
		}
		rest = append([]string{strconv.Itoa(i - rg.before)}, rest[1:]...)
	}
	return edit.NewCtxLagging(nt, out, 0), rest
}

// parentIn finds the container again in the edited tree.
func parentIn(nt *cst.Tree, out []byte, rg *regionInfo, doc int, parentToks []string) int32 {
	nc, pt := checkCtx(nt, out, rg, doc, parentToks)
	if nc == nil {
		return 0
	}
	l, err := nc.Resolve(pt, false)
	if err != nil {
		return 0
	}
	return l.Node
}

// plan resolves the pointer and plans the operation.
func (s *docStream) plan(c *edit.Ctx, op editKindOp, toks []string, v *edit.Value, opt edit.Options) (pl planned, err error) {
	loc, err := c.Resolve(toks, true)
	if err != nil {
		return pl, err
	}
	if loc.Merged && (op == opRemove || op == opReplace) {
		// The key is not in this mapping but in a merge source: removing or
		// replacing it would write into the source, through an alias.
		return pl, edit.MergedKeyError(loc.Tok)
	}
	doc := c.Doc
	var parentToks []string
	if len(toks) > 0 {
		parentToks = toks[:len(toks)-1]
	}
	pl.parent = loc.Parent
	pl.anchor = loc.Node
	if pl.anchor == 0 && loc.Parent != 0 {
		pl.anchor = s.tree.Last(loc.Parent)
	}
	switch op {
	case opRemove:
		repls, err := c.Remove(loc)
		if err != nil {
			return pl, err
		}
		pn := &s.tree.Nodes[loc.Parent]
		pos := 0
		for ch := s.tree.First(loc.Parent); ch != loc.Node && ch != 0; ch = s.tree.Nodes[ch].Next {
			pos++
		}
		wantCount := s.tree.Count(loc.Parent) - 1
		if pn.Kind == cst.Mapping {
			pos = (pos - 1) / 2
			wantCount--
		}
		ns := s.neighbours(loc.Parent, pos, true)
		pl.repls, pl.margin = repls, len(ns) > 0
		pl.check = func(nt *cst.Tree, out []byte, rg *regionInfo) bool {
			p := parentIn(nt, out, rg, doc, parentToks)
			if p == 0 {
				return false
			}
			cnt := nt.Count(p)
			if rg != nil && p == 1 {
				cnt += rg.outside
			}
			return cnt == wantCount && neighboursOK(nt, out, p, ns, rg)
		}
		return pl, nil
	}
	readToks := toks
	var ns []neighbour
	var repls []edit.Repl
	switch {
	case loc.Node != 0 && (op == opAdd) && loc.Parent != 0 && s.tree.Nodes[loc.Parent].Kind == cst.Sequence:
		repls, err = c.Add(loc, v, opt) // insert before the existing item
		ns = s.neighbours(loc.Parent, loc.Index, false)
	case loc.Node != 0:
		repls, err = c.Replace(loc, v, opt)
		pl.target = loc.Node
	case op == opReplace:
		return planned{}, edit.ErrNotFound
	default:
		repls, err = c.Add(loc, v, opt)
		pos := loc.Index
		if s.tree.Nodes[loc.Parent].Kind == cst.Mapping {
			pos = s.tree.Count(loc.Parent) / 2
		}
		ns = s.neighbours(loc.Parent, pos, false)
		if err == nil && loc.Dash && len(toks) > 0 {
			readToks = append(append([]string(nil), parentToks...), strconv.Itoa(loc.Index))
		}
	}
	if err != nil {
		return planned{}, err
	}
	pl.repls, pl.margin = repls, len(ns) > 0
	pl.check = func(nt *cst.Tree, out []byte, rg *regionInfo) bool {
		nc, rt := checkCtx(nt, out, rg, doc, readToks)
		if nc == nil {
			return false
		}
		nl, err := nc.Resolve(rt, false)
		if err != nil || nl.Node == 0 || !edit.Equal(nt, out, nl.Node, v) {
			return false
		}
		return len(ns) == 0 || neighboursOK(nt, out, nl.Parent, ns, rg)
	}
	return pl, nil
}

// docSnapshot captures the stream state so a multi-step change can be undone.
//
// A light snapshot holds only the text, which is never changed in place: undoing
// parses it again, which costs nothing when nothing changed and one parse after a
// failure, and holds no second tree while the steps run. A tree snapshot also
// keeps the tree, for a step that reads the old tree after changing the stream (a
// move); it marks the slices shared so that the one edit that changes a tree in
// place (patchScalar) copies them first.
type docSnapshot struct {
	src   []byte
	tree  *cst.Tree
	dirty bool
	epoch uint32
	rng   []savedRange // loops that had already noted their place; see rollback
}

// epoch is the version of the tree that Nodes carry: a Node is valid while its
// epoch is the stream's.
func (s *docStream) epoch() uint32 {
	if e := s.ed; e != nil && s.gen == e.ovGen && e.ovGen != 0 {
		return e.ovEpoch
	}
	return s.gen
}

func (s *docStream) snapshot() docSnapshot {
	s.owned = false
	return docSnapshot{src: s.src, dirty: s.dirty, epoch: s.epoch(), rng: s.saveRanges()}
}

func (s *docStream) snapshotTree() docSnapshot {
	s.tree.Settle()
	s.owned = false
	s.shared = true
	t := s.tree
	return docSnapshot{s.src, &t, s.dirty, s.epoch(), s.saveRanges()}
}

// lose records that the tree no longer matches the text. The text stays as it
// was, so Bytes still returns it; the tree is empty and edits are refused.
func (s *docStream) lose() {
	s.tree = cst.Tree{Nodes: make([]cst.Node, 1), Props: make([]cst.Prop, 1)}
	s.shared = false
	s.es().lost = true
	s.gen++
}

// restore puts a snapshot back. It returns an error only if the text of the
// snapshot does not parse, which does not happen; the stream then stays as it is.
func (s *docStream) restore(sn docSnapshot) error {
	switch {
	case sn.tree != nil:
		s.tree = *sn.tree
		s.shared = true // the snapshot may be restored again, or still be read
	case len(sn.src) == len(s.src) && (len(sn.src) == 0 || &sn.src[0] == &s.src[0]):
		// Nothing replaced the text, and the tree only ever matches it.
		s.dirty = sn.dirty
		return nil
	default:
		nt := s.es().spare
		s.es().spare = cst.Tree{}
		if err := s.buildTree(&nt, sn.src, s.depth()); err != nil {
			// The text parsed when it was taken; this does not happen. The current
			// tree was not touched and still matches the current text.
			s.es().spare = nt
			return errInternal
		}
		if !s.shared {
			s.es().spare = s.tree
		}
		s.tree, s.shared = nt, false
	}
	s.src, s.dirty, s.owned = sn.src, sn.dirty, false
	// The text, and with it the tree, is what it was when the snapshot was taken
	// (a parse of the same text gives the same nodes), so Nodes read then are
	// valid again.
	s.gen++
	e := s.es()
	e.ovEpoch, e.ovGen = sn.epoch, s.gen
	return nil
}

// rollback puts a snapshot back after a failed multi-step change and reports
// whether it could. If the snapshot's text does not parse (it did when it was
// taken, so this does not happen) the text is still put back, so the bytes are
// those from before the change, and the stream is marked lost: further edits are
// refused, never applied to a tree that does not match.
func (s *docStream) rollback(sn docSnapshot) error {
	err := s.restore(sn)
	if err != nil {
		s.src, s.dirty, s.owned = sn.src, sn.dirty, false
		s.lose()
		return err
	}
	s.restoreRanges(sn)
	return nil
}

// beginBatch marks the start of a multi-step change; the returned function ends it.
func (s *docStream) beginBatch() (end func()) {
	s.freezeRanges()
	s.batch = true
	return func() {
		s.batch = false
		if s.ed != nil {
			s.ed.spareSrc = nil
		}
	}
}

// takeBuf returns an empty buffer to build the next text in.
func (s *docStream) takeBuf() []byte {
	if b := s.es().spareSrc; b != nil {
		s.es().spareSrc = nil
		return b[:0]
	}
	return make([]byte, 0, len(s.src)+128)
}

// setSrc makes out, a buffer the stream allocated, the current text.
func (s *docStream) setSrc(out []byte) {
	if s.batch && s.owned {
		s.es().spareSrc = s.src
	}
	s.owned = true
	s.src = out
}

// takeSpare returns a tree to parse into: the slices of an earlier tree if one is
// free, otherwise empty ones sized for the current tree, since an edit changes the
// node count by very little and growing a slice by appending allocates a multiple
// of its final size.
func (s *docStream) takeSpare() cst.Tree {
	nt := s.es().spare
	s.es().spare = cst.Tree{}
	if cap(nt.Nodes) == 0 {
		n := len(s.tree.Nodes)
		nt.Nodes = make([]cst.Node, 0, n+n/32+8)
		nt.Props = make([]cst.Prop, 0, len(s.tree.Props)+len(s.tree.Props)/32+4)
		nt.Docs = make([]cst.Doc, 0, len(s.tree.Docs)+1)
	}
	return nt
}

// unshare gives the stream a tree of its own before an in-place change.
func (s *docStream) unshare() {
	s.tree.Settle()
	s.tree = cst.Tree{
		Nodes: append([]cst.Node(nil), s.tree.Nodes...),
		Props: append([]cst.Prop(nil), s.tree.Props...),
		Docs:  append([]cst.Doc(nil), s.tree.Docs...),
	}
	s.shared = false
}

// firstDocument gives a stream that holds no document (empty, or only comments and
// directives) its first one, written after the existing text.
func (s *docStream) firstDocument(v *edit.Value, fail func(error) error) error {
	nl := "\n"
	if i := bytes.IndexAny(s.src, "\r\n"); i >= 0 {
		nl = "\n"
		if s.src[i] == '\r' {
			nl = "\r"
			if i+1 < len(s.src) && s.src[i+1] == '\n' {
				nl = "\r\n"
			}
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		out := append([]byte(nil), s.src...)
		if n := len(out); n > 0 && out[n-1] != '\n' && out[n-1] != '\r' {
			out = append(out, nl...)
		}
		out = append(out, edit.RenderRoot(v, nl, attempt == 1, s.cfg.yaml11)...)
		var nt cst.Tree
		if err := s.buildTree(&nt, out, s.depth()); err != nil || len(nt.Docs) != 1 || !edit.Equal(&nt, out, nt.Docs[0].Root, v) {
			continue
		}
		s.src, s.tree, s.dirty, s.shared = out, nt, true, false
		s.gen++
		return nil
	}
	return fail(errEdit)
}

// cleanText reports whether text holds only characters that a parse of the whole
// stream accepts anywhere: this fast path writes the text without a parse, so
// anything the scanner might refuse (invalid UTF-8, a control character, a byte
// order mark) goes the general way, where a parse decides.
func cleanText(text []byte) bool {
	for i := 0; i < len(text); {
		b := text[i]
		if b < utf8.RuneSelf {
			if (b < 0x20 && b != '\t') || b == 0x7F {
				return false
			}
			i++
			continue
		}
		r, size := utf8.DecodeRune(text[i:])
		if r == utf8.RuneError || !(r == 0x85 || r >= 0xA0 && r <= 0xD7FF || r >= 0xE000 && r <= 0xFFFD && r != 0xFEFF || r >= 0x10000) {
			return false
		}
		i += size
	}
	return true
}

// noLag makes Set shift every later offset at once, as a stream edited by a patch
// does. Tests set it to compare the two ways.
var noLag bool

// lagMargin is how many nodes after its target a lazy plan of a Set may read: the
// pair around the target and its neighbours. A target nearer the frontier than this
// is planned again on a settled tree.
const lagMargin = 16

// ctxLagging is ctx for a plan that works on a tree with stale offsets.
func (s *docStream) ctxLagging(doc int) *edit.Ctx {
	c := edit.NewCtxLagging(&s.tree, s.src, doc)
	c.MaxAliasHops = s.cfg.limits.MaxAliasNodes
	c.YAML11 = s.cfg.yaml11
	s.attach(c)
	return c
}

// patchScalar handles the common edit without parsing again: one inline scalar of
// the tree is replaced by another inline scalar. The tree keeps its shape, so only
// the node itself and the offsets after it change. It reports whether it did the
// edit; if not, nothing has changed and the caller takes the general path.
func (s *docStream) patchScalar(n int32, repls []edit.Repl, v *edit.Value) bool {
	if n == 0 || len(repls) != 1 || !v.IsScalar() || v.Kind == edit.Alias {
		return false
	}
	r := repls[0]
	nd := s.tree.Nodes[n]
	if nd.Kind != cst.Scalar || nd.Style == scan.Literal || nd.Style == scan.Folded ||
		nd.Start >= nd.End || int(nd.Start) != r.Start || int(nd.End) != r.End {
		return false
	}
	text := r.Text
	if len(text) == 0 || bytes.IndexByte(text, '\n') >= 0 || bytes.IndexByte(text, '\r') >= 0 || !cleanText(text) {
		return false
	}
	delta := int32(len(text)) - (nd.End - nd.Start)
	st := scan.Plain
	switch text[0] {
	case '\'':
		st = scan.SingleQuoted
	case '"':
		st = scan.DoubleQuoted
	}
	// Judge the result on a one-node tree and a text that holds only the tag and the
	// new scalar, before touching the real ones: a failed check must leave every
	// offset and byte as it was.
	var pbuf [96]byte
	psrc := pbuf[:0]
	probe := cst.Tree{Nodes: make([]cst.Node, 2)}
	probe.Nodes[1] = nd
	probe.Nodes[1].Prop = 0
	if nd.Prop != 0 {
		if pr := s.tree.Props[nd.Prop]; pr.HasTag() {
			// The probe has no directives, so it gets the tag in its canonical form.
			ctag := edit.CanonTag(&s.tree, s.src, n)
			psrc = append(append(psrc, ctag...), ' ')
			probe.Props = []cst.Prop{{}, {TagStart: 0, TagEnd: int32(len(ctag))}}
			probe.Nodes[1].Prop = 1
		}
	}
	probe.Nodes[1].Start = int32(len(psrc))
	psrc = append(psrc, text...)
	probe.Nodes[1].End = int32(len(psrc))
	probe.Nodes[1].Style, probe.Nodes[1].Indent = st, 0
	if !edit.Equal(&probe, psrc, 1, v) {
		return false
	}
	var out []byte
	if newLen := len(s.src) + int(delta); s.owned && cap(s.src) >= newLen {
		// Edit the stream's own buffer in place.
		out = s.src[:newLen]
		if delta > 0 {
			copy(out[r.End+int(delta):], s.src[r.End:])
		} else if delta < 0 {
			copy(out[r.Start+len(text):], s.src[r.End:])
		}
		copy(out[r.Start:], text)
		s.src = out
	} else {
		out = make([]byte, 0, newLen+newLen/16+32)
		out = append(append(append(out, s.src[:r.Start]...), text...), s.src[r.End:]...)
		s.setSrc(out)
	}
	if s.shared {
		s.unshare()
	}
	if s.batch || s.decoding || noLag {
		s.tree.ShiftAfter(nd.End, delta)
	} else {
		s.tree.ShiftAfterNode(n, nd.End, delta)
	}
	node := &s.tree.Nodes[n]
	node.End = node.Start + int32(len(text))
	node.Style, node.Indent = st, 0
	s.dirty = true
	s.gen++
	s.ed.work.inline++
	return true
}
