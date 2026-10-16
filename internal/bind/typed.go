package bind

import (
	"encoding"
	"encoding/base64"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"time"

	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/resolve"
	"oneyaml.dev/go/internal/scan"
	"oneyaml.dev/go/internal/typeinfo"
)

func (b *Binder) typeErr(ev *parse.Event, t reflect.Type, msg string) error {
	return &Error{Kind: KindType, Offset: ev.Start, GoType: t, Msg: msg}
}

// nodeName names the YAML type of a node for messages.
func nodeName(ev *parse.Event, r *resolve.Result, tag tagClass) string {
	switch ev.Kind {
	case parse.SequenceStart:
		return "!!seq"
	case parse.MappingStart:
		return "!!map"
	}
	if tag != tagNone && tag != tagOther && tag != tagNonSpecific {
		return tagNames[tag]
	}
	switch r.Kind {
	case resolve.Null:
		return "!!null"
	case resolve.Bool:
		return "!!bool"
	case resolve.Int:
		return "!!int"
	case resolve.Float:
		return "!!float"
	}
	return "!!str"
}

func (b *Binder) mismatch(ev *parse.Event, t reflect.Type, name string, text []byte) error {
	msg := "cannot unmarshal " + name
	if ev.Kind == parse.Scalar {
		msg += " `" + abbrev(text) + "`"
	}
	return b.typeErr(ev, t, msg+" into "+t.String())
}

func deref(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	return v
}

// value decodes the node that starts with ev into v, which must be settable.
func (b *Binder) value(v reflect.Value, ev *parse.Event) error {
	// A nil pointer is allocated on the way to its target; if the value then
	// does not fit, the pointer goes back to nil, so a mismatch changes nothing.
	nilPtr := v.Kind() == reflect.Pointer && v.IsNil()
	err := b.valueInto(v, ev)
	if be, ok := err.(*Error); ok && be.Direct {
		if nilPtr && be.Resume {
			v.SetZero()
		}
		// The mismatch is about this value only; the levels above must not take it
		// for their own.
		be.Direct = false
	}
	return err
}

func (b *Binder) valueInto(v reflect.Value, ev *parse.Event) error {
	for ev.Kind == parse.Alias {
		b.expand(ev)
		e, err := b.next()
		if err != nil {
			return err
		}
		ev = &e
	}
	if ok, err := b.hook(v, ev); ok || err != nil {
		return err
	}
	switch ev.Kind {
	case parse.Scalar:
		err := b.scalarInto(v, ev)
		if err != nil {
			if b.cfg.Dialect == DialectV3 {
				if b.soft(err) {
					return nil
				}
			} else if be, ok := err.(*Error); ok && be.Kind == KindType && be.Err == nil && be.Category == "" {
				// The scalar is consumed and the target is untouched.
				be.Resume, be.Direct = true, true
			}
		}
		return err
	case parse.SequenceStart:
		return b.seqInto(v, ev)
	case parse.MappingStart:
		return b.mapInto(v, ev)
	}
	return b.syntax(ev.Start, "syntax", "unexpected "+ev.Kind.String())
}

var (
	byteType    = reflect.TypeFor[byte]()
	timeLayouts = buildTimeLayouts()
)

func buildTimeLayouts() []string {
	var out []string
	for _, sep := range []string{"T", "t", " "} {
		for _, zone := range []string{"Z07:00", "Z07", " Z07:00", " Z07", ""} {
			out = append(out, "2006-1-2"+sep+"15:4:5"+zone)
		}
	}
	return append(out, "2006-1-2")
}

