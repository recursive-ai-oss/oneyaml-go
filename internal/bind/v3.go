package bind

import (
	"encoding"
	"encoding/base64"
	"math"
	"reflect"
	"time"
	"unicode/utf8"

	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/resolve"
	"oneyaml.dev/go/internal/scan"
)

// This file holds the parts of the binder that differ in DialectV3. The shared
// machinery (events, aliases, limits, maps, structs, sequences) is in the other
// files; only scalar resolution and the leniencies of typed targets live here.

// classifyV3 resolves the text of a scalar for a tag class under yaml.v3's
// rules: only a plain scalar is resolved, and an explicit tag demands the form
// it names.
func classifyV3(text []byte, style scan.Style, tag tagClass) (r resolve.Result, ok bool) {
	switch tag {
	case tagNone, tagNonSpecific:
		if style == scan.Plain {
			return resolve.V3(text), true
		}
	case tagNull:
		r = resolve.V3(text)
		return r, r.Kind == resolve.Null
	case tagBool:
		r = resolve.V3(text)
		return r, r.Kind == resolve.Bool
	case tagInt:
		r = resolve.V3(text)
		return r, r.Kind == resolve.Int
	case tagFloat:
		r = resolve.V3(text)
		return r, r.Kind == resolve.Float || r.Kind == resolve.Int
	case tagTimestamp:
		r = resolve.V3(text)
		return r, r.Kind == resolve.Timestamp
	}
	return resolve.Result{Kind: resolve.String}, true
}

// scalarV3 reads a scalar event: its tag class, the raw text, the resolved form
// and the text to bind (the decoded bytes for !!binary, else the raw text).
type scalarV3 struct {
	tag      tagClass
	raw, txt []byte
	r        resolve.Result
}

// readV3 resolves ev. The error is fatal: yaml.v3 stops on a tag that does not
// fit its text and on bad base64.
func (b *Binder) readV3(ev *parse.Event, s *scalarV3) error {
	var err error
	if ev.HasTag() {
		if s.tag, err = b.tagOf(ev); err != nil {
			return err
		}
	}
	if s.raw, err = b.scalar(ev); err != nil {
		return err
	}
	r, ok := classifyV3(s.raw, ev.Style, s.tag)
	if !ok {
		return &Error{Kind: KindType, Offset: ev.Start, GoType: "interface {}", Category: "decode",
			Msg: "cannot decode " + v3KindName(&r) + " `" + abbrevV3(s.raw) + "` as a " + tagNames[s.tag]}
	}
	s.r, s.txt = r, s.raw
	if s.tag == tagBinary {
		dec, derr := base64.StdEncoding.DecodeString(string(s.raw))
		if derr != nil {
			return &Error{Kind: KindType, Offset: ev.Start, GoType: "interface {}", Category: "decode", Msg: "!!binary value contains invalid base64 data"}
		}
		s.txt = dec
	}
	return nil
}

func v3KindName(r *resolve.Result) string {
	switch r.Kind {
	case resolve.Null:
		return "!!null"
	case resolve.Bool:
		return "!!bool"
	case resolve.Int:
		return "!!int"
	case resolve.Float:
		return "!!float"
	case resolve.Timestamp:
		return "!!timestamp"
	}
	return "!!str"
}

// abbrevV3 shortens a value for an error message the way yaml.v3 does: more than
// ten bytes are cut to seven and "..." (never in the middle of a character).
func abbrevV3(b []byte) string {
	if len(b) <= 10 {
		return string(b)
	}
	n := 7
	for n > 0 && !utf8.RuneStart(b[n]) {
		n--
	}
	return string(b[:n]) + "..."
}

// v3Mismatch is the error for a scalar that does not fit its target. It is soft:
// the scalar is consumed, so decoding can go on.
func (b *Binder) v3Mismatch(ev *parse.Event, t reflect.Type, s *scalarV3) error {
	name := v3KindName(&s.r)
	if s.tag != tagNone && s.tag != tagOther && s.tag != tagNonSpecific {
		name = tagNames[s.tag]
	}
	return &Error{Kind: KindType, Offset: ev.Start, GoType: t.String(),
		Msg: "cannot unmarshal " + name + " `" + abbrevV3(s.raw) + "` into " + t.String()}
}

// anyV3 is the generic value of a resolved scalar.
func anyV3(s *scalarV3) any {
	switch s.r.Kind {
	case resolve.Null:
		return nil
	case resolve.Bool:
		return s.r.B
	case resolve.Int:
		if s.tag == tagFloat {
			return intToFloat(&s.r)
		}
		if s.r.W == resolve.FitsU64 {
			return s.r.U
		}
		if int64(int(s.r.I)) == s.r.I {
			return int(s.r.I)
		}
		return s.r.I
	case resolve.Float:
		return s.r.F
	case resolve.Timestamp:
		t, _ := resolve.ParseTimestampV3(string(s.raw))
		return t
	}
	return string(s.txt)
}

