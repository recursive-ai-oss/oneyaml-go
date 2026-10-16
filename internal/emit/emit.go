// Package emit writes Go values as YAML text.
//
// It walks values directly, with no intermediate tree, and appends to one
// growing buffer. Layout is block style; see Encoder for the defaults.
package emit

import (
	"encoding/json"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"oneyaml.dev/go/internal/typeinfo"
)

// Kind classifies an Error.
type Kind uint8

// Error kinds.
const (
	// KindUnsupported is a value of a type that has no YAML form.
	KindUnsupported Kind = iota + 1
	// KindDepth is nesting deeper than the configured limit.
	KindDepth
	// KindCycle is a value that contains itself.
	KindCycle
	// KindHook is an error returned by a user's MarshalYAML or MarshalText.
	KindHook
	// KindValue is a value that cannot be written, such as a year out of range.
	KindValue
	// KindKey is a duplicate key produced by an inline map.
	KindKey
	// KindNumberDigits is a number from a MarshalJSON method with more digits
	// than the configured limit.
	KindNumberDigits
)

// Error is a failure while writing a value. Path lists the keys and indexes from
// the innermost value outwards.
type Error struct {
	Kind   Kind
	Type   reflect.Type
	Path   []string
	Detail string
	Err    error
	// Max is the bound that was exceeded, for KindDepth and KindNumberDigits.
	Max int
}

func (e *Error) Error() string { return e.Detail }

// Unwrap returns the error a user hook returned, if any.
func (e *Error) Unwrap() error { return e.Err }

// PathString renders Path from the root as a /-separated pointer.
func (e *Error) PathString() string {
	var b strings.Builder
	for i := len(e.Path) - 1; i >= 0; i-- {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(e.Path[i], "~", "~0"), "/", "~1"))
	}
	return b.String()
}

type marshaler interface{ MarshalYAML() (any, error) }

type textMarshaler interface{ MarshalText() ([]byte, error) }

// Dialect selects the output conventions.
type Dialect uint8

const (
	// DialectCore is the primary API's layout.
	DialectCore Dialect = iota
	// DialectV3 reproduces yaml.v3's output: its indent rules, string quoting,
	// key order, float forms and empty collections.
	DialectV3
)

// Config holds the encoder settings.
type Config struct {
	// Indent is the extra indent for nested block collections; 0 means 2 (4 in
	// DialectV3).
	Indent int
	// MaxDepth is the deepest nesting allowed; 0 means no limit.
	MaxDepth int
	// MaxNumberDigits is the most digits a number taken from a json.Marshaler may
	// have; 0 means no limit.
	MaxNumberDigits int
	// Dialect selects the output conventions.
	Dialect Dialect
	// CompactSeq (DialectV3) writes a sequence under a mapping key two columns
	// closer to the key, so that its dashes line up with the key's text.
	CompactSeq bool
}

type mode uint8

const (
	mRoot mode = iota // start of a line, nothing written yet
	mKey              // just after "key:"
	mItem             // just after "- ", "? " or ": "
	mFlow             // inside [] or {}, or the value of a flow-style field
)

// pos says where the cursor is. col is the column of the key or indicator the
// value belongs to.
type pos struct {
	m   mode
	col int
	// key is set for the key of an explicit "? " entry, whose block scalar is
	// indented like a mapping value, not like a sequence item.
	key bool
}

// cycleCheckDepth is the nesting level from which the encoder tracks the
// containers on the current path. Above it nothing is tracked; real data is
// rarely deeper, and a cycle is caught on the way down at the latest here.
const cycleCheckDepth = 1000

// hardDepth bounds nesting when the caller set no limit.
const hardDepth = 100_000

type fieldVal struct {
	idx int
	v   reflect.Value
}

type kvPair struct {
	k string
	v any
}

type ssPair struct{ k, v string }

type mapEnt struct {
	kind reflect.Kind  // kind of the key, for DialectV3 ordering
	kv   reflect.Value // key with interfaces and pointers removed
	v    reflect.Value
	rank uint8
	i    int64
	u    uint64
	f    float64
	s    string
}

// Key ranks order the keys of one mapping: null, booleans, numbers, strings, then
// anything else by its flow text.
const (
	rankNull uint8 = iota
	rankBool
	rankInt
	rankUint
	rankFloat
	rankString
	rankOther
)

