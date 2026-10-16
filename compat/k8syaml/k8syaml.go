// Package yaml is a compatibility adapter for sigs.k8s.io/yaml (and its parent
// ghodss/yaml) built on oneyaml. Its functions have the names and signatures of
// the original's, which it covers except for what "Not covered" lists at the
// end, and the differences are listed below.
//
// # Migrating
//
// Change the import path; your call sites stay as they are. Then check the
// differences listed under "Where it differs" against what your program relies
// on.
//
//	// before
//	import "sigs.k8s.io/yaml"
//	// after (the package is named yaml, as the original is)
//	import "oneyaml.dev/go/compat/k8syaml"
//
// The functions keep their names, signatures and behaviour: Marshal writes a
// value through its json tags, Unmarshal reads YAML into a value through its json
// tags, and YAMLToJSON, JSONToYAML and the strict forms convert between the two
// text formats. Types that implement json.Marshaler, json.Unmarshaler or
// encoding.TextUnmarshaler are honoured exactly as encoding/json honours them,
// because the final step of every call is encoding/json itself.
//
// This package is the opt-in compatibility mode. The primary API of oneyaml
// (Unmarshal, Marshal, Parse and the Document type in the parent package) reads
// YAML 1.2 and uses yaml tags; this package reads what the sigs.k8s.io/yaml
// family reads, so that existing manifests mean the same thing after the swap.
//
// # What is read
//
// Like sigs.k8s.io/yaml, the package reads YAML 1.1 scalars: the plain words y,
// yes, on, n, no and off are booleans, and 0755 is an octal integer. Merge keys
// ("<<") are expanded, anchors and aliases are followed, only the first document
// of a stream is read, non-string keys are turned into strings (1 becomes "1",
// true becomes "true"), and every number that fits an integer type stays an
// integer.
//
// # Where it differs
//
// The differences are the cases in which sigs.k8s.io/yaml loses or changes data.
//
//   - A merge key never overrides a key written in the same mapping, wherever it
//     appears. The reader behind sigs.k8s.io/yaml lets a merge that comes after a
//     key replace that key.
//   - A float read into a string field, or used as a key, keeps its full
//     precision. The reader behind sigs.k8s.io/yaml rounds it to 32 bits.
//   - A number or boolean read into a field promoted from an embedded struct is
//     converted to a string like any other string field; sigs.k8s.io/yaml fails
//     on it.
//   - YAML 1.2 syntax that the reader behind sigs.k8s.io/yaml rejects or reads
//     wrongly is read as the specification says (flow mappings with empty values,
//     anchors with ':' in the name, folded and literal blocks with blank lines,
//     tabs, directives, UTF-16 and UTF-32 input).
//   - Keys that print the same (1 and "1", or 1 and 1.0) are one key in JSON.
//     The other library keeps one of them, chosen at random; the last one written
//     wins here.
//   - Marshal and JSONToYAML write what sigs.k8s.io/yaml writes, byte for byte,
//     except where its output cannot be read back: the string "<<" is quoted so
//     that it is not read as a merge key; U+0085, U+2028 and U+2029 are written as
//     escapes; a string at the root that starts with a space or a line break and
//     has more lines is written quoted; and characters such as DEL and U+0080 to
//     U+009F, which make sigs.k8s.io/yaml fail, are escaped.
//   - Unmarshal with a nil target returns an error; sigs.k8s.io/yaml panics.
//   - Reading is bounded: nesting is limited to 10,000 levels and alias expansion
//     to one million nodes (more with a larger input), so that a few bytes cannot
//     expand into gigabytes.
//
// The error text differs from sigs.k8s.io/yaml and is not part of the contract
// (neither is theirs). Errors from the YAML side wrap the oneyaml error types
// (SyntaxError and LimitError), so errors.As works.
//
// The limits are the default limits of the oneyaml package and cannot be
// changed. For input from a source you do not control, read it with the oneyaml
// package and its Untrusted option instead.
//
// Not covered: JSONObjectToYAMLObject, which returns a type of the yaml.v2
// module, and the kyaml, yamlfmt and goyaml.v2/v3 subpackages of
// sigs.k8s.io/yaml.
package yaml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	oneyaml "oneyaml.dev/go"
)

// Marshal marshals obj into JSON using encoding/json and then converts the JSON
// to YAML with JSONToYAML. Field names come from json tags.
func Marshal(obj interface{}) ([]byte, error) {
	jsonBytes, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("error marshaling into JSON: %w", err)
	}
	return JSONToYAML(jsonBytes)
}

// JSONOpt is a decoding option for decoding from JSON format.
type JSONOpt func(*json.Decoder) *json.Decoder

// DisallowUnknownFields configures the JSON decoder to fail on fields that the
// target struct does not have, instead of dropping them.
func DisallowUnknownFields(d *json.Decoder) *json.Decoder {
	d.DisallowUnknownFields()
	return d
}