func parseTimestamp(text string) (time.Time, bool) {
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, text); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (b *Binder) scalarInto(v reflect.Value, ev *parse.Event) error {
	if b.cfg.Dialect == DialectV3 {
		return b.scalarIntoV3(v, ev)
	}
	var tag tagClass
	if ev.HasTag() {
		var err error
		if tag, err = b.tagOf(ev); err != nil {
			return err
		}
	}
	text, err := b.scalar(ev)
	if err != nil {
		return err
	}
	r, ok := classify(text, ev.Style, tag)
	if !ok {
		return b.tagMismatch(ev, text, tag)
	}
	null := r.Kind == resolve.Null
	for v.Kind() == reflect.Pointer {
		if null {
			v.SetZero()
			return nil
		}
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	t, k := v.Type(), v.Kind()
	if k == reflect.Interface {
		if null {
			v.SetZero()
			return nil
		}
		if v.NumMethod() != 0 {
			return b.mismatch(ev, t, nodeName(ev, &r, tag), text)
		}
		var x any
		if r.Kind == resolve.String {
			x = string(text)
		} else {
			if err := b.numLimit(&r, text, ev.Start); err != nil {
				return err
			}
			x = anyOf(text, &r, tag)
		}
		v.Set(reflect.ValueOf(x))
		return nil
	}
	if null {
		if k == reflect.Map || k == reflect.Slice {
			v.SetZero()
		}
		return nil
	}
	var special typeinfo.Special
	if t.PkgPath() != "" || k == reflect.Struct || k == reflect.Slice || k == reflect.Array || k == reflect.Map {
		dp := b.plan(t)
		special = dp.tp.Special
		if special == typeinfo.SpecialNone && dp.textUnm {
			if err := v.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText(text); err != nil {
				return &Error{Kind: KindType, Offset: ev.Start, GoType: t, Err: err,
					Msg: "cannot unmarshal " + nodeName(ev, &r, tag) + " `" + abbrev(text) + "` into " + t.String() + ": " + err.Error()}
			}
			return nil
		}
	}
	switch special {
	case typeinfo.SpecialTime:
		tm, ok := parseTimestamp(string(text))
		if !ok {
			return b.mismatch(ev, t, "timestamp", text)
		}
		v.Set(reflect.ValueOf(tm))
		return nil
	case typeinfo.SpecialBigInt:
		if r.Kind != resolve.Int {
			return b.mismatch(ev, t, nodeName(ev, &r, tag), text)
		}
		if err := b.numLimit(&r, text, ev.Start); err != nil {
			return err
		}
		v.Addr().Interface().(*big.Int).Set(r.Big())
		return nil
	case typeinfo.SpecialDuration:
		if r.Kind == resolve.Int && r.W == resolve.Fits64 {
			v.SetInt(r.I)
			return nil
		}
		d, err := time.ParseDuration(string(text))
		if err != nil {
			return b.mismatch(ev, t, nodeName(ev, &r, tag), text)
		}
		v.SetInt(int64(d))
		return nil
	case typeinfo.SpecialBytes:
		if t.Elem() == byteType {
			if tag == tagBinary {
				bs, err := decodeBase64(text)
				if err != nil {
					return b.mismatch(ev, t, "!!binary", text)
				}
				v.SetBytes(bs)
				return nil
			}
			v.SetBytes(append([]byte(nil), text...))
			return nil
		}
	}
	switch k {
	case reflect.String:
		v.SetString(string(text))
	case reflect.Bool:
		if r.Kind != resolve.Bool {
			return b.mismatch(ev, t, nodeName(ev, &r, tag), text)
		}
		v.SetBool(r.B)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if r.Kind != resolve.Int {
			return b.mismatch(ev, t, nodeName(ev, &r, tag), text)
		}
		if r.W != resolve.Fits64 || v.OverflowInt(r.I) {
			return b.typeErr(ev, t, "value `"+abbrev(text)+"` overflows "+t.String())
		}
		v.SetInt(r.I)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if r.Kind != resolve.Int {
			return b.mismatch(ev, t, nodeName(ev, &r, tag), text)
		}
		var u uint64
		switch {
		case r.W == resolve.FitsU64:
			u = r.U
		case r.W == resolve.Fits64 && r.I >= 0:
			u = uint64(r.I)
		default:
			return b.typeErr(ev, t, "value `"+abbrev(text)+"` overflows "+t.String())
		}
		if v.OverflowUint(u) {
			return b.typeErr(ev, t, "value `"+abbrev(text)+"` overflows "+t.String())
		}
		v.SetUint(u)
	case reflect.Float32, reflect.Float64:
		var f float64
		switch r.Kind {
		case resolve.Float:
			if err := b.numLimit(&r, text, ev.Start); err != nil {
				return err
			}
			f = r.F
		case resolve.Int:
			if err := b.numLimit(&r, text, ev.Start); err != nil {
				return err
			}
			f = intToFloat(&r)
		default:
			return b.mismatch(ev, t, nodeName(ev, &r, tag), text)
		}
		if k == reflect.Float32 && !math.IsInf(f, 0) && !math.IsNaN(f) && v.OverflowFloat(f) {
			return b.typeErr(ev, t, "value `"+abbrev(text)+"` overflows "+t.String())
		}
		v.SetFloat(f)
	default:
		return b.mismatch(ev, t, nodeName(ev, &r, tag), text)
	}
	return nil
}

func decodeBase64(text []byte) ([]byte, error) {
	clean := make([]byte, 0, len(text))
	for _, c := range text {
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			clean = append(clean, c)
		}
	}
	out, err := base64.StdEncoding.DecodeString(string(clean))
	if err != nil {
		out, err = base64.RawStdEncoding.DecodeString(string(clean))
	}
	return out, err
}