// Encoder holds the buffer and scratch space for writing documents. It is not
// safe for concurrent use.
type Encoder struct {
	buf       []byte
	step      int
	v3        bool
	compact   bool
	maxDepth  int
	maxDigits int
	depth     int
	seen      map[uintptr]struct{}

	fvs  []fieldVal
	kvs  []kvPair
	sss  []ssPair
	ents []mapEnt
	tmp  []byte
}

// New returns an encoder.
func New(cfg Config) *Encoder {
	e := &Encoder{}
	e.Configure(cfg)
	return e
}

// Configure applies cfg.
func (e *Encoder) Configure(cfg Config) {
	e.v3, e.compact = cfg.Dialect == DialectV3, cfg.CompactSeq
	e.step = cfg.Indent
	if e.v3 {
		// libyaml: no indent means 4, and one outside 2..9 means 2.
		switch {
		case e.step == 0:
			e.step = 4
		case e.step < 2 || e.step > 9:
			e.step = 2
		}
	} else {
		if e.step <= 0 {
			e.step = 2
		}
		if e.step > 9 {
			e.step = 9
		}
	}
	e.maxDepth = cfg.MaxDepth
	e.maxDigits = cfg.MaxNumberDigits
}

// Reset empties the output buffer, keeping its capacity.
func (e *Encoder) Reset() { e.buf = e.buf[:0] }

// Bytes returns the text written so far. It is valid until the next Reset or
// Document call.
func (e *Encoder) Bytes() []byte { return e.buf }

// Buffer gives the encoder an output buffer to append to, so a caller can keep
// separators it wrote itself. Document appends after the existing contents.
func (e *Encoder) Buffer(b []byte) { e.buf = b }

// Document appends v as one document: block style, ending with a newline. On an
// error the buffer is cut back to where it was.
func (e *Encoder) Document(v any) error {
	e.depth = 0
	if len(e.seen) > 0 {
		clear(e.seen)
	}
	e.fvs, e.kvs, e.sss, e.ents = e.fvs[:0], e.kvs[:0], e.sss[:0], e.ents[:0]
	start := len(e.buf)
	if err := e.any(v, pos{}); err != nil {
		e.buf = e.buf[:start]
		return err
	}
	return nil
}

func (e *Encoder) indent(col int) {
	for i := 0; i < col; i++ {
		e.buf = append(e.buf, ' ')
	}
}

// begin and end bracket a scalar or an empty collection.
func (e *Encoder) begin(p pos) {
	if p.m == mKey {
		e.buf = append(e.buf, ' ')
	}
}

func (e *Encoder) end(p pos) {
	if p.m != mFlow {
		e.buf = append(e.buf, '\n')
	}
}

func (e *Encoder) raw(p pos, s string) {
	e.begin(p)
	e.buf = append(e.buf, s...)
	e.end(p)
}

func (e *Encoder) null(p pos) error { e.raw(p, "null"); return nil }

func (e *Encoder) boolean(p pos, b bool) {
	if b {
		e.raw(p, "true")
	} else {
		e.raw(p, "false")
	}
}

func (e *Encoder) integer(p pos, i int64) {
	e.begin(p)
	e.buf = strconv.AppendInt(e.buf, i, 10)
	e.end(p)
}

func (e *Encoder) unsigned(p pos, u uint64) {
	e.begin(p)
	e.buf = strconv.AppendUint(e.buf, u, 10)
	e.end(p)
}

func (e *Encoder) float(p pos, f float64, bits int) {
	e.begin(p)
	if e.v3 {
		e.buf = appendFloatV3(e.buf, f, bits)
	} else {
		e.buf = appendFloat(e.buf, f, bits)
	}
	e.end(p)
}

func ctxOf(p pos) context {
	if p.m == mFlow {
		return ctxFlow
	}
	return ctxValue
}

// str writes a string scalar.
func (e *Encoder) str(p pos, s string) {
	if e.v3 {
		e.strV3(p, s)
		return
	}
	st, ind := chooseStyle(s, ctxOf(p))
	if st == stLiteral && p.m == mRoot && ind {
		// An indentation indicator is relative to a parent indent that a root
		// scalar does not have.
		st = stDouble
	}
	e.begin(p)
	switch st {
	case stPlain:
		e.buf = append(e.buf, s...)
	case stSingle:
		e.buf = appendSingle(e.buf, s)
	case stDouble:
		e.buf = appendDouble(e.buf, s)
	case stBinary:
		e.buf = appendBinary(e.buf, []byte(s))
	case stLiteral:
		e.literal(p, s, ind)
		return
	}
	e.end(p)
}

