package yaml

import (
	"encoding"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
)

// A target describes the Go type a JSON value is about to be decoded into, as far
// as the conversion needs it: whether it is a string (so that a YAML number or
// boolean is written as a JSON string), and how to find the type of a struct
// field, map value or slice element.
type target struct {
	kind   targetKind
	elem   reflect.Type // map value or slice element
	fields []tfield
	exact  map[string]int
	folded map[string]int
}

type targetKind uint8

const (
	tgOther targetKind = iota
	tgString
	tgStruct
	tgMap
	tgSlice
)

type tfield struct {
	name string
	typ  reflect.Type
}

var (
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	targets             sync.Map // reflect.Type -> *target (nil entry stored as noTarget)
	noTarget            = &target{}
)

// targetOf returns the target for values decoded into type t, or nil when the
// conversion has nothing to do for it: t decodes itself (json.Unmarshaler or
// encoding.TextUnmarshaler), is an interface, or is not a string, struct, map or
// slice.
func targetOf(t reflect.Type) *target {
	if t == nil {
		return nil
	}
	if v, ok := targets.Load(t); ok {
		if v == noTarget {
			return nil
		}
		return v.(*target)
	}
	tg := buildTarget(t)
	if tg == nil {
		targets.Store(t, noTarget)
		return nil
	}
	actual, _ := targets.LoadOrStore(t, tg)
	return actual.(*target)
}

func buildTarget(t reflect.Type) *target {
	for t.Kind() == reflect.Pointer {
		if implementsUnmarshaler(t) {
			return nil
		}
		t = t.Elem()
	}
	if implementsUnmarshaler(t) {
		return nil
	}
	switch t.Kind() {
	case reflect.String:
		return &target{kind: tgString}
	case reflect.Map:
		return &target{kind: tgMap, elem: t.Elem()}
	case reflect.Slice:
		return &target{kind: tgSlice, elem: t.Elem()}
	case reflect.Struct:
		return buildStruct(t)
	}
	return nil
}

func implementsUnmarshaler(t reflect.Type) bool {
	if t.Kind() == reflect.Interface {
		return t.Implements(jsonUnmarshalerType) || t.Implements(textUnmarshalerType)
	}
	p := reflect.PointerTo(t)
	return p.Implements(jsonUnmarshalerType) || p.Implements(textUnmarshalerType) ||
		t.Implements(jsonUnmarshalerType) || t.Implements(textUnmarshalerType)
}

// field lookup: the exact name first, then a case-insensitive match, which is
// what encoding/json does when it decodes the same key.
func (tg *target) field(key []byte) reflect.Type {
	if i, ok := tg.exact[string(key)]; ok {
		return tg.fields[i].typ
	}
	if len(tg.folded) == 0 {
		return nil
	}
	var buf [64]byte
	var low []byte
	if len(key) <= len(buf) {
		low = buf[:len(key)]
	} else {
		low = make([]byte, len(key))
	}
	ascii := true
	for i, c := range key {
		if c >= 0x80 {
			ascii = false
			break
		}
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		low[i] = c
	}
	var name string
	if ascii {
		name = string(low)
	} else {
		name = strings.ToLower(string(key))
	}
	if i, ok := tg.folded[name]; ok {
		return tg.fields[i].typ
	}
	return nil
}

// buildStruct lists the JSON-visible fields of t the way encoding/json does:
// the json tag names a field or hides it with "-", embedded structs without a
// name are promoted, and of several fields with one name the shallowest wins,
// then the tagged one, otherwise none.
func buildStruct(t reflect.Type) *target {
	type cand struct {
		name   string
		typ    reflect.Type
		depth  int
		tagged bool
	}
	var cands []cand
	type level struct {
		typ   reflect.Type
		depth int
	}
	current := []level{{t, 0}}
	seen := map[reflect.Type]bool{}
	for len(current) > 0 {
		var next []level
		for _, lv := range current {
			if seen[lv.typ] {
				continue
			}
			seen[lv.typ] = true
			for i := 0; i < lv.typ.NumField(); i++ {
				sf := lv.typ.Field(i)
				if sf.Anonymous {
					ft := sf.Type
					if ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					if !sf.IsExported() && ft.Kind() != reflect.Struct {
						continue
					}
				} else if !sf.IsExported() {
					continue
				}
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, _, _ := strings.Cut(tag, ",")
				if !validTagName(name) {
					name = ""
				}
				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if name == "" && sf.Anonymous && ft.Kind() == reflect.Struct {
					next = append(next, level{ft, lv.depth + 1})
					continue
				}
				tagged := name != ""
				if name == "" {
					name = sf.Name
				}
				cands = append(cands, cand{name, sf.Type, lv.depth, tagged})
			}
		}
		current = next
	}
	byName := map[string][]int{}
	var order []string
	for i, c := range cands {
		if _, ok := byName[c.name]; !ok {
			order = append(order, c.name)
		}
		byName[c.name] = append(byName[c.name], i)
	}
	tg := &target{kind: tgStruct, exact: map[string]int{}, folded: map[string]int{}}
	for _, name := range order {
		idx := byName[name]
		best := idx[0]
		dominant := true
		if len(idx) > 1 {
			// The shallowest field wins; among those, the tagged one; if that is
			// still ambiguous, the name is dropped.
			minDepth := cands[idx[0]].depth
			for _, i := range idx {
				if cands[i].depth < minDepth {
					minDepth = cands[i].depth
				}
			}
			var atMin []int
			for _, i := range idx {
				if cands[i].depth == minDepth {
					atMin = append(atMin, i)
				}
			}
			if len(atMin) > 1 {
				var tagged []int
				for _, i := range atMin {
					if cands[i].tagged {
						tagged = append(tagged, i)
					}
				}
				if len(tagged) == 1 {
					atMin = tagged
				} else {
					dominant = false
				}
			}
			best = atMin[0]
		}
		if !dominant {
			continue
		}
		tg.exact[name] = len(tg.fields)
		tg.fields = append(tg.fields, tfield{name, cands[best].typ})
	}
	for i, f := range tg.fields {
		low := strings.ToLower(f.name)
		if _, ok := tg.folded[low]; !ok {
			tg.folded[low] = i
		}
	}
	return tg
}

func validTagName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c > 0x7f:
		default:
			return false
		}
	}
	return true
}
