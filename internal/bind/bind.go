// Package bind decodes a stream of parse events into Go values.
//
// A Binder pulls events from an Input and writes them into the target value,
// with no intermediate tree. Anchored nodes are the one exception: their
// events are recorded while they are read, so that an alias can be decoded by
// replaying them (the alias budget is charged for every replayed node).
//
// The Input is an interface so that the same binder serves a parser reading
// source text and a document replaying its lossless tree.
package bind

import (
	"errors"
	"io"
	"reflect"
	"strconv"
	"sync/atomic"

	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/resolve"
	"oneyaml.dev/go/internal/scan"
)

// Input is the event source of a Binder.
//
// Event spans index Source. AppendScalar and AppendTag decode a scalar's value
// and an event's resolved tag; AppendTag is only called while the document that
// holds the event is current.
type Input interface {
	Next() (parse.Event, error)
	Source() []byte
	AppendScalar(dst []byte, ev *parse.Event) ([]byte, error)
	AppendTag(dst []byte, ev *parse.Event) ([]byte, error)
}

// Config holds the options a Binder reads.
type Config struct {
	// Dialect selects the scalar resolution and binding rules; the zero value is
	// the YAML 1.2 Core rules of the primary API.
	Dialect Dialect
	// KnownFields makes an unknown struct key an error.
	KnownFields bool
	// MergeKeys turns on "<<" merge keys.
	MergeKeys bool
	// MaxDepth bounds nesting, counting nesting that aliases expand to.
	// Zero means no bound (the typed path still keeps an absolute ceiling).
	MaxDepth int
	// MaxAliasNodes and MaxAliasBytes bound alias expansion; zero means none.
	MaxAliasNodes, MaxAliasBytes int
	// AliasRatio, when positive, raises each alias bound to at least AliasRatio
	// times the document's own size: nodes counted so far, and InputBytes.
	AliasRatio int
	// ExactNodes and ExactBytes keep AliasRatio from raising that bound.
	ExactNodes, ExactBytes bool
	// InputBytes is the size of the input, for AliasRatio.
	InputBytes int
	// MaxScalarBytes bounds the source length of one scalar; zero means none.
	MaxScalarBytes int
	// MaxNumberDigits bounds the digits of a number converted from a scalar; zero means none.
	MaxNumberDigits int
	// Spend, when set, is the alias expansion already paid for by the call this
	// binder is nested in: a type that reads its own node may start another
	// binder, and the budget must not start again with each one.
	Spend *AliasSpend
}

// AliasSpend counts alias expansion across the binders of one top-level call.
type AliasSpend struct {
	Nodes, Bytes int
	// Own is the number of nodes the top-level document holds, for AliasRatio.
	Own int
}

// Scope ties the Nodes handed to one call of a type that reads its own node to
// the alias budget of the call they were made in. While the call runs, a decode
// made from one of its Nodes spends that budget. Once the call has returned the
// scope is closed, so a Node the type kept and decodes later starts a budget of
// its own instead of carrying the old one (or keeping it alive).
type Scope struct {
	spend  *AliasSpend
	closed atomic.Bool
}

// Spend returns the budget to charge, or nil once the call has returned.
func (s *Scope) Spend() *AliasSpend {
	if s == nil || s.closed.Load() {
		return nil
	}
	return s.spend
}

// Close ends the scope.
func (s *Scope) Close() { s.closed.Store(true) }

// Dialect names a set of binding rules.
type Dialect uint8

const (
	// DialectCore is the primary API: Core schema, first type error stops.
	DialectCore Dialect = iota
	// DialectV3 follows yaml.v3: its scalar resolution, its leniencies for typed
	// targets, and type errors collected while decoding goes on.
	DialectV3
)

// hardDepth is the deepest typed (reflect) recursion allowed even when no depth
// limit is configured. Each level costs a few hundred bytes of Go stack, so this
// stays far below the runtime's stack ceiling. Generic values do not recurse.
const hardDepth = 100_000

// Kind says how an Error is to be reported.
type Kind uint8

// Error kinds.
const (
	// KindType: the value does not fit its target.
	KindType Kind = iota + 1
	// KindSyntax: the document is not valid (duplicate key, undefined alias).
	KindSyntax
	// KindLimit: a limit was exceeded.
	KindLimit
)