// literal writes s as a literal block scalar, header line included. The caller
// has checked that literalStyle accepts s.
func (e *Encoder) literal(p pos, s string, indicator bool) {
	body := strings.TrimRight(s, "\n")
	trailing := len(s) - len(body)
	e.buf = append(e.buf, '|')
	if indicator {
		e.buf = append(e.buf, byte('0'+e.step))
	}
	switch {
	case trailing == 0:
		e.buf = append(e.buf, '-')
	case trailing > 1:
		e.buf = append(e.buf, '+')
	}
	e.buf = append(e.buf, '\n')
	col := p.col + e.step
	for start := 0; start <= len(body); {
		end := start
		for end < len(body) && body[end] != '\n' {
			end++
		}
		if end > start {
			e.indent(col)
			e.buf = append(e.buf, body[start:end]...)
		}
		e.buf = append(e.buf, '\n')
		start = end + 1
	}
	for i := 1; i < trailing; i++ {
		e.buf = append(e.buf, '\n')
	}
}

// enter checks depth and, deep down, cycles. ptr identifies the container
// (zero for none). The caller calls leave with the returned tracked flag.
func (e *Encoder) enter(ptr uintptr) (tracked bool, err error) {
	e.depth++
	limit := e.maxDepth
	if limit <= 0 {
		// Even with limits off, a MarshalYAML that returns its own receiver would
		// recurse until the stack overflows, and that cannot be recovered from.
		limit = hardDepth
	}
	if e.depth > limit {
		return false, &Error{Kind: KindDepth, Max: limit, Detail: "nesting is deeper than the depth limit (a value may contain itself)"}
	}
	if e.depth > cycleCheckDepth && ptr != 0 {
		if _, dup := e.seen[ptr]; dup {
			return false, &Error{Kind: KindCycle, Detail: "value contains itself"}
		}
		if e.seen == nil {
			e.seen = make(map[uintptr]struct{})
		}
		e.seen[ptr] = struct{}{}
		return true, nil
	}
	return false, nil
}

func (e *Encoder) leave(ptr uintptr, tracked bool) {
	e.depth--
	if tracked {
		delete(e.seen, ptr)
	}
}

func at(err error, seg string) error {
	if ee, ok := err.(*Error); ok {
		ee.Path = append(ee.Path, seg)
	}
	return err
}

func atIndex(err error, i int) error {
	if _, ok := err.(*Error); ok {
		return at(err, strconv.Itoa(i))
	}
	return err
}

// any writes a value held in an interface. The common dynamic types are handled
// without reflection.
func (e *Encoder) any(x any, p pos) error {
	switch x := x.(type) {
	case nil:
		return e.null(p)
	case string:
		e.str(p, x)
	case bool:
		e.boolean(p, x)
	case int:
		e.integer(p, int64(x))
	case int64:
		e.integer(p, x)
	case jsonNum:
		e.raw(p, string(x))
	case float64:
		e.float(p, x, 64)
	case []any:
		return e.anySeq(x, p)
	case map[string]any:
		return e.anyMap(x, p)
	case map[string]string:
		return e.strMap(x, p)
	case []string:
		return e.strSeq(x, p)
	default:
		return e.value(reflect.ValueOf(x), p)
	}
	return nil
}

func (e *Encoder) empty(p pos, text string) error {
	e.raw(p, text)
	return nil
}

func (e *Encoder) seqOpen(p pos) (col int, inline bool) {
	switch p.m {
	case mFlow:
		e.buf = append(e.buf, '[')
		return 0, false
	case mRoot:
		return 0, false
	case mKey:
		e.buf = append(e.buf, '\n')
		if e.v3 && e.compact {
			return e.nested(p.col) - 2, false
		}
		return e.nested(p.col), false
	}
	return e.itemCol(p), true
}

func (e *Encoder) seqItem(p pos, col int, inline bool, i int) pos {
	if p.m == mFlow {
		if i > 0 {
			e.buf = append(e.buf, ',', ' ')
		}
		return pos{m: mFlow}
	}
	if !(i == 0 && inline) {
		e.indent(col)
	}
	e.buf = append(e.buf, '-', ' ')
	return pos{m: mItem, col: col}
}

func (e *Encoder) seqClose(p pos) {
	if p.m == mFlow {
		e.buf = append(e.buf, ']')
	}
}