// enter counts one level of typed recursion. Unlike generic values, which are
// built on an explicit stack, typed values recurse on the Go stack, so a ceiling
// applies even when no depth limit is configured.
func (b *Binder) enter(ev *parse.Event) error {
	b.tdepth++
	if b.tdepth > hardDepth {
		e := b.limit("MaxDepth", "depth", hardDepth, ev.Start).(*Error)
		e.Fixed = true
		return e
	}
	return b.checkDepth(len(b.frames)+b.tdepth, ev.Start)
}

func (b *Binder) seqInto(v reflect.Value, ev *parse.Event) error {
	v = deref(v)
	t := v.Type()
	switch v.Kind() {
	case reflect.Interface:
		return b.genericInto(v, ev)
	case reflect.Slice:
		if err := b.enter(ev); err != nil {
			return err
		}
		n := 0
		var pend collected
		v.SetLen(0)
		for {
			e, err := b.next()
			if err != nil {
				return err
			}
			if e.Kind == parse.SequenceEnd {
				break
			}
			if n >= v.Cap() {
				v.Grow(1)
			}
			v.SetLen(n + 1)
			el := v.Index(n)
			el.SetZero()
			if err := b.value(el, &e); err != nil {
				if err = withIdx(err, n); !resumable(err) {
					return err
				}
				if pend.add(err) {
					return pend.result()
				}
			}
			n++
		}
		b.tdepth--
		if v.IsNil() {
			v.Set(reflect.MakeSlice(t, 0, 0))
		}
		return pend.result()
	case reflect.Array:
		if err := b.enter(ev); err != nil {
			return err
		}
		i := 0
		var pend collected
		for {
			e, err := b.next()
			if err != nil {
				return err
			}
			if e.Kind == parse.SequenceEnd {
				break
			}
			if i < v.Len() {
				el := v.Index(i)
				el.SetZero()
				if err := b.value(el, &e); err != nil {
					if err = withIdx(err, i); !resumable(err) {
						return err
					}
					if pend.add(err) {
						return pend.result()
					}
				}
			} else if err := b.skip(&e); err != nil {
				return err
			}
			i++
		}
		for j := i; j < v.Len(); j++ {
			v.Index(j).SetZero()
		}
		if i != v.Len() && b.cfg.Dialect == DialectV3 {
			return &Error{Kind: KindType, Offset: ev.Start, GoType: t, Category: "decode",
				Msg: "invalid array: want " + strconv.Itoa(v.Len()) + " elements but got " + strconv.Itoa(i)}
		}
		b.tdepth--
		return pend.result()
	}
	return b.collMismatch(ev, t, "!!seq")
}

// genericInto decodes the collection at ev as a generic value into the empty
// interface v.
func (b *Binder) genericInto(v reflect.Value, ev *parse.Event) error {
	if v.NumMethod() != 0 {
		kind := "!!map"
		if ev.Kind == parse.SequenceStart {
			kind = "!!seq"
		}
		return b.collMismatch(ev, v.Type(), kind)
	}
	x, err := b.generic(ev)
	if err != nil {
		return err
	}
	v.Set(reflect.ValueOf(x))
	return nil
}

func (b *Binder) mapInto(v reflect.Value, ev *parse.Event) error {
	v = deref(v)
	t := v.Type()
	switch v.Kind() {
	case reflect.Interface:
		return b.genericInto(v, ev)
	case reflect.Struct:
		dp := b.plan(t)
		if dp.tp.Err != nil {
			return &Error{Kind: KindType, Offset: ev.Start, GoType: t, Category: "decode", Msg: dp.tp.Err.Error()}
		}
		if dp.tp.Special == typeinfo.SpecialNone || (b.cfg.Dialect == DialectV3 && dp.tp.Special == typeinfo.SpecialTime) {
			// yaml.v3 treats time.Time as a struct without fields here: a mapping
			// reads without error and sets nothing.
			if err := b.enter(ev); err != nil {
				return err
			}
			var st structState
			err := b.structPairs(v, dp, &st, false)
			b.putSet(st.unk)
			if err != nil && !resumable(err) {
				return err
			}
			b.tdepth--
			return err
		}
	case reflect.Map:
		if err := b.enter(ev); err != nil {
			return err
		}
		err := b.mapTyped(v)
		if err != nil && !resumable(err) {
			return err
		}
		b.tdepth--
		return err
	}
	return b.collMismatch(ev, t, "!!map")
}

