package emit

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
)

var nodeValue func(v reflect.Value) (any, error)

// SetNodeValue registers how a document node is turned into a plain value so
// that a struct holding one is written as the node's content. It must run from
// an init function.
func SetNodeValue(f func(v reflect.Value) (any, error)) { nodeValue = f }

// jsonValue writes the JSON that jm produces as the equivalent YAML value. A
// JSON object is written with sorted keys, like every other mapping.
func (e *Encoder) jsonValue(v reflect.Value, jm json.Marshaler, p pos) error {
	b, err := jm.MarshalJSON()
	if err != nil {
		return &Error{Kind: KindHook, Type: v.Type(), Detail: "MarshalJSON of " + v.Type().String() + ": " + err.Error(), Err: err}
	}
	x, err := jsonToAny(b, e.maxDigits)
	if ed, ok := err.(errDigits); ok {
		return &Error{Kind: KindNumberDigits, Type: v.Type(), Max: ed.max, Detail: "MarshalJSON of " + v.Type().String() + " gave a number with more than " + strconv.Itoa(ed.max) + " digits"}
	}
	if err != nil {
		return &Error{Kind: KindHook, Type: v.Type(), Detail: "MarshalJSON of " + v.Type().String() + " gave text that cannot be written as YAML: " + err.Error(), Err: err}
	}
	tracked, err := e.enter(0)
	if err != nil {
		return err
	}
	err = e.any(x, p)
	e.leave(0, tracked)
	return err
}

// jsonNum is a number from JSON, kept as the text it had. Every JSON number is
// also a YAML 1.2 Core integer or float, so the text reads back with the same
// digits; converting to int64 or float64 could round it.
type jsonNum string

// jsonToAny parses one JSON value. maxDigits bounds the length of a number's
// text; 0 means no bound.
func jsonToAny(b []byte, maxDigits int) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errTrailing
	}
	return fixNumbers(x, maxDigits)
}

type jsonErr string

func (e jsonErr) Error() string { return string(e) }

const errTrailing = jsonErr("data after the top-level value")

// errDigits reports a number over the digit limit.
type errDigits struct{ max int }

func (e errDigits) Error() string { return "number has too many digits" }

// fixNumbers replaces json.Number values in place. The decoder limits nesting
// itself, so the recursion is bounded.
func fixNumbers(x any, maxDigits int) (any, error) {
	switch v := x.(type) {
	case json.Number:
		if maxDigits > 0 && len(v) > maxDigits {
			return nil, errDigits{maxDigits}
		}
		return jsonNum(v), nil
	case []any:
		for i := range v {
			y, err := fixNumbers(v[i], maxDigits)
			if err != nil {
				return nil, err
			}
			v[i] = y
		}
	case map[string]any:
		for k, e := range v {
			y, err := fixNumbers(e, maxDigits)
			if err != nil {
				return nil, err
			}
			v[k] = y
		}
	}
	return x, nil
}