func (e *Encoder) mapOpen(p pos) (col int, inline bool) {
	switch p.m {
	case mFlow:
		e.buf = append(e.buf, '{')
		return 0, false
	case mRoot:
		return 0, false
	case mKey:
		e.buf = append(e.buf, '\n')
		return e.nested(p.col), false
	}
	return e.itemCol(p), true
}

// itemCol returns the column of a block collection that starts right after an
// indicator ("- ", "? " or ": "). libyaml aligns the value of an explicit key to
// the nesting step, padding after the colon.
func (e *Encoder) itemCol(p pos) int {
	if e.v3 && p.key {
		col := e.nested(p.col)
		for n := col - (p.col + 2); n > 0; n-- {
			e.buf = append(e.buf, ' ')
		}
		return col
	}
	return p.col + 2
}

func (e *Encoder) mapClose(p pos) {
	if p.m == mFlow {
		e.buf = append(e.buf, '}')
	}
}

// entryStart writes the separator or indentation before entry i.
func (e *Encoder) entryStart(p pos, col int, inline bool, i int) {
	if p.m == mFlow {
		if i > 0 {
			e.buf = append(e.buf, ',', ' ')
		}
		return
	}
	if !(i == 0 && inline) {
		e.indent(col)
	}
}

// stringKey writes the key s and returns the position for its value.
func (e *Encoder) stringKey(p pos, col int, s string) pos {
	c := ctxKey
	if p.m == mFlow {
		c = ctxFlow
	}
	if out, ok := e.appendKey(e.buf, s, c); ok {
		e.buf = out
		return e.afterKey(p, col)
	}
	return e.complexKey(p, col, func() {
		kp := pos{m: mItem, col: col, key: true}
		if p.m == mFlow {
			kp = pos{m: mFlow}
		}
		e.str(kp, s)
	})
}

// appendKey appends s as an implicit key in the encoder's dialect.
func (e *Encoder) appendKey(dst []byte, s string, c context) ([]byte, bool) {
	if e.v3 {
		return appendKeyV3(dst, s, c == ctxFlow)
	}
	return appendKey(dst, s, c)
}

func (e *Encoder) afterKey(p pos, col int) pos {
	e.buf = append(e.buf, ':')
	if p.m == mFlow {
		e.buf = append(e.buf, ' ')
		return pos{m: mFlow}
	}
	return pos{m: mKey, col: col}
}

// complexKey writes "? key" and ": " and returns the position of the value.
func (e *Encoder) complexKey(p pos, col int, key func()) pos {
	e.buf = append(e.buf, '?', ' ')
	key()
	if p.m == mFlow {
		if e.v3 {
			// libyaml puts a space before the colon that follows a complex key.
			e.buf = append(e.buf, ' ')
		}
		e.buf = append(e.buf, ':', ' ')
		return pos{m: mFlow}
	}
	e.indent(col)
	e.buf = append(e.buf, ':', ' ')
	return pos{m: mItem, col: col, key: true}
}

func (e *Encoder) anySeq(s []any, p pos) error {
	if len(s) == 0 {
		return e.empty(p, "[]")
	}
	var ptr uintptr
	if e.depth >= cycleCheckDepth {
		ptr = reflect.ValueOf(s).Pointer()
	}
	tracked, err := e.enter(ptr)
	if err != nil {
		return err
	}
	col, inline := e.seqOpen(p)
	for i, x := range s {
		ip := e.seqItem(p, col, inline, i)
		if err := e.any(x, ip); err != nil {
			e.leave(ptr, tracked)
			return atIndex(err, i)
		}
	}
	e.seqClose(p)
	e.leave(ptr, tracked)
	return nil
}

func (e *Encoder) strSeq(s []string, p pos) error {
	if len(s) == 0 {
		return e.empty(p, "[]")
	}
	col, inline := e.seqOpen(p)
	for i, x := range s {
		ip := e.seqItem(p, col, inline, i)
		e.str(ip, x)
	}
	e.seqClose(p)
	return nil
}