// nextKey reads a key event, following aliases.
func (b *Binder) nextKey() (parse.Event, error) {
	ke, err := b.next()
	b.keyAlias = -1
	for err == nil && ke.Kind == parse.Alias {
		if b.keyAlias < 0 {
			b.keyAlias = ke.Start
		}
		b.expand(&ke)
		ke, err = b.next()
	}
	return ke, err
}

// keyPos is the offset an error about the key ke just read by nextKey points
// at: the alias that was written there, or the key itself. Read it before the
// key's value is decoded, which reads keys of its own.
func (b *Binder) keyPos(ke *parse.Event) int {
	if b.keyAlias >= 0 {
		return b.keyAlias
	}
	return ke.Start
}

func (b *Binder) isMergeKey(ke *parse.Event) bool {
	return b.cfg.MergeKeys && ke.Style == scan.Plain && !ke.HasTag() && ke.End-ke.Start == 2 &&
		b.src[ke.Start] == '<' && b.src[ke.Start+1] == '<'
}

// keyText returns the text of a key node as a string key: the scalar itself, or
// for a !!binary key under the yaml.v3 dialect the bytes it decodes to.
func (b *Binder) keyText(ke *parse.Event) ([]byte, error) {
	if b.cfg.Dialect == DialectV3 && ke.HasTag() {
		var s scalarV3
		if err := b.readV3(ke, &s); err != nil {
			return nil, err
		}
		return s.txt, nil
	}
	return b.scalar(ke)
}

func (b *Binder) keyLabel(ke *parse.Event) string {
	t, err := b.scalar(ke)
	if err != nil {
		return "?"
	}
	return string(t)
}

type structState struct {
	seen, weak bits
	merged     bool
	// failed holds the keys of the inline map whose value did not fit.
	failed map[string]struct{}
	// unk holds the keys that no field has, to catch a repeat. It is taken from
	// the binder when the first such key comes, and given back by mapInto.
	unk *keySet
}

// structPairs reads the pairs of a mapping, whose start event is consumed, into
// the struct v until the mapping ends. With weak set the pairs come from a merge
// source: they fill only fields no explicit key sets.
func (b *Binder) structPairs(v reflect.Value, dp *dplan, st *structState, weak bool) error {
	var pend collected
	for {
		ke, err := b.nextKey()
		if err != nil {
			return err
		}
		if ke.Kind == parse.MappingEnd {
			return pend.result()
		}
		if ke.Kind != parse.Scalar {
			return b.typeErr(&ke, v.Type(), "mapping keys must be scalars to decode into "+v.Type().String())
		}
		kpos := b.keyPos(&ke)
		merge := b.isMergeKey(&ke)
		var kb []byte
		if !merge {
			if kb, err = b.keyText(&ke); err != nil {
				return err
			}
		}
		ve, err := b.next()
		if err != nil {
			return err
		}
		if merge {
			if !weak {
				if st.merged {
					return b.dupKey(kpos)
				}
				st.merged = true
			}
			if err := b.mergeStruct(v, dp, st, &ve); err != nil {
				if err = withSeg(err, "<<"); !resumable(err) {
					return err
				}
				if pend.add(err) {
					return pend.result()
				}
			}
			continue
		}
		idx := dp.lookup(kb)
		if idx < 0 {
			if err := b.structExtra(v, dp, st, &ke, kpos, kb, &ve, weak); err != nil {
				if !resumable(err) {
					return err
				}
				if pend.add(err) {
					return pend.result()
				}
			}
			continue
		}
		f := &dp.fields[idx]
		clearFirst := false
		if weak {
			if st.seen.has(idx) || st.weak.has(idx) {
				if err := b.skip(&ve); err != nil {
					return err
				}
				continue
			}
			st.weak.set(idx)
		} else {
			if st.seen.has(idx) {
				return withSeg(b.dupKey(kpos), f.Name)
			}
			st.seen.set(idx)
			if st.weak.has(idx) {
				st.weak.clear(idx)
				clearFirst = true
			}
		}
		fv, ok := fieldValue(v, f)
		if !ok {
			if err := b.skip(&ve); err != nil {
				return err
			}
			continue
		}
		if clearFirst {
			fv.SetZero()
		}
		if err := b.value(fv, &ve); err != nil {
			if err = withSeg(err, f.Name); !resumable(err) {
				return err
			}
			if pend.add(err) {
				return pend.result()
			}
		}
	}
}

