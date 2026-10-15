package bench

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/resolve"
	"oneyaml.dev/go/internal/scan"
)

// ownRead is a third reader: the library's own event parser plus its Core
// resolver. It refuses aliases and more than one document.
func ownRead(src []byte) (any, error) {
	p := parse.New(src, 0)
	type frame struct {
		isMap   bool
		seq     []any
		m       map[any]any
		key     any
		haveKey bool
	}
	var stack []*frame
	var root any
	docs := 0
	add := func(v any) error {
		if len(stack) == 0 {
			root = v
			return nil
		}
		f := stack[len(stack)-1]
		if !f.isMap {
			f.seq = append(f.seq, v)
			return nil
		}
		if !f.haveKey {
			if v != nil && !reflect.TypeOf(v).Comparable() {
				return errors.New("non-comparable key")
			}
			f.key, f.haveKey = v, true
			return nil
		}
		if _, dup := f.m[f.key]; dup {
			return fmt.Errorf("duplicate key %v", f.key)
		}
		f.m[f.key] = v
		f.haveKey = false
		return nil
	}
	var buf, tag []byte
	for {
		ev, err := p.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch ev.Kind {
		case parse.DocumentStart:
			docs++
			if docs > 1 {
				return nil, errors.New("more than one document")
			}
		case parse.Alias:
			return nil, errors.New("alias")
		case parse.Scalar:
			buf, err = p.AppendScalar(buf[:0], &ev)
			if err != nil {
				return nil, err
			}
			tag, err = p.AppendTag(tag[:0], &ev)
			if err != nil {
				return nil, err
			}
			var v any
			switch {
			case string(tag) == "tag:yaml.org,2002:binary":
				clean := strings.Map(func(r rune) rune {
					if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
						return -1
					}
					return r
				}, string(buf))
				b, err := base64.StdEncoding.DecodeString(clean)
				if err != nil {
					return nil, err
				}
				v = string(b)
			case len(tag) > 0:
				v = string(buf)
			case ev.Style == scan.Plain:
				r := resolve.Core(buf)
				switch r.Kind {
				case resolve.Null:
					v = nil
				case resolve.Bool:
					v = r.B
				case resolve.Int:
					switch r.W {
					case resolve.Fits64:
						v = r.I
					case resolve.FitsU64:
						v = r.U
					default:
						v = r.Big()
					}
				case resolve.Float:
					v = r.F
				default:
					v = string(buf)
				}
			default:
				v = string(buf)
			}
			if err := add(v); err != nil {
				return nil, err
			}
		case parse.SequenceStart:
			stack = append(stack, &frame{seq: []any{}})
		case parse.MappingStart:
			stack = append(stack, &frame{isMap: true, m: map[any]any{}})
		case parse.SequenceEnd, parse.MappingEnd:
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			var v any = f.seq
			if f.isMap {
				v = f.m
			}
			if err := add(v); err != nil {
				return nil, err
			}
		}
	}
	return root, nil
}