// Error is a binding failure. Offset indexes the input's Source.
type Error struct {
	Kind   Kind
	Offset int
	// Path is the part of the RFC 6901 pointer found inside generic values; the
	// segments added by the enclosing typed collections are in segs. Read the
	// whole with FullPath or Flatten.
	Path   string
	GoType string
	Msg    string
	// Err is the error a user hook returned.
	Err error
	// Limit, Max and Option describe a KindLimit error.
	Limit  string
	Max    int
	Option string
	// Category names a KindSyntax error.
	Category string
	// Resume marks a type error after which the document can still be read:
	// the value it is about has been consumed whole. A mapping or sequence that
	// receives one keeps the error, goes on with its next entry and reports the
	// errors together when it ends.
	Resume bool
	// Multi holds the errors of an aggregate, in input order. The other fields
	// of an aggregate are not used.
	Multi []*Error
	n     int      // mismatches under an aggregate
	segs  []string // path segments added while unwinding, innermost first
	// Direct marks a mismatch between the value and its own target, as opposed
	// to one found deeper inside it.
	Direct bool
	// First is the offset of the earlier key of a "float-key" error.
	First int
	// Unknown marks a key that no field of the target struct has.
	Unknown bool
}

func (e *Error) Error() string { return e.Msg }

// Unwrap returns the hook error, if any.
func (e *Error) Unwrap() error { return e.Err }

// prepend adds the path segment of an enclosing collection. Segments are kept
// innermost first and joined once, when the path is read: joining as the error
// unwinds would copy the whole path at every level. An aggregate keeps one list
// for all the errors under it, so a level costs the same however many there are.
func (e *Error) prepend(seg string) { e.segs = append(e.segs, seg) }

// appendSeg appends one escaped path segment, with its leading slash.
func appendSeg(dst []byte, seg string) []byte {
	dst = append(dst, '/')
	for i := 0; i < len(seg); i++ {
		switch seg[i] {
		case '~':
			dst = append(dst, '~', '0')
		case '/':
			dst = append(dst, '~', '1')
		default:
			dst = append(dst, seg[i])
		}
	}
	return dst
}

// joinPath joins segments, outermost first, into an RFC 6901 pointer.
func joinPath(segs []string) string {
	var b []byte
	for _, s := range segs {
		b = appendSeg(b, s)
	}
	return string(b)
}

// FullPath is the RFC 6901 pointer of a single error's value.
func (e *Error) FullPath() string {
	var b []byte
	for i := len(e.segs) - 1; i >= 0; i-- {
		b = appendSeg(b, e.segs[i])
	}
	return string(b) + e.Path
}

// Located is one mismatch of an aggregate with its complete path.
type Located struct {
	Err  *Error
	Path string
}

// segsLen is the length of the escaped segments an error has gathered.
func (e *Error) segsLen() int {
	n := 0
	for _, s := range e.segs {
		n++
		for i := 0; i < len(s); i++ {
			n++
			if s[i] == '~' || s[i] == '/' {
				n++
			}
		}
	}
	return n
}

// Flatten returns the mismatches of e in input order, each with its complete
// path. For an error that is not an aggregate it returns e alone. The paths are
// built only while their total length stays within budget: the first mismatch
// is always returned whole, and the ones after it that would pass the budget are
// left out. Their length is added up without building them, so a long path costs
// nothing unless it is kept.
func (e *Error) Flatten(budget int) []Located {
	var out []Located
	var chain []*Error
	total := 0
	stop := false
	var walk func(n *Error, plen int)
	walk = func(n *Error, plen int) {
		plen += n.segsLen()
		chain = append(chain, n)
		defer func() { chain = chain[:len(chain)-1] }()
		if n.Multi != nil {
			for _, c := range n.Multi {
				if walk(c, plen); stop {
					return
				}
			}
			return
		}
		plen += len(n.Path)
		if len(out) > 0 && total+plen > budget {
			stop = true
			return
		}
		b := make([]byte, 0, plen)
		for _, a := range chain {
			for i := len(a.segs) - 1; i >= 0; i-- {
				b = appendSeg(b, a.segs[i])
			}
		}
		b = append(b, n.Path...)
		total += plen
		out = append(out, Located{Err: n, Path: string(b)})
	}
	walk(e, 0)
	return out
}

