package typeinfo

import (
	"errors"
	"reflect"
	"strings"
	"sync"
)

var cacheV3 sync.Map // reflect.Type -> *Plan

// ForV3 returns the plan for t under the struct rules of yaml.v3:
//   - only the yaml tag names a field (no json fallback), and a tag with no
//     "key:" part is read whole as the yaml tag;
//   - an embedded struct is an ordinary field unless it is tagged inline;
//   - the options are omitempty, flow and inline; anything else is an error,
//     reported through Plan.Err so the caller can return it instead of failing
//     while the plan is built;
//   - two fields with one key are an error.
//
// Only time.Time and time.Duration are special types.
func ForV3(t reflect.Type) *Plan {
	if p, ok := cacheV3.Load(t); ok {
		return p.(*Plan)
	}
	p := &Plan{Type: t, Kind: t.Kind(), V3: true}
	if t.Kind() != reflect.Interface {
		p.Hooks = hooksOf(t)
		// A MarshalYAML this package cannot call is reported, not skipped.
		p.BadMarshal = badMarshal(t)
	}
	switch t {
	case timeType:
		p.Special = SpecialTime
	case durationType:
		p.Special = SpecialDuration
	}
	if t.Kind() == reflect.Struct {
		p.Fields, p.InlineMap, p.Err = structFieldsV3(t)
	}
	actual, _ := cacheV3.LoadOrStore(t, p)
	return actual.(*Plan)
}

func structFieldsV3(t reflect.Type) (fields []Field, im *InlineMap, err error) {
	seen := map[string]bool{}
	var walk func(t reflect.Type, prefix []int, viaPtr bool) error
	walk = func(t reflect.Type, prefix []int, viaPtr bool) error {
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if sf.PkgPath != "" && !sf.Anonymous {
				continue
			}
			tag := sf.Tag.Get("yaml")
			if tag == "" && !strings.Contains(string(sf.Tag), ":") {
				tag = string(sf.Tag)
			}
			if tag == "-" {
				continue
			}
			var omitEmpty, flow, inline bool
			parts := strings.Split(tag, ",")
			if len(parts) > 1 {
				for _, flag := range parts[1:] {
					switch flag {
					case "omitempty":
						omitEmpty = true
					case "flow":
						flow = true
					case "inline":
						inline = true
					default:
						return errors.New("unsupported flag " + quote(flag) + " in tag " + quote(tag) + " of type " + t.String())
					}
				}
				tag = parts[0]
			}
			index := append(append([]int(nil), prefix...), i)
			if inline {
				switch sf.Type.Kind() {
				case reflect.Map:
					if im != nil {
						return errors.New("multiple ,inline maps in struct " + t.String())
					}
					if sf.Type.Key().Kind() != reflect.String || sf.Type.Key().PkgPath() != "" {
						return errors.New("option ,inline needs a map with string keys in struct " + t.String())
					}
					im = &InlineMap{Index: index, Type: sf.Type, ViaPointer: viaPtr}
				case reflect.Struct, reflect.Pointer:
					ft := sf.Type
					for ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					if ft.Kind() != reflect.Struct {
						return errors.New("option ,inline may only be used on a struct or map field")
					}
					if err := walk(ft, index, viaPtr || sf.Type.Kind() == reflect.Pointer); err != nil {
						return err
					}
				default:
					return errors.New("option ,inline may only be used on a struct or map field")
				}
				continue
			}
			name := tag
			if name == "" {
				name = strings.ToLower(sf.Name)
			}
			if seen[name] {
				return errors.New("duplicated key '" + name + "' in struct " + t.String())
			}
			seen[name] = true
			fields = append(fields, Field{Name: name, Index: index, Type: sf.Type, OmitEmpty: omitEmpty,
				Flow: flow, Tagged: tag != "", ViaPointer: viaPtr, V3: true})
		}
		return nil
	}
	if err := walk(t, nil, false); err != nil {
		return nil, nil, err
	}
	return fields, im, nil
}

func quote(s string) string { return `"` + s + `"` }
