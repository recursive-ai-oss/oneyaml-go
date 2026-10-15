package emit

import (
	"reflect"
	"slices"
	"strings"

	"oneyaml.dev/go/internal/typeinfo"
)

// structEmit is the encoder's per-struct data, built once per type.
type structEmit struct {
	// keys holds each field's key text including the colon, or nil when the key
	// needs the explicit form and must be written at run time.
	keys [][]byte
	// zero says how to ask a field whether it is zero: 0 by reflection, 1 through
	// its IsZero method, 2 through the method on its address.
	zero  []uint8
	names map[string]struct{}
}

func buildStructEmit(p *typeinfo.Plan) any {
	se := &structEmit{
		keys:  make([][]byte, len(p.Fields)),
		zero:  make([]uint8, len(p.Fields)),
		names: make(map[string]struct{}, len(p.Fields)),
	}
	for i := range p.Fields {
		f := &p.Fields[i]
		se.names[f.Name] = struct{}{}
		var b []byte
		var ok bool
		if p.V3 {
			b, ok = appendKeyV3(nil, f.Name, false)
		} else {
			b, ok = appendKey(nil, f.Name, ctxKey)
		}
		if ok {
			se.keys[i] = append(b, ':')
		}
		if f.Type.Kind() != reflect.Interface {
			h := typeinfo.For(f.Type).Hooks
			switch {
			case h&typeinfo.HookIsZero != 0:
				se.zero[i] = 1
			case h&typeinfo.HookPtrIsZero != 0:
				se.zero[i] = 2
			}
		}
	}
	return se
}

func fieldByIndex(v reflect.Value, idx []int) (reflect.Value, bool) {
	if len(idx) == 1 {
		return v.Field(idx[0]), true
	}
	for i, x := range idx {
		if i > 0 && v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Value{}, false
			}
			v = v.Elem()
		}
		v = v.Field(x)
	}
	return v, true
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer, reflect.Chan, reflect.Func:
		return v.IsNil()
	case reflect.Struct:
		// Every field, unexported ones too: a struct that holds anything is never
		// dropped. A type that wants a different test has an IsZero method.
		return v.IsZero()
	}
	return false
}

// isZero applies the field's IsZero method when it has one, else reflection.
func isZero(v reflect.Value, how uint8) bool {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return true
		}
	}
	switch how {
	case 1:
		return v.Interface().(interface{ IsZero() bool }).IsZero()
	case 2:
		if v.CanAddr() {
			return v.Addr().Interface().(interface{ IsZero() bool }).IsZero()
		}
	}
	return v.IsZero()
}

func omitted(v reflect.Value, f *typeinfo.Field, how uint8) bool {
	if f.V3 {
		return f.OmitEmpty && isZeroV3(v)
	}
	if f.OmitZero && isZero(v, how) {
		return true
	}
	if f.OmitEmpty {
		if how != 0 && isZero(v, how) {
			return true
		}
		return isEmptyValue(v)
	}
	return false
}

func (e *Encoder) structure(v reflect.Value, plan *typeinfo.Plan, p pos) error {
	if plan.Err != nil {
		return &Error{Kind: KindUnsupported, Type: plan.Type, Detail: plan.Err.Error()}
	}
	se := plan.EmitData(buildStructEmit).(*structEmit)
	fbase := len(e.fvs)
	for i := range plan.Fields {
		f := &plan.Fields[i]
		fv, ok := fieldByIndex(v, f.Index)
		if !ok {
			continue
		}
		if (f.OmitEmpty || f.OmitZero) && omitted(fv, f, se.zero[i]) {
			continue
		}
		e.fvs = append(e.fvs, fieldVal{i, fv})
	}
	nf := len(e.fvs) - fbase
	ebase := len(e.ents)
	ni := 0
	if im := plan.InlineMap; im != nil {
		if mv, ok := fieldByIndex(v, im.Index); ok && mv.Kind() == reflect.Map && mv.Len() > 0 {
			it := mv.MapRange()
			for it.Next() {
				ent := mapEnt{v: it.Value()}
				if err := e.classifyKey(&ent, it.Key()); err != nil {
					e.fvs, e.ents = e.fvs[:fbase], e.ents[:ebase]
					return err
				}
				if ent.rank == rankString {
					if _, dup := se.names[ent.s]; dup {
						e.fvs, e.ents = e.fvs[:fbase], e.ents[:ebase]
						return &Error{Kind: KindKey, Type: plan.Type, Detail: "inline map key " + ent.s + " is also a field of " + plan.Type.String()}
					}
				}
				e.ents = append(e.ents, ent)
			}
			ni = len(e.ents) - ebase
			ents := e.ents[ebase:]
			slices.SortFunc(ents, func(a, b mapEnt) int { return e.cmpEnt(&a, &b) })
		}
	}
	if nf+ni == 0 {
		e.fvs, e.ents = e.fvs[:fbase], e.ents[:ebase]
		return e.empty(p, "{}")
	}
	tracked, err := e.enter(0)
	if err != nil {
		e.fvs, e.ents = e.fvs[:fbase], e.ents[:ebase]
		return err
	}
	fields := e.fvs[fbase : fbase+nf]
	ents := e.ents[ebase : ebase+ni]
	col, inline := e.mapOpen(p)
	for n := range fields {
		f := &plan.Fields[fields[n].idx]
		e.entryStart(p, col, inline, n)
		var vp pos
		if k := se.keys[fields[n].idx]; k != nil && p.m != mFlow {
			e.buf = append(e.buf, k...)
			vp = pos{m: mKey, col: col}
		} else {
			vp = e.stringKey(p, col, f.Name)
		}
		var err error
		if f.Flow && p.m != mFlow {
			e.begin(vp)
			err = e.value(fields[n].v, pos{m: mFlow})
			e.buf = append(e.buf, '\n')
		} else {
			err = e.value(fields[n].v, vp)
		}
		if err != nil {
			e.fvs, e.ents = e.fvs[:fbase], e.ents[:ebase]
			e.leave(0, tracked)
			return at(err, f.Name)
		}
	}
	for n := range ents {
		e.entryStart(p, col, inline, nf+n)
		vp, err := e.writeKey(p, col, &ents[n])
		if err == nil {
			err = e.value(ents[n].v, vp)
		}
		if err != nil {
			label := keyLabel(&ents[n])
			e.fvs, e.ents = e.fvs[:fbase], e.ents[:ebase]
			e.leave(0, tracked)
			return at(err, label)
		}
	}
	e.fvs, e.ents = e.fvs[:fbase], e.ents[:ebase]
	e.mapClose(p)
	e.leave(0, tracked)
	return nil
}

var _ = strings.Compare