// withSeg adds a path segment to a binding error as it unwinds; other errors
// pass through.
func withSeg(err error, seg string) error {
	var be *Error
	if errors.As(err, &be) && be.Kind != KindLimit {
		be.prepend(seg)
	}
	return err
}

func withIdx(err error, i int) error {
	if err == nil {
		return nil
	}
	var be *Error
	if errors.As(err, &be) && be.Kind != KindLimit {
		be.prepend(strconv.Itoa(i))
	}
	return err
}

// maxCollected bounds the type errors kept from one document, so that input
// made of nothing but mismatches cannot grow the error without limit.
const maxCollected = 100

// resumable reports whether err is a type error after which decoding can go on.
func resumable(err error) bool {
	be, ok := err.(*Error)
	return ok && be.Resume
}

// leaves is the number of mismatches in e.
func (e *Error) leaves() int {
	if e.Multi == nil {
		return 1
	}
	return e.n
}

// trim returns e cut to its first k mismatches. e is not changed.
func (e *Error) trim(k int) *Error {
	if e.leaves() <= k {
		return e
	}
	c := *e
	c.Multi, c.n = nil, 0
	for _, m := range e.Multi {
		if k == 0 {
			break
		}
		t := m.trim(k)
		k -= t.leaves()
		c.n += t.leaves()
		c.Multi = append(c.Multi, t)
	}
	return &c
}

// collected gathers the mismatches one collection met. The elements are leaves
// or aggregates, kept as a tree so that nothing is copied when it is passed up.
type collected struct {
	errs []*Error
	n    int
}

// add keeps the resumable error err. It reports true when the bound is reached,
// which ends the read.
func (c *collected) add(err error) bool {
	be := err.(*Error).trim(maxCollected - c.n)
	c.errs = append(c.errs, be)
	c.n += be.leaves()
	return c.n >= maxCollected
}

// result is the error a collection returns when it ends. At the bound it is no
// longer resumable, so decoding stops.
func (c *collected) result() error {
	if c.n == 0 {
		return nil
	}
	if len(c.errs) == 1 && c.n < maxCollected {
		return c.errs[0]
	}
	f := c.errs[0]
	return &Error{Kind: KindType, Offset: f.Offset, GoType: f.GoType, Msg: f.Msg, Resume: c.n < maxCollected, Multi: c.errs, n: c.n}
}

type logRange struct{ start, end int }

type openAnchor struct {
	name  string
	start int
	depth int
}

type replay struct{ pos, end int }

// Binder decodes documents. It is reused between documents and calls; it is not
// safe for concurrent use.
type Binder struct {
	in  Input
	src []byte
	cfg Config

	buf []byte // scalar scratch

	// Anchor recording; see track.
	log     []parse.Event
	anchors map[string]logRange
	open    []openAnchor
	depth   int
	replays []replay
	own     int // nodes read from the input in this document
	// spend counts the nodes and scalar bytes produced by alias expansion. sp is
	// the cfg.Spend of a nested binder, else &spend.
	spend AliasSpend
	// spendp is the heap copy of spend that nested binders charge; it is made when
	// the first hook runs and dropped with the document. The budget of a call must
	// not live in this binder, which is pooled and reused by later calls.
	spendp *AliasSpend
	// hookCache is the Cache of the NodeRefs handed out in this document.
	hookCache any
	// jsonBuf is scratch for the JSON text handed to a json.Unmarshaler.
	jsonBuf []byte
	// jsonNums makes the generic form keep the text of plain JSON numbers (see
	// jsonNum) while a value is read for a json.Unmarshaler.
	jsonNums bool
	// hooks counts the nodes handed to hooks over the life of the Binder.
	hooks int

	started bool
	inDoc   bool

	// sets are keySets given back by finished struct mappings; skipFrames and
	// idbuf are scratch for skipChecked.
	sets       []*keySet
	kev        parse.Event
	skipFrames []skipFrame
	skipArena  []byte // keys of the open mappings of skipFrames, back to back
	skipEnds   []int  // end of each key in skipArena
	idbuf      []byte

	// keyAlias is the offset of the alias nextKey followed to reach the key it
	// returned, or -1 when the key was written out.
	keyAlias int

	// DialectV3: type errors collected so far in this document, and whether the
	// document root has been handed out yet (a node hook needs to know).
	terrs    []*Error
	rootSeen bool
	docStart parse.Event
	tdepth   int // typed recursion depth

	// Generic-value scratch.
	vals   []any
	pairs  []pair
	frames []anyFrame
	intern map[string]string
}

