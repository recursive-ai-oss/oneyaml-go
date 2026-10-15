package bind

import (
	"encoding"
	"encoding/json"
	"reflect"

	"oneyaml.dev/go/internal/typeinfo"
)

var (
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
)

// dplan is the decoder's per-type data, built once and hung off the shared
// typeinfo plan.
type dplan struct {
	tp *typeinfo.Plan
	// textUnm is set when *T implements encoding.TextUnmarshaler.
	textUnm bool
	// nodeUnm is set when *T implements the registered node hook interface.
	nodeUnm bool
	// v3Unm is set, in a DialectV3 plan, when *T reads its own node through the
	// compat hook, and v3Node when T is the compat node type itself.
	v3Unm, v3Node bool
	// funcUnm is set when *T has UnmarshalYAML(func(any) error) error, the form
	// older YAML libraries use; badUnm when it has an UnmarshalYAML method of any
	// other form, which is an error rather than a method silently not called;
	// jsonUnm when *T implements json.Unmarshaler and nothing ranks above it;
	// isNode when T is the document node type, which cannot be a target.
	funcUnm, badUnm, jsonUnm, isNode bool
	// hooked is true when any of the flags above is set, so a plain type costs
	// one test.
	hooked bool

	// Struct lookup. Few fields are matched by a linear scan over name lengths,
	// more by a map; the map lookup does not allocate.
	fields []dfield
	byName map[string]int
	inline *typeinfo.InlineMap
}

type dfield struct {
	typeinfo.Field
	direct bool // reachable as v.Field(Index[0]) with no embedded pointer
}

const linearFields = 8

func planOf(t reflect.Type) *dplan {
	return typeinfo.For(t).DecodeData(buildPlan).(*dplan)
}

func planOfV3(t reflect.Type) *dplan {
	return typeinfo.ForV3(t).DecodeData(buildPlan).(*dplan)
}

// plan returns the decode plan of t under the binder's dialect.
func (b *Binder) plan(t reflect.Type) *dplan {
	if b.cfg.Dialect == DialectV3 {
		return planOfV3(t)
	}
	return planOf(t)
}

func buildPlan(p *typeinfo.Plan) any {
	t := p.Type
	dp := &dplan{tp: p, inline: p.InlineMap}
	if t.Kind() != reflect.Interface && t.Kind() != reflect.Pointer {
		dp.textUnm = reflect.PointerTo(t).Implements(textUnmarshalerType)
	} else if t.Kind() == reflect.Pointer {
		dp.textUnm = t.Implements(textUnmarshalerType)
	}
	if p.V3 {
		if v3Handles != nil && t.Kind() != reflect.Interface && t.Kind() != reflect.Pointer {
			dp.v3Unm = v3Handles(reflect.PointerTo(t))
			dp.v3Node = v3IsNode(t)
		}
	} else if t.Kind() != reflect.Interface && t.Kind() != reflect.Pointer {
		pt := reflect.PointerTo(t)
		if hookIface != nil {
			dp.nodeUnm = pt.Implements(hookIface)
		}
		if funcIface != nil {
			dp.funcUnm = pt.Implements(funcIface)
		}
		if !dp.nodeUnm && !dp.funcUnm {
			if _, ok := pt.MethodByName("UnmarshalYAML"); ok {
				dp.badUnm = true
			}
		}
		dp.jsonUnm = !dp.nodeUnm && !dp.funcUnm && !dp.badUnm && p.Special == typeinfo.SpecialNone &&
			pt.Implements(jsonUnmarshalerType)
		dp.isNode = p.Special == typeinfo.SpecialNode
		dp.hooked = dp.nodeUnm || dp.funcUnm || dp.badUnm || dp.jsonUnm || dp.isNode
	}
	if len(p.Fields) > 0 {
		dp.fields = make([]dfield, len(p.Fields))
		for i, f := range p.Fields {
			dp.fields[i].Field = f
			dp.fields[i].direct = len(f.Index) == 1 && !f.ViaPointer
		}
		if len(dp.fields) > linearFields {
			dp.byName = make(map[string]int, len(dp.fields))
			for i := range dp.fields {
				dp.byName[dp.fields[i].Name] = i
			}
		}
	}
	return dp
}

// lookup finds the field named by key, or -1.
func (dp *dplan) lookup(key []byte) int {
	if dp.byName != nil {
		if i, ok := dp.byName[string(key)]; ok {
			return i
		}
		return -1
	}
	for i := range dp.fields {
		if n := dp.fields[i].Name; len(n) == len(key) && n == string(key) {
			return i
		}
	}
	return -1
}

// fieldValue returns the settable value of struct field f in v, allocating nil
// embedded pointers on the way. ok is false when the path crosses an embedded
// pointer that cannot be set.
func fieldValue(v reflect.Value, f *dfield) (reflect.Value, bool) {
	if f.direct {
		return v.Field(f.Index[0]), true
	}
	for i, x := range f.Index {
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

// bits is a small set of field numbers.
type bits struct {
	lo uint64
	hi []uint64
}

func (s *bits) has(i int) bool {
	if i < 64 {
		return s.lo&(1<<uint(i)) != 0
	}
	w := i/64 - 1
	return w < len(s.hi) && s.hi[w]&(1<<uint(i%64)) != 0
}

func (s *bits) set(i int) {
	if i < 64 {
		s.lo |= 1 << uint(i)
		return
	}
	w := i/64 - 1
	for len(s.hi) <= w {
		s.hi = append(s.hi, 0)
	}
	s.hi[w] |= 1 << uint(i%64)
}

func (s *bits) clear(i int) {
	if i < 64 {
		s.lo &^= 1 << uint(i)
		return
	}
	if w := i/64 - 1; w < len(s.hi) {
		s.hi[w] &^= 1 << uint(i%64)
	}
}
