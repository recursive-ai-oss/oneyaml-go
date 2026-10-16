package bind

import (
	"encoding/json"
	"math"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"unicode/utf8"

	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/resolve"
	"oneyaml.dev/go/internal/scan"
)

// jsonHook decodes the node at ev through the json.Unmarshaler of the value: the
// node is read in its generic form, written as JSON text and handed to
// UnmarshalJSON. Types written for encoding/json (Kubernetes quantities, say)
// then read the same YAML they would read as JSON.
//
// A scalar that resolves to null, and any scalar that the type's
// encoding.TextUnmarshaler reads, are left to the ordinary path.
func (b *Binder) jsonHook(v reflect.Value, ev *parse.Event, dp *dplan) (bool, error) {
	if ev.Kind == parse.Scalar && (dp.textUnm || b.isNull(ev)) {
		return false, nil
	}
	start := ev.Start
	t := v.Type()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var buf []byte
	var err error
	if num, ok, nerr := b.jsonNumber(ev); nerr != nil {
		return true, nerr
	} else if ok {
		// Keep the digits as written: a float64 would round them.
		buf = append(b.jsonBuf[:0], num...)
		b.jsonBuf = buf[:0]
	} else {
		var x any
		if x, err = b.generic(ev); err != nil {
			return true, err
		}
		buf, err = appendJSON(b.jsonBuf[:0], x)
		b.jsonBuf = buf[:0]
	}
	if err != nil {
		return true, &Error{Kind: KindType, Offset: start, GoType: t.String(),
			Msg: "cannot read this value into " + t.String() + " through its UnmarshalJSON: " + err.Error()}
	}
	v = deref(v)
	if uerr := v.Addr().Interface().(json.Unmarshaler).UnmarshalJSON(buf); uerr != nil {
		return true, &Error{Kind: KindType, Offset: start, GoType: t.String(), Err: uerr,
			Msg: "cannot unmarshal " + abbrev(buf) + " into " + t.String() + ": " + uerr.Error()}
	}
	return true, nil
}

// jsonNumber returns the source text of a plain scalar that the Core schema reads
// as a number and whose text is already a JSON number. Anything else (a tag, a
// quoted scalar, 0x1F, +5, .5) takes the generic route and is rewritten.
func (b *Binder) jsonNumber(ev *parse.Event) ([]byte, bool, error) {
	if ev.Kind != parse.Scalar || ev.Style != scan.Plain || ev.HasTag() || b.cfg.Dialect != DialectCore {
		return nil, false, nil
	}
	text := b.src[ev.Start:ev.End]
	if !isJSONNumber(text) {
		return nil, false, nil
	}
	r := resolve.Core(text)
	if r.Kind != resolve.Int && r.Kind != resolve.Float {
		return nil, false, nil
	}
	if err := b.numLimit(&r, text, ev.Start); err != nil {
		return nil, false, err
	}
	return text, true, nil
}

// isJSONNumber reports whether s is a number as the JSON grammar writes it.
func isJSONNumber(s []byte) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	if i >= len(s) {
		return false
	}
	if s[i] == '0' {
		i++
	} else if s[i] >= '1' && s[i] <= '9' {
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	} else {
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		n := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == n {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		n := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == n {
			return false
		}
	}
	return i == len(s)
}

type jsonFrame struct {
	arr  []any
	keys []string
	m    map[string]any
	i    int
}

func (f *jsonFrame) len() int {
	if f.m != nil {
		return len(f.keys)
	}
	return len(f.arr)
}

// appendJSON appends x, a value of the generic form, as JSON. It uses an
// explicit stack, so the depth of the value is not Go recursion. A mapping with
// a key that is not a string, and a number JSON has no form for, are errors.
func appendJSON(dst []byte, x any) ([]byte, error) {
	var stack []jsonFrame
	cur := x
	for {
		switch v := cur.(type) {
		case nil:
			dst = append(dst, "null"...)
		case bool:
			dst = strconv.AppendBool(dst, v)
		case string:
			dst = appendJSONString(dst, v)
		case int:
			dst = strconv.AppendInt(dst, int64(v), 10)
		case int64:
			dst = strconv.AppendInt(dst, v, 10)
		case uint64:
			dst = strconv.AppendUint(dst, v, 10)
		case *big.Int:
			dst = v.Append(dst, 10)
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return dst, jsonErr("a NaN or infinite number has no JSON form")
			}
			dst = appendJSONFloat(dst, v)
		case []any:
			if len(v) == 0 {
				dst = append(dst, "[]"...)
				break
			}
			dst = append(dst, '[')
			stack = append(stack, jsonFrame{arr: v})
			cur = v[0]
			continue
		case map[string]any:
			if len(v) == 0 {
				dst = append(dst, "{}"...)
				break
			}
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			dst = append(dst, '{')
			dst = appendJSONString(dst, keys[0])
			dst = append(dst, ':')
			stack = append(stack, jsonFrame{keys: keys, m: v})
			cur = v[keys[0]]
			continue
		case map[any]any:
			return dst, jsonErr("a mapping with a key that is not a string has no JSON form")
		default:
			return dst, jsonErr("a value of type " + reflect.TypeOf(cur).String() + " has no JSON form")
		}
		// cur is written whole: move to the next sibling, closing finished
		// collections on the way up.
		for {
			if len(stack) == 0 {
				return dst, nil
			}
			f := &stack[len(stack)-1]
			f.i++
			if f.i < f.len() {
				dst = append(dst, ',')
				if f.m != nil {
					dst = appendJSONString(dst, f.keys[f.i])
					dst = append(dst, ':')
					cur = f.m[f.keys[f.i]]
				} else {
					cur = f.arr[f.i]
				}
				break
			}
			if f.m != nil {
				dst = append(dst, '}')
			} else {
				dst = append(dst, ']')
			}
			stack = stack[:len(stack)-1]
		}
	}
}

type jsonErr string

func (e jsonErr) Error() string { return string(e) }

// appendJSONFloat formats f as encoding/json does.
func appendJSONFloat(dst []byte, f float64) []byte {
	abs := math.Abs(f)
	format := byte('f')
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}
	dst = strconv.AppendFloat(dst, f, format, -1, 64)
	if format == 'e' {
		// e-09 becomes e-9
		n := len(dst)
		if n >= 4 && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
			dst[n-2] = dst[n-1]
			dst = dst[:n-1]
		}
	}
	return dst
}

const hexDigits = "0123456789abcdef"

// appendJSONString appends s as a JSON string. Bytes that are not valid UTF-8
// become U+FFFD, as encoding/json writes them.
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '"' || c == '\\':
				dst = append(dst, '\\', c)
			case c == '\n':
				dst = append(dst, '\\', 'n')
			case c == '\r':
				dst = append(dst, '\\', 'r')
			case c == '\t':
				dst = append(dst, '\\', 't')
			case c < 0x20 || c == 0x7f:
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
			default:
				dst = append(dst, c)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			dst = append(dst, "\\ufffd"...)
		} else {
			dst = append(dst, s[i:i+size]...)
		}
		i += size
	}
	return append(dst, '"')
}