// New returns a Binder with configuration cfg.
func New(cfg Config) *Binder { return &Binder{cfg: cfg} }

// Reset points b at a new input and clears all per-stream state, keeping
// buffers.
func (b *Binder) Reset(in Input, cfg Config) {
	b.in, b.src, b.cfg = in, in.Source(), cfg
	b.started, b.inDoc = false, false
	b.resetDoc()
}

// Hooks returns how many nodes have been handed to node hooks. A caller that
// reuses the source bytes compares it before and after a decode: a hook may have
// kept a node that points into them.
func (b *Binder) Hooks() int { return b.hooks }

// Release drops the input so a pooled Binder does not keep the source alive.
func (b *Binder) Release() {
	b.in, b.src = nil, nil
	clear(b.vals[:cap(b.vals)])
	b.vals = b.vals[:0]
	clear(b.pairs[:cap(b.pairs)])
	b.pairs = b.pairs[:0]
	for i := range b.frames[:cap(b.frames)] {
		b.frames[:cap(b.frames)][i] = anyFrame{}
	}
	b.frames = b.frames[:0]
	// The JSON text of a value may be large or private; a pooled binder keeps
	// neither a big buffer nor the old bytes.
	if b.jsonBuf != nil {
		b.releaseJSON()
	}
	// The key sets hold keys of the document just read.
	clear(b.sets)
	b.sets = b.sets[:0]
	if cap(b.skipFrames) > 0 {
		clear(b.skipFrames[:cap(b.skipFrames)])
		b.skipFrames = b.skipFrames[:0]
	}
	b.skipArena, b.skipEnds = b.skipArena[:0], b.skipEnds[:0]
	if cap(b.skipArena) > 64<<10 || cap(b.skipEnds) > 4<<10 {
		b.skipArena, b.skipEnds = nil, nil
	}
	if len(b.intern) > 1024 {
		clear(b.intern)
	}
}

func (b *Binder) resetDoc() {
	b.log = b.log[:0]
	clear(b.anchors)
	b.open = b.open[:0]
	b.depth = 0
	b.replays = b.replays[:0]
	b.own = 0
	b.hookCache = nil
	b.spend = AliasSpend{}
	b.spendp = nil
	b.tdepth = 0
	b.terrs, b.rootSeen = b.terrs[:0], false
}

// Collected returns the type errors that DialectV3 collected while decoding the
// current document. The slice is valid until the next document.
func (b *Binder) Collected() []*Error { return b.terrs }

func (b *Binder) limit(name string, max int, option string, off int) error {
	return &Error{Kind: KindLimit, Offset: off, Limit: name, Max: max, Option: option, Msg: name + " limit of " + strconv.Itoa(max) + " exceeded"}
}

// numLimit refuses to convert a scalar to a number when it has more digits than
// the limit allows. Only conversions that cost more than a few operations per
// digit are checked: an integer too wide for 64 bits (it is rebuilt as a
// big.Int) and a float. A scalar read as a string never comes here.
func (b *Binder) numLimit(r *resolve.Result, text []byte, off int) error {
	max := b.cfg.MaxNumberDigits
	if max <= 0 || len(text) <= max {
		return nil
	}
	switch r.Kind {
	case resolve.Int:
		if r.W != resolve.Wide || len(r.Digits) <= max {
			return nil
		}
	case resolve.Float:
	default:
		return nil
	}
	return b.limit("number digits", max, "Limits (MaxNumberDigits)", off)
}

func (b *Binder) syntax(off int, cat, msg string) error {
	return &Error{Kind: KindSyntax, Offset: off, Category: cat, Msg: msg}
}