// scalarAnyV3 decodes a scalar event into its generic value.
func (b *Binder) scalarAnyV3(ev *parse.Event) (any, error) {
	var s scalarV3
	if err := b.readV3(ev, &s); err != nil {
		return nil, err
	}
	if s.r.Kind == resolve.String {
		return string(s.txt), nil
	}
	if err := b.numLimit(&s.r, s.raw, ev.Start); err != nil {
		return nil, err
	}
	return anyV3(&s), nil
}

var timeType = reflect.TypeFor[time.Time]()

// scalarIntoV3 is scalarInto for the yaml.v3 dialect.
func (b *Binder) scalarIntoV3(v reflect.Value, ev *parse.Event) error {
	var s scalarV3
	if err := b.readV3(ev, &s); err != nil {
		return err
	}
	null := s.r.Kind == resolve.Null
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
			return b.v3Mismatch(ev, t, &s)
		}
		if err := b.numLimit(&s.r, s.raw, ev.Start); err != nil {
			return err
		}
		v.Set(reflect.ValueOf(anyV3(&s)))
		return nil
	}
	if null {
		if k == reflect.Map || k == reflect.Slice {
			v.SetZero()
		}
		return nil
	}
	if t == timeType && s.r.Kind == resolve.Timestamp {
		tm, _ := resolve.ParseTimestampV3(string(s.raw))
		v.Set(reflect.ValueOf(tm))
		return nil
	}
	if t.PkgPath() != "" || k == reflect.Struct || k == reflect.Slice || k == reflect.Array || k == reflect.Map {
		if b.plan(t).textUnm {
			if err := v.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText(s.txt); err != nil {
				return &Error{Kind: KindType, Offset: ev.Start, GoType: t.String(), Err: err, Msg: err.Error()}
			}
			return nil
		}
	}
	isDuration := t == durationType
	switch k {
	case reflect.String:
		v.SetString(string(s.txt))
		return nil
	case reflect.Bool:
		switch s.r.Kind {
		case resolve.Bool:
			v.SetBool(s.r.B)
			return nil
		case resolve.String:
			if x, ok := resolve.V3Bool11(s.txt); ok {
				v.SetBool(x)
				return nil
			}
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if isDuration {
			if s.r.Kind == resolve.String {
				if d, err := time.ParseDuration(string(s.txt)); err == nil {
					v.SetInt(int64(d))
					return nil
				}
			}
			break
		}
		switch s.r.Kind {
		case resolve.Int:
			if s.r.W == resolve.Fits64 && !v.OverflowInt(s.r.I) {
				v.SetInt(s.r.I)
				return nil
			}
		case resolve.Float:
			// A float with no fraction is the same number; "3.5" is not an int.
			if f := s.r.F; f == math.Trunc(f) && f >= -(1<<63) && f < 1<<63 && !v.OverflowInt(int64(f)) {
				v.SetInt(int64(f))
				return nil
			}
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		switch s.r.Kind {
		case resolve.Int:
			if s.r.W == resolve.FitsU64 && !v.OverflowUint(s.r.U) {
				v.SetUint(s.r.U)
				return nil
			}
			if s.r.W == resolve.Fits64 && s.r.I >= 0 && !v.OverflowUint(uint64(s.r.I)) {
				v.SetUint(uint64(s.r.I))
				return nil
			}
		case resolve.Float:
			if f := s.r.F; f == math.Trunc(f) && f >= 0 && f < 1<<64 && !v.OverflowUint(uint64(f)) {
				v.SetUint(uint64(f))
				return nil
			}
		}
	case reflect.Float32, reflect.Float64:
		var f float64
		switch s.r.Kind {
		case resolve.Int:
			if err := b.numLimit(&s.r, s.raw, ev.Start); err != nil {
				return err
			}
			f = intToFloat(&s.r)
		case resolve.Float:
			if err := b.numLimit(&s.r, s.raw, ev.Start); err != nil {
				return err
			}
			f = s.r.F
		default:
			return b.v3Mismatch(ev, t, &s)
		}
		if k == reflect.Float32 && !math.IsInf(f, 0) && !math.IsNaN(f) && v.OverflowFloat(f) {
			return b.v3Mismatch(ev, t, &s)
		}
		v.SetFloat(f)
		return nil
	}
	return b.v3Mismatch(ev, t, &s)
}

var durationType = reflect.TypeFor[time.Duration]()

// soft records err as a collected type error and reports true when err is a
// type error that yaml.v3 would have collected: a mismatch between a value and
// its target, not a failure of a user's hook or a broken document.
func (b *Binder) soft(err error) bool {
	if be, ok := err.(*Error); ok && be.Kind == KindType && be.Err == nil && be.Category == "" {
		b.terrs = append(b.terrs, be)
		return true
	}
	return false
}

// collMismatch handles a collection that does not fit its target: under the
// yaml.v3 dialect the collection is skipped and the error collected.
func (b *Binder) collMismatch(ev *parse.Event, t reflect.Type, name string) error {
	err := b.typeErr(ev, t, "cannot unmarshal "+name+" into "+t.String())
	if b.cfg.Dialect != DialectV3 {
		return err
	}
	if serr := b.skip(ev); serr != nil {
		return serr
	}
	b.terrs = append(b.terrs, err.(*Error))
	return nil
}