func (e *Encoder) anyMap(m map[string]any, p pos) error {
	if len(m) == 0 {
		return e.empty(p, "{}")
	}
	var ptr uintptr
	if e.depth >= cycleCheckDepth {
		ptr = reflect.ValueOf(m).Pointer()
	}
	tracked, err := e.enter(ptr)
	if err != nil {
		return err
	}
	base := len(e.kvs)
	for k, v := range m {
		e.kvs = append(e.kvs, kvPair{k, v})
	}
	pairs := e.kvs[base:]
	if e.v3 {
		slices.SortFunc(pairs, func(a, b kvPair) int { return cmpNatural(a.k, b.k) })
	} else {
		slices.SortFunc(pairs, func(a, b kvPair) int { return strings.Compare(a.k, b.k) })
	}
	col, inline := e.mapOpen(p)
	for i := range pairs {
		e.entryStart(p, col, inline, i)
		vp := e.stringKey(p, col, pairs[i].k)
		if err := e.any(pairs[i].v, vp); err != nil {
			k := pairs[i].k
			e.kvs = e.kvs[:base]
			e.leave(ptr, tracked)
			return at(err, k)
		}
	}
	e.kvs = e.kvs[:base]
	e.mapClose(p)
	e.leave(ptr, tracked)
	return nil
}

func (e *Encoder) strMap(m map[string]string, p pos) error {
	if len(m) == 0 {
		return e.empty(p, "{}")
	}
	base := len(e.sss)
	for k, v := range m {
		e.sss = append(e.sss, ssPair{k, v})
	}
	pairs := e.sss[base:]
	if e.v3 {
		slices.SortFunc(pairs, func(a, b ssPair) int { return cmpNatural(a.k, b.k) })
	} else {
		slices.SortFunc(pairs, func(a, b ssPair) int { return strings.Compare(a.k, b.k) })
	}
	col, inline := e.mapOpen(p)
	for i := range pairs {
		e.entryStart(p, col, inline, i)
		vp := e.stringKey(p, col, pairs[i].k)
		e.str(vp, pairs[i].v)
	}
	e.sss = e.sss[:base]
	e.mapClose(p)
	return nil
}

var timeType = reflect.TypeFor[time.Time]()