// NextDocument advances to the next document. It reports false at the end of the
// stream. After it returns true, call Decode to read the document.
func (b *Binder) NextDocument() (bool, error) {
	if b.inDoc {
		if err := b.finishDocument(); err != nil {
			return false, err
		}
	}
	if !b.started {
		ev, err := b.in.Next()
		if err != nil {
			return false, err
		}
		if ev.Kind != parse.StreamStart {
			return false, b.syntax(ev.Start, "syntax", "stream does not start")
		}
		b.started = true
	}
	ev, err := b.in.Next()
	if err != nil {
		if err == io.EOF {
			return false, nil
		}
		return false, err
	}
	switch ev.Kind {
	case parse.StreamEnd:
		return false, nil
	case parse.DocumentStart:
		b.resetDoc()
		b.inDoc = true
		b.docStart = ev
		return true, nil
	}
	return false, b.syntax(ev.Start, "syntax", "unexpected "+ev.Kind.String())
}

// finishDocument consumes events up to the end of the current document. It is
// used when a caller abandons a document early.
func (b *Binder) finishDocument() error {
	for {
		ev, err := b.in.Next()
		if err != nil {
			return err
		}
		if ev.Kind == parse.DocumentEnd {
			b.inDoc = false
			return nil
		}
	}
}

// Decode reads the root node of the current document into v, which must be
// settable (the element of a non-nil pointer).
func (b *Binder) Decode(v reflect.Value) error {
	ev, err := b.next()
	if err != nil {
		return err
	}
	if err := b.value(v, &ev); err != nil {
		b.inDoc = false
		if resumable(err) {
			// The value was read whole, so the rest of the document can still be
			// checked. A syntax or limit error after it is the one to report: the
			// document is not valid, whatever the target is.
			if eerr := b.endDocument(); eerr != nil {
				return eerr
			}
		}
		return err
	}
	return b.endDocument()
}

// DecodeAny reads the root node of the current document as a generic value.
func (b *Binder) DecodeAny() (any, error) {
	ev, err := b.next()
	if err != nil {
		return nil, err
	}
	v, err := b.generic(&ev)
	if err != nil {
		b.inDoc = false
		return nil, err
	}
	return v, b.endDocument()
}

func (b *Binder) endDocument() error {
	ev, err := b.next()
	if err != nil {
		b.inDoc = false
		return err
	}
	if ev.Kind != parse.DocumentEnd {
		b.inDoc = false
		return b.syntax(ev.Start, "syntax", "unexpected "+ev.Kind.String()+" after the document root")
	}
	b.inDoc = false
	return nil
}

// next returns the next event: from the innermost alias replay if one is
// running, else from the input.
func (b *Binder) next() (parse.Event, error) {
	for len(b.replays) > 0 {
		r := &b.replays[len(b.replays)-1]
		if r.pos >= r.end {
			b.replays = b.replays[:len(b.replays)-1]
			continue
		}
		ev := b.log[r.pos]
		r.pos++
		switch ev.Kind {
		case parse.Scalar:
			if err := b.charge(ev.End-ev.Start, ev.Start); err != nil {
				return ev, err
			}
		case parse.SequenceStart, parse.MappingStart:
			if err := b.charge(0, ev.Start); err != nil {
				return ev, err
			}
		}
		return ev, nil
	}
	ev, err := b.in.Next()
	if err != nil {
		return ev, err
	}
	if err := b.track(&ev); err != nil {
		return ev, err
	}
	return ev, nil
}

// charge counts one node and n scalar bytes against the alias budget.
func (b *Binder) charge(n, off int) error {
	sp := b.spender()
	sp.Nodes++
	sp.Bytes += n
	own := b.own
	if b.cfg.Spend != nil {
		own = sp.Own
	}
	if max := b.cfg.MaxAliasNodes; max > 0 {
		if r := b.cfg.AliasRatio; r > 0 && !b.cfg.ExactNodes && r*own > max {
			max = r * own
		}
		if sp.Nodes > max {
			return b.limit("alias expansion nodes", max, "Limits (MaxAliasNodes)", off)
		}
	}
	if max := b.cfg.MaxAliasBytes; max > 0 {
		if r := b.cfg.AliasRatio; r > 0 && !b.cfg.ExactBytes && r*b.cfg.InputBytes > max {
			max = r * b.cfg.InputBytes
		}
		if sp.Bytes > max {
			return b.limit("alias expansion bytes", max, "Limits (MaxAliasBytes)", off)
		}
	}
	return nil
}