// mergeStruct applies the value of a "<<" key: a mapping, or a sequence of
// mappings, usually aliases.
func (b *Binder) mergeStruct(v reflect.Value, dp *dplan, st *structState, ve *parse.Event) error {
	one := func(e *parse.Event) error {
		for e.Kind == parse.Alias {
			b.expand(e)
			n, err := b.next()
			if err != nil {
				return err
			}
			*e = n
		}
		if e.Kind != parse.MappingStart {
			return b.typeErr(e, v.Type(), "a merge key needs a mapping or a sequence of mappings")
		}
		if err := b.enter(e); err != nil {
			return err
		}
		err := b.structPairs(v, dp, st, true)
		if err != nil && !resumable(err) {
			return err
		}
		b.tdepth--
		return err
	}
	e := *ve
	if e.Kind == parse.Alias {
		b.expand(&e)
		var err error
		if e, err = b.next(); err != nil {
			return err
		}
	}
	if e.Kind != parse.SequenceStart {
		return one(&e)
	}
	var pend collected
	for {
		el, err := b.next()
		if err != nil {
			return err
		}
		if el.Kind == parse.SequenceEnd {
			return pend.result()
		}
		if err := one(&el); err != nil {
			if !resumable(err) {
				return err
			}
			if pend.add(err) {
				return pend.result()
			}
		}
	}
}

// structExtra handles a key that names no field: it goes to the inline map, is
// an error under KnownFields, or is skipped.
func (b *Binder) structExtra(v reflect.Value, dp *dplan, st *structState, ke *parse.Event, kpos int, kb []byte, ve *parse.Event, weak bool) error {
	if im := dp.inline; im != nil {
		mv, ok := fieldByIndexAlloc(v, im.Index)
		if ok {
			if mv.IsNil() {
				mv.Set(reflect.MakeMap(mv.Type()))
			}
			mt := mv.Type()
			key := reflect.ValueOf(b.internKey(kb))
			if key.Type() != mt.Key() {
				key = key.Convert(mt.Key())
			}
			if mv.MapIndex(key).IsValid() {
				if weak {
					return b.skip(ve)
				}
				return withSeg(b.dupKey(kpos), key.String())
			}
			if _, ok := st.failed[key.String()]; ok && !weak {
				return withSeg(b.dupKey(kpos), key.String())
			}
			el := reflect.New(mt.Elem()).Elem()
			if err := b.value(el, ve); err != nil {
				if resumable(err) && !weak {
					if st.failed == nil {
						st.failed = map[string]struct{}{}
					}
					st.failed[key.String()] = struct{}{}
				}
				return withSeg(err, key.String())
			}
			mv.SetMapIndex(key, el)
			return nil
		}
	}
	// The key names no field, so nothing else will look at it: a repeat of an
	// earlier such key is refused here, as a repeat is anywhere else.
	if !weak {
		var err error
		if b.idbuf, err = b.identity(b.idbuf[:0], ke, kb); err != nil {
			return err
		}
		if st.unk == nil {
			st.unk = b.getSet()
		}
		if st.unk.add(b.idbuf) {
			return withSeg(b.dupKey(kpos), string(kb))
		}
	}
	if b.cfg.KnownFields {
		if b.cfg.Dialect == DialectV3 {
			b.terrs = append(b.terrs, b.typeErr(ke, v.Type(), "field "+string(kb)+" not found in type "+v.Type().String()).(*Error))
			return b.skip(ve)
		}
		e := b.typeErr(ke, v.Type(), "field "+strconv.Quote(string(kb))+" not found in type "+v.Type().String()).(*Error)
		e.Unknown = true
		if err := b.skipIgnored(ve, weak); err != nil {
			return err
		}
		e.Resume = true
		return withSeg(e, string(kb))
	}
	return b.skipIgnored(ve, weak)
}