// Unmarshal converts the YAML to JSON and then unmarshals the JSON into obj with
// encoding/json. Options for the json.Decoder can be given, for example to read
// untyped numbers as json.Number (so that integers above 2^53 keep their value)
// or to disallow unknown fields. obj must be a non-nil pointer.
//
// As with encoding/json, decoding is case-insensitive, an unknown field is
// ignored (see UnmarshalStrict and DisallowUnknownFields), and an untyped number
// becomes a float64. A scalar read into a string field is converted to a string:
// the YAML 1.1 words yes and no, numbers and booleans all become the string of
// their value.
func Unmarshal(yamlBytes []byte, obj interface{}, opts ...JSONOpt) error {
	return unmarshal(yamlBytes, obj, false, opts)
}

// UnmarshalStrict is like Unmarshal, with two differences: a key that occurs
// twice in a mapping is an error, and so is a field the target struct does not
// have.
func UnmarshalStrict(yamlBytes []byte, obj interface{}, opts ...JSONOpt) error {
	return unmarshal(yamlBytes, obj, true, append(opts[:len(opts):len(opts)], DisallowUnknownFields))
}

func unmarshal(y []byte, obj interface{}, strict bool, opts []JSONOpt) error {
	w := getWalker()
	defer w.release()
	if obj == nil {
		return errors.New("k8syaml: Unmarshal needs a pointer to decode into, not nil")
	}
	tt := rootType(obj)
	if err := w.convert(y, strict, tt); err != nil {
		return fmt.Errorf("error converting YAML to JSON: %w", yamlError(err))
	}
	var err error
	if len(opts) == 0 {
		err = json.Unmarshal(w.out, &obj)
	} else {
		d := json.NewDecoder(bytes.NewReader(w.out))
		for _, opt := range opts {
			d = opt(d)
		}
		err = d.Decode(&obj)
		if err != nil {
			err = fmt.Errorf("while decoding JSON: %w", err)
		}
	}
	if err != nil {
		return fmt.Errorf("error unmarshaling JSON: %w", err)
	}
	return nil
}

// rootType is the type the JSON will be decoded into, following pointers and
// interfaces that hold values the way encoding/json does when it decodes.
func rootType(obj any) reflect.Type {
	v := reflect.ValueOf(obj)
	for {
		switch {
		case v.Kind() == reflect.Interface && !v.IsNil():
			v = v.Elem()
		case v.Kind() == reflect.Pointer && !v.IsNil():
			v = v.Elem()
		default:
			return v.Type()
		}
	}
}

// yamlError gives errors from the conversion the "yaml:" prefix the other
// library's errors carry, unless they are oneyaml errors, which already say what
// they are.
func yamlError(err error) error {
	var se *oneyaml.SyntaxError
	var le *oneyaml.LimitError
	if errors.As(err, &se) || errors.As(err, &le) || strings.HasPrefix(err.Error(), "yaml:") || strings.HasPrefix(err.Error(), "json:") {
		return err
	}
	return fmt.Errorf("yaml: %w", err)
}

// YAMLToJSON converts YAML to JSON. Since JSON is a subset of YAML, passing JSON
// through it changes nothing but whitespace and key order.
//
// Keys come out sorted, as encoding/json writes a map. Non-string keys (int,
// bool and float) become strings; null keys are an error. The !!binary tag is
// decoded to its text, so use plain base64 text when the JSON should carry the
// encoded form. Duplicate keys are not an error; the last one wins. See
// YAMLToJSONStrict.
func YAMLToJSON(y []byte) ([]byte, error) {
	return yamlToJSON(y, false)
}

// YAMLToJSONStrict is like YAMLToJSON but fails on a duplicate key.
func YAMLToJSONStrict(y []byte) ([]byte, error) {
	return yamlToJSON(y, true)
}

func yamlToJSON(y []byte, strict bool) ([]byte, error) {
	w := getWalker()
	defer w.release()
	if err := w.convert(y, strict, nil); err != nil {
		return nil, yamlError(err)
	}
	return append([]byte(nil), w.out...), nil
}

// JSONToYAML converts JSON to YAML.
//
// Mappings are written with keys in natural order (a2 before a10), sequences are
// not indented below their key, long strings are folded after column 80, and
// integers up to 64 bits keep every digit. Strings that a YAML 1.1 reader would
// take for something else (yes, 0755, 1:30, 2001-12-14) are quoted.
func JSONToYAML(j []byte) ([]byte, error) {
	p, err := parseJSON(j)
	if err != nil {
		if !errors.Is(err, errNotJSON) {
			return nil, err
		}
		// Not strict JSON: read it as YAML, which JSON is a subset of.
		w := getWalker()
		defer w.release()
		if cerr := w.convert(j, false, nil); cerr != nil {
			return nil, yamlError(cerr)
		}
		if p, err = parseJSONMode(w.out, true); err != nil {
			return nil, yamlError(err)
		}
	}
	return writeYAML(p, make([]byte, 0, len(j)+len(j)/4+16))
}