// track watches a live event: it counts nodes, applies the scalar-size limit and
// records the events of anchored nodes. Events outside every anchored node are
// not recorded, so a document without anchors pays only the counters.
//
// A recorded Alias event is resolved when it is read: AnchorStart and AnchorEnd
// then hold the recorded range of its target instead of a source span (an
// alias never has an anchor of its own), so a later redefinition of the name
// cannot change what a replay means.
func (b *Binder) track(ev *parse.Event) error {
	switch ev.Kind {
	case parse.Scalar:
		b.own++
		if max := b.cfg.MaxScalarBytes; max > 0 && ev.End-ev.Start > max {
			return b.limit("scalar size", max, "Limits (MaxScalarBytes)", ev.Start)
		}
		if ev.AnchorEnd > ev.AnchorStart {
			start := len(b.log)
			b.log = append(b.log, *ev)
			b.setAnchor(string(b.src[ev.AnchorStart:ev.AnchorEnd]), start, start+1)
		} else if len(b.open) > 0 {
			b.log = append(b.log, *ev)
		}
	case parse.SequenceStart, parse.MappingStart:
		b.own++
		if ev.AnchorEnd > ev.AnchorStart {
			b.open = append(b.open, openAnchor{string(b.src[ev.AnchorStart:ev.AnchorEnd]), len(b.log), b.depth})
		}
		if len(b.open) > 0 {
			b.log = append(b.log, *ev)
		}
		b.depth++
	case parse.SequenceEnd, parse.MappingEnd:
		b.depth--
		if n := len(b.open); n > 0 {
			b.log = append(b.log, *ev)
			if top := b.open[n-1]; top.depth == b.depth {
				b.setAnchor(top.name, top.start, len(b.log))
				b.open = b.open[:n-1]
			}
		}
	case parse.Alias:
		b.own++
		name := b.src[ev.Start+1 : ev.End]
		for i := range b.open {
			if b.open[i].name == string(name) {
				return b.syntax(ev.Start, "syntax", "alias *"+string(name)+" refers to a node that contains it")
			}
		}
		r, ok := b.anchors[string(name)]
		if !ok {
			return b.syntax(ev.Start, "syntax", "undefined alias *"+string(name))
		}
		ev.AnchorStart, ev.AnchorEnd = r.start, r.end
		if len(b.open) > 0 {
			b.log = append(b.log, *ev)
		}
	}
	return nil
}

func (b *Binder) setAnchor(name string, start, end int) {
	if b.anchors == nil {
		b.anchors = map[string]logRange{}
	}
	b.anchors[name] = logRange{start, end}
}

// expand starts replaying the target of the alias event ev. The next event is
// the first event of the target node.
func (b *Binder) expand(ev *parse.Event) {
	b.replays = append(b.replays, replay{ev.AnchorStart, ev.AnchorEnd})
}

// skip consumes the rest of the node that starts with ev. Aliases are not
// expanded.
func (b *Binder) skip(ev *parse.Event) error {
	if ev.Kind != parse.SequenceStart && ev.Kind != parse.MappingStart {
		return nil
	}
	for depth := 1; depth > 0; {
		e, err := b.next()
		if err != nil {
			return err
		}
		switch e.Kind {
		case parse.SequenceStart, parse.MappingStart:
			depth++
		case parse.SequenceEnd, parse.MappingEnd:
			depth--
		}
	}
	return nil
}

func (b *Binder) checkDepth(d, off int) error {
	if max := b.cfg.MaxDepth; max > 0 && d > max {
		return b.limit("depth", max, "Limits (MaxDepth)", off)
	}
	return nil
}

// scalar returns the value bytes of a scalar event. The result may point into
// the source or into b's scratch buffer: it is valid until the next call.
func (b *Binder) scalar(ev *parse.Event) ([]byte, error) {
	s := b.src[ev.Start:ev.End]
	switch ev.Style {
	case scan.Plain:
		if noBreak(s) {
			return s, nil
		}
	case scan.SingleQuoted:
		if len(s) >= 2 && noBreakQuote(s[1:len(s)-1], '\'') {
			return s[1 : len(s)-1], nil
		}
	case scan.DoubleQuoted:
		if len(s) >= 2 && noBreakQuote(s[1:len(s)-1], '\\') {
			return s[1 : len(s)-1], nil
		}
	}
	var err error
	b.buf, err = b.in.AppendScalar(b.buf[:0], ev)
	if err != nil {
		return nil, err
	}
	return b.buf, nil
}