// value writes a reflected value.
func (e *Encoder) value(v reflect.Value, p pos) error {
	for steps := 0; ; steps++ {
		if !v.IsValid() {
			return e.null(p)
		}
		if steps > 1000 {
			return &Error{Kind: KindCycle, Detail: "value contains itself"}
		}
		t := v.Type()
		k := v.Kind()
		if e.v3 && v3NodeMatch != nil && v3NodeMatch(t) {
			if k == reflect.Pointer && v.IsNil() {
				return e.null(p)
			}
			return e.node(v, p)
		}
		var plan *typeinfo.Plan
		if t.PkgPath() != "" || k == reflect.Pointer || k == reflect.Struct || (k == reflect.Slice && t.Elem().Kind() == reflect.Uint8) {
			plan = e.plan(t)
			if done, err := e.hooks(v, plan, p); done {
				return err
			}
		}
		switch k {
		case reflect.Interface:
			if v.IsNil() {
				return e.null(p)
			}
			tracked, err := e.enter(0)
			if err != nil {
				return err
			}
			err = e.any(v.Interface(), p)
			e.leave(0, tracked)
			return err
		case reflect.Pointer:
			if v.IsNil() {
				return e.null(p)
			}
			if e.depth >= cycleCheckDepth {
				ptr := v.Pointer()
				tracked, err := e.enter(ptr)
				if err != nil {
					return err
				}
				err = e.value(v.Elem(), p)
				e.leave(ptr, tracked)
				return err
			}
			v = v.Elem()
			continue
		case reflect.Bool:
			e.boolean(p, v.Bool())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			e.integer(p, v.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			e.unsigned(p, v.Uint())
		case reflect.Float32:
			e.float(p, v.Float(), 32)
		case reflect.Float64:
			e.float(p, v.Float(), 64)
		case reflect.String:
			e.str(p, v.String())
		case reflect.Slice:
			if v.IsNil() {
				if e.v3 {
					return e.empty(p, "[]")
				}
				return e.null(p)
			}
			return e.seq(v, p)
		case reflect.Array:
			return e.seq(v, p)
		case reflect.Map:
			if v.IsNil() {
				if e.v3 {
					return e.empty(p, "{}")
				}
				return e.null(p)
			}
			if t.Name() == "" {
				// A map is pointer-shaped, so boxing it costs nothing, and the typed
				// paths avoid the two allocations per entry that reflect makes.
				switch m := v.Interface().(type) {
				case map[string]string:
					return e.strMap(m, p)
				case map[string]any:
					return e.anyMap(m, p)
				}
			}
			return e.mapping(v, p)
		case reflect.Struct:
			return e.structure(v, plan, p)
		default:
			return &Error{Kind: KindUnsupported, Type: t, Detail: "unsupported type " + t.String()}
		}
		return nil
	}
}

// plan returns the plan of t under the encoder's dialect.
func (e *Encoder) plan(t reflect.Type) *typeinfo.Plan {
	if e.v3 {
		return typeinfo.ForV3(t)
	}
	return typeinfo.For(t)
}

// hooks applies MarshalYAML, the special types and MarshalText, in that order.
// done is true when the value has been written (or failed).
func (e *Encoder) hooks(v reflect.Value, plan *typeinfo.Plan, p pos) (done bool, err error) {
	h := plan.Hooks
	if plan.ForeignNode {
		return true, &Error{Kind: KindHook, Type: v.Type(), Detail: v.Type().String() + " is the Node of another yaml.v3 module, which this package cannot write; build this package's yaml.Node instead, or write the value with that module"}
	}
	if plan.BadMarshal {
		return true, &Error{Kind: KindHook, Type: v.Type(), Detail: v.Type().String() + " has a MarshalYAML method that is not MarshalYAML() (any, error); change its signature, or remove it to write the value by its fields"}
	}
	if h&(typeinfo.HookMarshaler|typeinfo.HookPtrMarshaler) != 0 {
		var m marshaler
		if h&typeinfo.HookMarshaler != 0 {
			if v.Kind() == reflect.Pointer && v.IsNil() {
				return true, e.null(p)
			}
			m = v.Interface().(marshaler)
		} else if v.CanAddr() {
			m = v.Addr().Interface().(marshaler)
		}
		if m != nil {
			r, merr := m.MarshalYAML()
			if merr != nil {
				return true, &Error{Kind: KindHook, Type: v.Type(), Detail: "MarshalYAML of " + v.Type().String() + ": " + merr.Error(), Err: merr}
			}
			tracked, derr := e.enter(0)
			if derr != nil {
				return true, derr
			}
			err = e.any(r, p)
			e.leave(0, tracked)
			return true, err
		}
	}
	switch plan.Special {
	case typeinfo.SpecialNode:
		if nodeValue == nil || (v.Kind() == reflect.Pointer && v.IsNil()) {
			return true, e.null(p)
		}
		x, nerr := nodeValue(v)
		if nerr != nil {
			return true, &Error{Kind: KindHook, Type: v.Type(), Detail: nerr.Error(), Err: nerr}
		}
		tracked, derr := e.enter(0)
		if derr != nil {
			return true, derr
		}
		err = e.any(x, p)
		e.leave(0, tracked)
		return true, err
	case typeinfo.SpecialTime:
		if t, ok := v.Interface().(time.Time); ok {
			return true, e.timestamp(p, t)
		}
	case typeinfo.SpecialDuration:
		e.str(p, time.Duration(v.Int()).String())
		return true, nil
	case typeinfo.SpecialBigInt:
		var n *big.Int
		switch {
		case v.Kind() == reflect.Pointer:
			if v.IsNil() {
				return true, e.null(p)
			}
			n = v.Interface().(*big.Int)
		case v.CanAddr():
			n = v.Addr().Interface().(*big.Int)
		default:
			c := v.Interface().(big.Int)
			n = &c
		}
		e.begin(p)
		e.buf = n.Append(e.buf, 10)
		e.end(p)
		return true, nil
	case typeinfo.SpecialBytes:
		if v.IsNil() {
			return true, e.null(p)
		}
		e.begin(p)
		e.buf = appendBinary(e.buf, v.Bytes())
		e.end(p)
		return true, nil
	}
	if h&(typeinfo.HookText|typeinfo.HookPtrText) != 0 {
		var tm textMarshaler
		if h&typeinfo.HookText != 0 {
			if v.Kind() == reflect.Pointer && v.IsNil() {
				return true, e.null(p)
			}
			tm = v.Interface().(textMarshaler)
		} else if v.CanAddr() {
			tm = v.Addr().Interface().(textMarshaler)
		}
		if tm != nil {
			b, terr := tm.MarshalText()
			if terr != nil {
				return true, &Error{Kind: KindHook, Type: v.Type(), Detail: "MarshalText of " + v.Type().String() + ": " + terr.Error(), Err: terr}
			}
			e.str(p, string(b))
			return true, nil
		}
	}
	if h&(typeinfo.HookJSON|typeinfo.HookPtrJSON) != 0 && !e.v3 {
		var jm json.Marshaler
		if h&typeinfo.HookJSON != 0 {
			if v.Kind() == reflect.Pointer && v.IsNil() {
				return true, e.null(p)
			}
			jm = v.Interface().(json.Marshaler)
		} else if v.CanAddr() {
			jm = v.Addr().Interface().(json.Marshaler)
		}
		if jm != nil {
			return true, e.jsonValue(v, jm, p)
		}
	}
	return false, nil
}

func (e *Encoder) timestamp(p pos, t time.Time) error {
	if y := t.Year(); !e.v3 && (y < 0 || y > 9999) {
		return &Error{Kind: KindValue, Type: timeType, Detail: "time.Time year is outside 0..9999"}
	}
	e.begin(p)
	e.buf = t.AppendFormat(e.buf, time.RFC3339Nano)
	e.end(p)
	return nil
}

func (e *Encoder) seq(v reflect.Value, p pos) error {
	n := v.Len()
	if n == 0 {
		return e.empty(p, "[]")
	}
	var ptr uintptr
	if e.depth >= cycleCheckDepth && v.Kind() == reflect.Slice {
		ptr = v.Pointer()
	}
	tracked, err := e.enter(ptr)
	if err != nil {
		return err
	}
	col, inline := e.seqOpen(p)
	for i := 0; i < n; i++ {
		ip := e.seqItem(p, col, inline, i)
		if err := e.value(v.Index(i), ip); err != nil {
			e.leave(ptr, tracked)
			return atIndex(err, i)
		}
	}
	e.seqClose(p)
	e.leave(ptr, tracked)
	return nil
}

// classifyKey resolves a map key to a rank and sort fields in ent.
func (e *Encoder) classifyKey(ent *mapEnt, k reflect.Value) error {
	ent.kind = reflect.Interface
	for k.IsValid() && (k.Kind() == reflect.Interface || k.Kind() == reflect.Pointer) {
		if k.IsNil() {
			ent.rank = rankNull
			return nil
		}
		if k.Kind() == reflect.Pointer {
			plan := e.plan(k.Type())
			if plan.Hooks&(typeinfo.HookMarshaler|typeinfo.HookText) != 0 || plan.Special != typeinfo.SpecialNone {
				return e.otherKey(ent, k)
			}
		}
		k = k.Elem()
	}
	if !k.IsValid() {
		ent.rank = rankNull
		return nil
	}
	t := k.Type()
	kind := k.Kind()
	ent.kind = kind
	if kind == reflect.String && t.PkgPath() == "" {
		ent.kv, ent.rank, ent.s = k, rankString, k.String()
		return nil
	}
	if t.PkgPath() != "" || kind == reflect.Struct {
		plan := e.plan(t)
		if plan.Hooks&(typeinfo.HookMarshaler|typeinfo.HookPtrMarshaler) != 0 || plan.Special != typeinfo.SpecialNone {
			return e.otherKey(ent, k)
		}
		if kind != reflect.String && plan.Hooks&typeinfo.HookText != 0 {
			b, err := k.Interface().(textMarshaler).MarshalText()
			if err != nil {
				return &Error{Kind: KindHook, Type: t, Detail: "MarshalText of " + t.String() + ": " + err.Error(), Err: err}
			}
			ent.kv, ent.rank, ent.s = k, rankString, string(b)
			return nil
		}
	}
	ent.kv = k
	switch kind {
	case reflect.String:
		ent.rank, ent.s = rankString, k.String()
	case reflect.Bool:
		ent.rank = rankBool
		if k.Bool() {
			ent.i = 1
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		ent.rank, ent.i = rankInt, k.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		ent.rank, ent.u = rankUint, k.Uint()
	case reflect.Float32, reflect.Float64:
		ent.rank, ent.f = rankFloat, k.Float()
	default:
		return e.otherKey(ent, k)
	}
	return nil
}

// otherKey handles keys that are not plain scalars: they are written in the
// explicit "? " form and sorted by their flow-style text.
func (e *Encoder) otherKey(ent *mapEnt, k reflect.Value) error {
	ent.kv, ent.rank = k, rankOther
	saved := e.buf
	e.buf = e.tmp[:0]
	err := e.value(k, pos{m: mFlow})
	ent.s = string(e.buf)
	e.tmp = e.buf[:0]
	e.buf = saved
	return err
}

func cmpNatural(a, b string) int {
	switch {
	case naturalLess(a, b):
		return -1
	case naturalLess(b, a):
		return 1
	}
	return 0
}

func (e *Encoder) cmpEnt(a, b *mapEnt) int {
	if e.v3 {
		switch {
		case lessV3(a, b):
			return -1
		case lessV3(b, a):
			return 1
		}
		return 0
	}
	return cmpEnt(a, b)
}

func cmpEnt(a, b *mapEnt) int {
	if a.rank != b.rank {
		return int(a.rank) - int(b.rank)
	}
	switch a.rank {
	case rankBool, rankInt:
		switch {
		case a.i < b.i:
			return -1
		case a.i > b.i:
			return 1
		}
	case rankUint:
		switch {
		case a.u < b.u:
			return -1
		case a.u > b.u:
			return 1
		}
	case rankFloat:
		switch {
		case a.f < b.f || (a.f != a.f && b.f == b.f):
			return -1
		case a.f > b.f || (b.f != b.f && a.f == a.f):
			return 1
		}
	case rankString, rankOther:
		return strings.Compare(a.s, b.s)
	}
	return 0
}

// writeKey writes the key of ent and returns the value position.
func (e *Encoder) writeKey(p pos, col int, ent *mapEnt) (pos, error) {
	switch ent.rank {
	case rankNull:
		e.buf = append(e.buf, "null"...)
	case rankBool:
		if ent.i != 0 {
			e.buf = append(e.buf, "true"...)
		} else {
			e.buf = append(e.buf, "false"...)
		}
	case rankInt:
		e.buf = strconv.AppendInt(e.buf, ent.i, 10)
	case rankUint:
		e.buf = strconv.AppendUint(e.buf, ent.u, 10)
	case rankFloat:
		if e.v3 {
			e.buf = appendFloatV3(e.buf, ent.f, 64)
		} else {
			e.buf = appendFloat(e.buf, ent.f, 64)
		}
	case rankString:
		return e.stringKey(p, col, ent.s), nil
	default:
		var kerr error
		vp := e.complexKey(p, col, func() {
			kp := pos{m: mItem, col: col, key: true}
			if p.m == mFlow {
				kp = pos{m: mFlow}
			}
			kerr = e.value(ent.kv, kp)
		})
		return vp, kerr
	}
	return e.afterKey(p, col), nil
}

func (e *Encoder) mapping(v reflect.Value, p pos) error {
	n := v.Len()
	if n == 0 {
		return e.empty(p, "{}")
	}
	var ptr uintptr
	if e.depth >= cycleCheckDepth {
		ptr = v.Pointer()
	}
	tracked, err := e.enter(ptr)
	if err != nil {
		return err
	}
	base := len(e.ents)
	it := v.MapRange()
	for it.Next() {
		// Classify into a local first: rendering a composite key can append to
		// e.ents and move it.
		ent := mapEnt{v: it.Value()}
		if err := e.classifyKey(&ent, it.Key()); err != nil {
			e.ents = e.ents[:base]
			e.leave(ptr, tracked)
			return err
		}
		e.ents = append(e.ents, ent)
	}
	ents := e.ents[base : base+n]
	slices.SortFunc(ents, func(a, b mapEnt) int { return e.cmpEnt(&a, &b) })
	col, inline := e.mapOpen(p)
	for i := range ents {
		e.entryStart(p, col, inline, i)
		vp, err := e.writeKey(p, col, &ents[i])
		if err == nil {
			err = e.value(ents[i].v, vp)
		}
		if err != nil {
			label := keyLabel(&ents[i])
			e.ents = e.ents[:base]
			e.leave(ptr, tracked)
			return at(err, label)
		}
	}
	e.ents = e.ents[:base]
	e.mapClose(p)
	e.leave(ptr, tracked)
	return nil
}

func keyLabel(ent *mapEnt) string {
	switch ent.rank {
	case rankString, rankOther:
		return ent.s
	case rankBool:
		return strconv.FormatBool(ent.i != 0)
	case rankInt:
		return strconv.FormatInt(ent.i, 10)
	case rankUint:
		return strconv.FormatUint(ent.u, 10)
	case rankFloat:
		return strconv.FormatFloat(ent.f, 'g', -1, 64)
	}
	return "null"
}
