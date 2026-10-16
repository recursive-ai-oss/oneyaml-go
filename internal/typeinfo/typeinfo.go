// Package typeinfo builds and caches, per Go type, what the encoder and the
// decoder need to know about it: struct fields with their YAML names and
// options, embedded and inline fields, and which hooks the type implements.
//
// A Plan is immutable once built and shared between goroutines. Encoder-only
// and decoder-only data hangs off the plan through EmitData and DecodeData, so
// each side builds its own lookup structures once without this package knowing
// their shape.
package typeinfo

import (
	"encoding"
	"encoding/json"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// Hooks records which marshalling interfaces a type implements.
type Hooks uint8

// Hook bits. The Ptr variants are set when only the pointer type implements the
// interface, so the hook may be used when the value is addressable.
const (
	HookMarshaler Hooks = 1 << iota
	HookPtrMarshaler
	HookText
	HookPtrText
	HookIsZero
	HookPtrIsZero
	// HookJSON is set when the type implements json.Marshaler. It ranks below
	// the YAML and text hooks.
	HookJSON
	HookPtrJSON
)

// Special marks types the encoder writes in a form of its own.
type Special uint8

// Special types.
const (
	SpecialNone     Special = iota
	SpecialTime             // time.Time: RFC 3339 timestamp
	SpecialDuration         // time.Duration: its String form
	SpecialBigInt           // *big.Int and big.Int: a plain integer
	SpecialBytes            // []byte: !!binary
	SpecialNode             // the document node type: written as its value, refused as a decode target
)

// Field is one struct field as it appears in YAML.
type Field struct {
	Name  string       // key text
	Index []int        // path from the struct, through embedded structs
	Type  reflect.Type // the field's declared type

	OmitEmpty bool
	OmitZero  bool
	Flow      bool
	// Tagged is true when the name came from a tag.
	Tagged bool
	// ViaPointer is true when the path passes through an embedded pointer, which
	// may be nil.
	ViaPointer bool
	// V3 marks a field of a ForV3 plan.
	V3 bool
}

// InlineMap is a map field whose entries are merged into the parent mapping.
type InlineMap struct {
	Index      []int
	Type       reflect.Type
	ViaPointer bool
}

// Plan describes one Go type.
type Plan struct {
	Type    reflect.Type
	Kind    reflect.Kind
	Hooks   Hooks
	Special Special

	// Fields lists a struct's fields in declaration order (embedded fields at the
	// position of their embedding field). Nil for other kinds.
	Fields []Field
	// InlineMap is the struct's inline map field, if any.
	InlineMap *InlineMap
	// Err is set when the struct's tags are invalid under the rules of a compat
	// dialect; the primary rules never set it.
	Err error
	// BadMarshal is set when the type has a method named MarshalYAML whose
	// signature is not the accepted one. Encoding such a type is an error: the
	// method would otherwise be ignored without a word.
	BadMarshal bool
	// ForeignNode is set by ForV3 for the Node type of the yaml.v3 module (or a
	// pointer to it). That type cannot be read here without importing it, so
	// writing it by its fields would be wrong; encoding it is an error.
	ForeignNode bool
	// V3 marks a plan built by ForV3.
	V3 bool

	emitOnce   sync.Once
	emitData   any
	decodeOnce sync.Once
	decodeData any
}

// EmitData returns data the encoder derived from the plan, building it on first
// use.
func (p *Plan) EmitData(build func(*Plan) any) any {
	p.emitOnce.Do(func() { p.emitData = build(p) })
	return p.emitData
}

// DecodeData is the decoder's counterpart of EmitData.
func (p *Plan) DecodeData(build func(*Plan) any) any {
	p.decodeOnce.Do(func() { p.decodeData = build(p) })
	return p.decodeData
}

var nodeType reflect.Type

// SetNodeType registers the document node type of the root package so that its
// plan is marked SpecialNode. It must run from an init function.
func SetNodeType(t reflect.Type) { nodeType = t }

var cache sync.Map // reflect.Type -> *Plan

// For returns the plan for t, building and caching it on first use.
func For(t reflect.Type) *Plan {
	if p, ok := cache.Load(t); ok {
		return p.(*Plan)
	}
	p := build(t)
	actual, _ := cache.LoadOrStore(t, p)
	return actual.(*Plan)
}

var (
	marshalerType = reflect.TypeFor[interface{ MarshalYAML() (any, error) }]()
	textType      = reflect.TypeFor[encoding.TextMarshaler]()
	jsonType      = reflect.TypeFor[json.Marshaler]()
	anyType       = reflect.TypeFor[any]()
	errorType     = reflect.TypeFor[error]()
	isZeroType    = reflect.TypeFor[interface{ IsZero() bool }]()
	timeType      = reflect.TypeFor[time.Time]()
	durationType  = reflect.TypeFor[time.Duration]()
	bigIntType    = reflect.TypeFor[big.Int]()
)

func build(t reflect.Type) *Plan {
	p := &Plan{Type: t, Kind: t.Kind()}
	if t.Kind() != reflect.Interface {
		p.Hooks = hooksOf(t)
	}
	if t.Kind() != reflect.Interface {
		p.Hooks |= jsonHooksOf(t)
		p.BadMarshal = badMarshal(t)
	}
	switch {
	case t == timeType:
		p.Special = SpecialTime
	case t == durationType:
		p.Special = SpecialDuration
	case nodeType != nil && (t == nodeType || t.Kind() == reflect.Pointer && t.Elem() == nodeType):
		p.Special = SpecialNode
	case t == bigIntType, t.Kind() == reflect.Pointer && t.Elem() == bigIntType:
		p.Special = SpecialBigInt
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 && p.Hooks&(HookMarshaler|HookPtrMarshaler|HookText|HookPtrText|HookJSON|HookPtrJSON) == 0 &&
		hooksOf(t.Elem())&(HookMarshaler|HookPtrMarshaler|HookText|HookPtrText) == 0:
		p.Special = SpecialBytes
	}
	if t.Kind() == reflect.Struct {
		p.Fields, p.InlineMap = structFields(t)
	}
	return p
}

func hooksOf(t reflect.Type) Hooks {
	var h Hooks
	isPtr := t.Kind() == reflect.Pointer
	pt := t
	if !isPtr {
		pt = reflect.PointerTo(t)
	}
	if t.Implements(marshalerType) {
		h |= HookMarshaler
	} else if !isPtr && pt.Implements(marshalerType) {
		h |= HookPtrMarshaler
	}
	if t.Implements(textType) {
		h |= HookText
	} else if !isPtr && pt.Implements(textType) {
		h |= HookPtrText
	}
	if t.Implements(isZeroType) {
		h |= HookIsZero
	} else if !isPtr && pt.Implements(isZeroType) {
		h |= HookPtrIsZero
	}
	return h
}

// jsonHooksOf reports the json.Marshaler hooks of t. It is kept apart from
// hooksOf because the yaml.v3 dialect does not use them.
func jsonHooksOf(t reflect.Type) Hooks {
	if t.Implements(jsonType) {
		return HookJSON
	}
	if t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(jsonType) {
		return HookPtrJSON
	}
	return 0
}

// badMarshal reports whether t, or a pointer to it, has a MarshalYAML method
// that is not func() (any, error).
// foreignNode reports whether t is yaml.v3's Node or a pointer to it, by name:
// this module does not import yaml.v3.
func foreignNode(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t.Name() != "Node" {
		return false
	}
	switch t.PkgPath() {
	case "gopkg.in/yaml.v3", "go.yaml.in/yaml/v3":
		return true
	}
	return false
}

func badMarshal(t reflect.Type) bool {
	pt := t
	if t.Kind() != reflect.Pointer {
		pt = reflect.PointerTo(t)
	}
	if pt.NumMethod() == 0 {
		return false
	}
	m, ok := pt.MethodByName("MarshalYAML")
	if !ok {
		return false
	}
	mt := m.Type
	return !(mt.NumIn() == 1 && mt.NumOut() == 2 && mt.Out(0) == anyType && mt.Out(1) == errorType)
}

type tagInfo struct {
	name                                    string
	omitEmpty, omitZero, flow, inline, skip bool
}

// parseTag reads the yaml tag, or the json tag when there is no yaml tag.
func parseTag(sf reflect.StructField) tagInfo {
	tag, ok := sf.Tag.Lookup("yaml")
	if !ok {
		tag, ok = sf.Tag.Lookup("json")
	}
	var ti tagInfo
	if !ok {
		return ti
	}
	if tag == "-" {
		ti.skip = true
		return ti
	}
	name, opts, _ := strings.Cut(tag, ",")
	ti.name = name
	for opts != "" {
		var o string
		o, opts, _ = strings.Cut(opts, ",")
		switch o {
		case "omitempty":
			ti.omitEmpty = true
		case "omitzero":
			ti.omitZero = true
		case "flow":
			ti.flow = true
		case "inline":
			ti.inline = true
		}
	}
	return ti
}

type fieldCtx struct {
	typ        reflect.Type
	index      []int
	viaPointer bool
}

// structFields applies the encoding/json rules for embedded structs: fields of
// embedded structs are promoted, the shallowest field of a name wins, a tagged
// field beats an untagged one at the same depth, and otherwise the name is
// dropped. The result is in declaration order.
func structFields(t reflect.Type) ([]Field, *InlineMap) {
	var current []fieldCtx
	next := []fieldCtx{{typ: t}}
	var count, nextCount map[reflect.Type]int
	visited := map[reflect.Type]bool{}

	type cand struct {
		Field
		depth int
	}
	var cands []cand
	var inlineMap *InlineMap
	inlineDepth := 0

	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, map[reflect.Type]int{}
		for _, f := range current {
			if visited[f.typ] {
				continue
			}
			visited[f.typ] = true
			for i := 0; i < f.typ.NumField(); i++ {
				sf := f.typ.Field(i)
				ft := sf.Type
				viaPtr := f.viaPointer
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if sf.Anonymous {
					if !sf.IsExported() && ft.Kind() != reflect.Struct {
						continue
					}
				} else if !sf.IsExported() {
					continue
				}
				ti := parseTag(sf)
				if ti.skip {
					continue
				}
				index := make([]int, len(f.index)+1)
				copy(index, f.index)
				index[len(f.index)] = i
				if sf.Type.Kind() == reflect.Pointer {
					viaPtr = true
				}

				if ti.inline && ft.Kind() == reflect.Map && sf.Type.Kind() == reflect.Map {
					if inlineMap == nil || len(index) < inlineDepth {
						inlineMap = &InlineMap{Index: index, Type: sf.Type, ViaPointer: f.viaPointer}
						inlineDepth = len(index)
					}
					continue
				}
				promote := ft.Kind() == reflect.Struct && ti.name == "" && (sf.Anonymous || ti.inline) &&
					ft != timeType && ft != bigIntType
				if !promote {
					name := ti.name
					tagged := name != ""
					if !tagged {
						name = strings.ToLower(sf.Name)
					}
					fld := Field{
						Name: name, Index: index, Type: sf.Type,
						OmitEmpty: ti.omitEmpty, OmitZero: ti.omitZero, Flow: ti.flow,
						Tagged: tagged, ViaPointer: f.viaPointer,
					}
					cands = append(cands, cand{fld, len(index)})
					if count[f.typ] > 1 {
						// The embedding type occurs more than once at this depth:
						// the name is ambiguous, so add a twin that cancels it.
						cands = append(cands, cand{fld, len(index)})
					}
					continue
				}
				nextCount[ft]++
				if nextCount[ft] == 1 {
					next = append(next, fieldCtx{typ: ft, index: index, viaPointer: viaPtr})
				}
			}
		}
	}

	// Choose the dominant field for each name.
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.depth != b.depth {
			return a.depth < b.depth
		}
		if a.Tagged != b.Tagged {
			return a.Tagged
		}
		return false
	})
	var out []Field
	for i := 0; i < len(cands); {
		j := i + 1
		for j < len(cands) && cands[j].Name == cands[i].Name {
			j++
		}
		// cands[i] is the best; it dominates unless another is equally good.
		if j-i == 1 || cands[i].depth != cands[i+1].depth || cands[i].Tagged != cands[i+1].Tagged {
			out = append(out, cands[i].Field)
		}
		i = j
	}
	sort.Slice(out, func(i, j int) bool { return lessIndex(out[i].Index, out[j].Index) })
	return out, inlineMap
}

func lessIndex(a, b []int) bool {
	for k := 0; k < len(a) && k < len(b); k++ {
		if a[k] != b[k] {
			return a[k] < b[k]
		}
	}
	return len(a) < len(b)
}