func noBreak(s []byte) bool {
	for _, c := range s {
		if c == '\n' || c == '\r' {
			return false
		}
	}
	return true
}

// noBreakQuote reports that s has no line break and no byte c, so a quoted
// scalar's body is its value as written.
func noBreakQuote(s []byte, c byte) bool {
	for _, x := range s {
		if x == '\n' || x == '\r' || x == c {
			return false
		}
	}
	return true
}

// Tag classes of an explicit tag.
type tagClass uint8

const (
	tagNone tagClass = iota
	tagStr
	tagInt
	tagFloat
	tagBool
	tagNull
	tagBinary
	tagMap
	tagSeq
	tagSet
	tagOmap
	tagTimestamp
	tagNonSpecific // "!"
	tagOther       // not resolved by the schema: kept as the untagged value
)

const yamlPrefix = "tag:yaml.org,2002:"

func (b *Binder) tagOf(ev *parse.Event) (tagClass, error) {
	if !ev.HasTag() {
		return tagNone, nil
	}
	tb, err := b.in.AppendTag(nil, ev)
	if err != nil {
		return tagNone, err
	}
	t := string(tb)
	switch t {
	case "!":
		return tagNonSpecific, nil
	case yamlPrefix + "str":
		return tagStr, nil
	case yamlPrefix + "int":
		return tagInt, nil
	case yamlPrefix + "float":
		return tagFloat, nil
	case yamlPrefix + "bool":
		return tagBool, nil
	case yamlPrefix + "null":
		return tagNull, nil
	case yamlPrefix + "binary":
		return tagBinary, nil
	case yamlPrefix + "map":
		return tagMap, nil
	case yamlPrefix + "seq":
		return tagSeq, nil
	case yamlPrefix + "set":
		return tagSet, nil
	case yamlPrefix + "omap":
		return tagOmap, nil
	case yamlPrefix + "timestamp":
		return tagTimestamp, nil
	}
	return tagOther, nil
}

// classify resolves the text of a scalar for a given tag class. ok is false when
// a tag demands a form the text does not have.
func classify(text []byte, style scan.Style, tag tagClass) (r resolve.Result, ok bool) {
	switch tag {
	case tagNone, tagNonSpecific, tagOther, tagTimestamp:
		if style == scan.Plain && tag != tagNonSpecific {
			return resolve.Core(text), true
		}
		return resolve.Result{Kind: resolve.String}, true
	case tagStr, tagBinary:
		return resolve.Result{Kind: resolve.String}, true
	case tagNull:
		r = resolve.Core(text)
		return r, r.Kind == resolve.Null
	case tagBool:
		r = resolve.Core(text)
		return r, r.Kind == resolve.Bool
	case tagInt:
		r = resolve.Core(text)
		return r, r.Kind == resolve.Int
	case tagFloat:
		r = resolve.Core(text)
		return r, r.Kind == resolve.Float || r.Kind == resolve.Int
	}
	return resolve.Result{Kind: resolve.String}, true
}

// spender returns the counters alias expansion is charged to: the binder's own,
// or those of the call it is nested in.
func (b *Binder) spender() *AliasSpend {
	if b.cfg.Spend != nil {
		return b.cfg.Spend
	}
	if b.spendp != nil {
		return b.spendp
	}
	return &b.spend
}

// nestedConfig returns the configuration for a binder that a hook starts: the
// same rules, charging the same alias budget as b.
func (b *Binder) nestedConfig() Config {
	cfg := b.cfg
	if cfg.Spend == nil {
		if b.spendp == nil {
			sp := b.spend
			sp.Own = b.own
			b.spendp = &sp
		}
		cfg.Spend = b.spendp
	}
	return cfg
}

// releaseJSON drops the JSON scratch buffer if it is big, else wipes it.
func (b *Binder) releaseJSON() {
	if cap(b.jsonBuf) > 64<<10 {
		b.jsonBuf = nil
		return
	}
	clear(b.jsonBuf[:cap(b.jsonBuf)])
	b.jsonBuf = b.jsonBuf[:0]
}