// skipIgnored consumes the value of a key the target ignores. The primary rules
// also look inside it for a repeated key; the yaml.v3 rules do not decode it and
// so, like yaml.v3, do not look.
func (b *Binder) skipIgnored(ve *parse.Event, weak bool) error {
	if weak || b.cfg.Dialect != DialectCore {
		return b.skip(ve)
	}
	return b.skipChecked(ve)
}

func fieldByIndexAlloc(v reflect.Value, idx []int) (reflect.Value, bool) {
	for i, x := range idx {
		if i > 0 && v.Kind() == reflect.Pointer {
			if v.IsNil() {
				if !v.CanSet() {
					return reflect.Value{}, false
				}
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(x)
	}
	return v, true
}

type mapState struct {
	kv, ev     reflect.Value
	fresh      bool
	seen, weak map[any]struct{}
	// failed holds the keys whose value did not fit. They are not in the map, so
	// a later repeat of one would otherwise pass as a new key.
	failed      map[any]struct{}
	track       bool
	nanSeen     bool
	merged      bool
	keyIsString bool
	// bad holds the identities of the keys that did not fit the key type, to
	// catch a repeat of one.
	bad *keySet
}

// nanKey stands for every NaN in a set of keys, since a NaN is not equal to
// itself and would never be found again.
type nanKey struct{}

// isNaNKey reports whether the key value kv, a float or an interface holding
// one, is NaN.
func isNaNKey(kv reflect.Value) bool {
	if kv.Kind() == reflect.Interface {
		kv = kv.Elem()
	}
	if k := kv.Kind(); k != reflect.Float64 && k != reflect.Float32 {
		return false
	}
	f := kv.Float()
	return f != f
}

// canonKey is the key kv as a value that compares equal to itself.
func canonKey(kv reflect.Value) any {
	if isNaNKey(kv) {
		return nanKey{}
	}
	return kv.Interface()
}

func (b *Binder) mapTyped(v reflect.Value) error {
	t := v.Type()
	if v.IsNil() {
		v.Set(reflect.MakeMap(t))
	}
	st := mapState{
		kv: reflect.New(t.Key()).Elem(), ev: reflect.New(t.Elem()).Elem(),
		fresh: v.Len() == 0,
	}
	st.keyIsString = t.Key().Kind() == reflect.String && t.Key().PkgPath() == ""
	st.track = !st.fresh || b.cfg.MergeKeys
	err := b.mapPairs(v, &st, false)
	b.putSet(st.bad)
	return err
}

func (b *Binder) mapPairs(v reflect.Value, st *mapState, weak bool) error {
	var pend collected
	for {
		ke, err := b.nextKey()
		if err != nil {
			return err
		}
		if ke.Kind == parse.MappingEnd {
			return pend.result()
		}
		if ke.Kind != parse.Scalar {
			return b.typeErr(&ke, v.Type().Key(), "mapping keys must be scalars to decode into "+v.Type().String())
		}
		kpos := b.keyPos(&ke)
		merge := b.isMergeKey(&ke)
		var ki any
		if !merge {
			st.kv.SetZero()
			if st.keyIsString {
				kb, err := b.keyText(&ke)
				if err != nil {
					return err
				}
				st.kv.SetString(b.internKey(kb))
			} else if err := b.scalarInto(st.kv, &ke); err != nil {
				if b.cfg.Dialect == DialectV3 && b.soft(err) {
					// The key is lost, so its value is skipped.
					ve, err := b.next()
					if err == nil {
						err = b.skip(&ve)
					}
					if err != nil {
						return err
					}
					continue
				}
				if be, ok := err.(*Error); ok && b.cfg.Dialect != DialectV3 && be.Kind == KindType && be.Err == nil && be.Category == "" {
					// The key does not fit the key type, like a value that does not fit:
					// the entry is skipped and the mismatch collected.
					// A repeat of such a key is a duplicate, as a repeat of a key that fits
					// is; the value is looked into for repeats like any ignored one.
					ve, serr := b.next()
					if serr == nil && !weak {
						kb, kerr := b.keyText(&ke)
						if kerr == nil {
							b.idbuf, kerr = b.identity(b.idbuf[:0], &ke, kb)
						}
						if serr = kerr; serr == nil {
							if st.bad == nil {
								st.bad = b.getSet()
							}
							if st.bad.add(b.idbuf) {
								return withSeg(b.dupKey(kpos), b.keyLabel(&ke))
							}
						}
					}
					if serr == nil {
						serr = b.skipIgnored(&ve, weak)
					}
					if serr != nil {
						return serr
					}
					be.Resume = true
					if pend.add(withSeg(be, b.keyLabel(&ke))) {
						return pend.result()
					}
					continue
				}
				return err
			}
			if st.track || st.failed != nil {
				ki = canonKey(st.kv)
			}
		}
		ve, err := b.next()
		if err != nil {
			return err
		}
		if merge {
			if !weak {
				if st.merged {
					return b.dupKey(kpos)
				}
				st.merged = true
			}
			if err := b.mergeMap(v, st, &ve); err != nil {
				if err = withSeg(err, "<<"); !resumable(err) {
					return err
				}
				if pend.add(err) {
					return pend.result()
				}
			}
			continue
		}
		if st.track {
			if weak {
				if _, ok := st.seen[ki]; ok {
					if err := b.skip(&ve); err != nil {
						return err
					}
					continue
				}
				if _, ok := st.weak[ki]; ok {
					if err := b.skip(&ve); err != nil {
						return err
					}
					continue
				}
				if st.weak == nil {
					st.weak = map[any]struct{}{}
				}
				st.weak[ki] = struct{}{}
			} else {
				if _, ok := st.seen[ki]; ok {
					return withSeg(b.dupKey(kpos), b.keyLabel(&ke))
				}
				if st.seen == nil {
					st.seen = map[any]struct{}{}
				}
				st.seen[ki] = struct{}{}
				delete(st.weak, ki)
			}
		}
		if st.failed != nil && !weak && !merge {
			if _, ok := st.failed[ki]; ok {
				return withSeg(b.dupKey(kpos), b.keyLabel(&ke))
			}
		}
		// A map never finds a NaN key again, so two .nan keys would both be kept.
		// They are one YAML value; the second is a duplicate. Under tracking the
		// canonical key above already catches it.
		if !st.track && !st.keyIsString && !merge && b.cfg.Dialect == DialectCore && isNaNKey(st.kv) {
			if st.nanSeen {
				return withSeg(b.dupKey(kpos), b.keyLabel(&ke))
			}
			st.nanSeen = true
		}
		st.ev.SetZero()
		if err := b.value(st.ev, &ve); err != nil {
			if err = withSeg(err, b.keyLabel(&ke)); !resumable(err) {
				return err
			}
			// The entry is not added, but its key is remembered. An earlier entry
			// of the same key was added: then this is a repeat, not a mismatch.
			if !st.track && !weak && v.MapIndex(st.kv).IsValid() {
				return withSeg(b.dupKey(kpos), b.keyLabel(&ke))
			}
			if !weak {
				if st.failed == nil {
					st.failed = map[any]struct{}{}
				}
				st.failed[canonKey(st.kv)] = struct{}{}
			}
			if pend.add(err) {
				return pend.result()
			}
			continue
		}
		if st.track {
			v.SetMapIndex(st.kv, st.ev)
			continue
		}
		n := v.Len()
		v.SetMapIndex(st.kv, st.ev)
		if v.Len() == n {
			return withSeg(b.dupKey(kpos), b.keyLabel(&ke))
		}
	}
}

func (b *Binder) mergeMap(v reflect.Value, st *mapState, ve *parse.Event) error {
	one := func(e *parse.Event) error {
		for e.Kind == parse.Alias {
			b.expand(e)
			n, err := b.next()
			if err != nil {
				return err
			}
			*e = n
		}
		if e.Kind != parse.MappingStart {
			return b.typeErr(e, v.Type(), "a merge key needs a mapping or a sequence of mappings")
		}
		if err := b.enter(e); err != nil {
			return err
		}
		err := b.mapPairs(v, st, true)
		if err != nil && !resumable(err) {
			return err
		}
		b.tdepth--
		return err
	}
	e := *ve
	if e.Kind == parse.Alias {
		b.expand(&e)
		var err error
		if e, err = b.next(); err != nil {
			return err
		}
	}
	if e.Kind != parse.SequenceStart {
		return one(&e)
	}
	var pend collected
	for {
		el, err := b.next()
		if err != nil {
			return err
		}
		if el.Kind == parse.SequenceEnd {
			return pend.result()
		}
		if err := one(&el); err != nil {
			if !resumable(err) {
				return err
			}
			if pend.add(err) {
				return pend.result()
			}
		}
	}
}
