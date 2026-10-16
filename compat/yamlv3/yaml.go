package yaml

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	oneyaml "oneyaml.dev/go"
	"oneyaml.dev/go/internal/compatopt"
)

// Unmarshaler is implemented by types that read their own YAML. UnmarshalYAML is
// given the node that holds the value; an error it returns stops the decode and
// is returned as is, except a *TypeError, whose messages are collected with the
// other type errors of the document.
type Unmarshaler interface {
	UnmarshalYAML(value *Node) error
}

// obsoleteUnmarshaler is the older, function style of Unmarshaler, which is still
// honoured.
type obsoleteUnmarshaler interface {
	UnmarshalYAML(unmarshal func(interface{}) error) error
}

// Marshaler is implemented by types that choose their own YAML form. The value
// MarshalYAML returns is written in place of the receiver. If it returns an
// error, writing stops and the error is returned.
type Marshaler interface {
	MarshalYAML() (interface{}, error)
}

// IsZeroer is used to decide whether a value is zero for the omitempty tag
// option. time.Time is one implementation.
type IsZeroer interface {
	IsZero() bool
}

// A TypeError is returned by Unmarshal when one or more values in the document
// cannot be stored in the type asked for. The rest of the document is still
// decoded, so the target holds what could be read.
type TypeError struct {
	Errors []string
}

// Error returns the messages of every value that did not fit, in the form
// yaml.v3 uses.
func (e *TypeError) Error() string {
	return fmt.Sprintf("yaml: unmarshal errors:\n  %s", strings.Join(e.Errors, "\n  "))
}

// softTypeError is a TypeError a user's UnmarshalYAML returned: the decoder
// collects its messages and goes on.
type softTypeError struct{ *TypeError }

func (e softTypeError) SoftMessages() []string { return e.Errors }

func v3Option() oneyaml.Option { return compatopt.V3().(oneyaml.Option) }

func compactOption() oneyaml.Option { return compatopt.V3Compact().(oneyaml.Option) }

// readOptions are the options every read passes to the core: yaml.v3's rules,
// merge keys on, and the core's default limits.
func readOptions(known bool) []oneyaml.Option {
	opts := []oneyaml.Option{v3Option(), oneyaml.MergeKeys()}
	if known {
		opts = append(opts, oneyaml.KnownFields())
	}
	return opts
}

// target returns the value to decode into. A map may be passed as itself: it is
// filled in place, so it gets a pointer to a copy of the (shared) map header.
func target(out interface{}) (interface{}, error) {
	v := reflect.ValueOf(out)
	switch {
	case v.Kind() == reflect.Pointer && !v.IsNil():
		return out, nil
	case v.Kind() == reflect.Map && !v.IsNil():
		p := reflect.New(v.Type())
		p.Elem().Set(v)
		return p.Interface(), nil
	}
	return nil, errors.New("yaml: Unmarshal needs a non-nil pointer or map, got " + typeName(out))
}

func typeName(v interface{}) string {
	if v == nil {
		return "nil"
	}
	return reflect.TypeOf(v).String()
}

// Unmarshal decodes the first document found within the in byte slice and
// assigns the decoded values into the out value.
//
// Maps and pointers (to a struct, string, int, etc) are accepted as out values.
// If an internal pointer within a struct is not initialized, the package will
// initialize it if necessary for unmarshalling the provided data. The out
// parameter must not be nil.
//
// The type of the decoded values should be compatible with the respective values
// in out. If one or more values cannot be decoded due to type mismatches,
// decoding continues partially until the end of the YAML content, and a
// *TypeError is returned with details for all missed values.
//
// Struct fields are only unmarshalled if they are exported (have an upper case
// first letter), and are unmarshalled using the field name lowercased as the
// default key. Custom keys may be defined via the "yaml" name in the field tag:
// the content preceding the first comma is used as the key, and the following
// comma-separated options are used to tweak the marshalling process (see
// Marshal). Conflicting names result in an error.
func Unmarshal(in []byte, out interface{}) error {
	t, err := target(out)
	if err != nil {
		return err
	}
	return convertError(oneyaml.Unmarshal(in, t, readOptions(false)...))
}

// A Decoder reads and decodes YAML values from an input stream.
type Decoder struct {
	r     io.Reader
	dec   *oneyaml.Decoder
	known bool
}

// NewDecoder returns a new decoder that reads from r.
//
// The decoder introduces its own buffering and may read data from r beyond the
// YAML values requested.
func NewDecoder(r io.Reader) *Decoder { return &Decoder{r: r} }

// KnownFields ensures that the keys in decoded mappings exist as fields in the
// struct being decoded into. The setting applies to each Decode that follows.
func (dec *Decoder) KnownFields(enable bool) { dec.known = enable }

// Decode reads the next YAML-encoded value from its input and stores it in the
// value pointed to by v. It returns io.EOF when there are no more documents.
//
// See the documentation of Unmarshal for the conversion of YAML into a Go value.
func (dec *Decoder) Decode(v interface{}) error {
	if dec.dec == nil {
		// The core decoder is made once and cannot change its options later, so it
		// always reports unknown fields; a Decode that does not want them drops
		// those reports (see dropUnknownFields).
		dec.dec = oneyaml.NewDecoder(dec.r, readOptions(true)...)
	}
	t, err := target(v)
	if err != nil {
		return err
	}
	err = dec.dec.Decode(t)
	if err == io.EOF {
		return io.EOF
	}
	if !dec.known {
		err = dropUnknownFields(err)
	}
	return convertError(err)
}

// dropUnknownFields removes the reports of struct keys that name no field from
// the type errors collected for a document, and the error itself if nothing else
// is left.
func dropUnknownFields(err error) error {
	var col *compatopt.Collected
	if !errors.As(err, &col) {
		return err
	}
	kept := make([]compatopt.Item, 0, len(col.Items))
	for _, it := range col.Items {
		if strings.HasPrefix(it.Msg, "field ") && strings.Contains(it.Msg, " not found in type ") {
			continue
		}
		kept = append(kept, it)
	}
	if len(kept) == len(col.Items) {
		return err
	}
	if len(kept) == 0 {
		return nil
	}
	return &compatopt.Collected{Items: kept}
}

// Marshal serializes the value provided into a YAML document. The structure of
// the generated document will reflect the structure of the value itself. Maps
// and pointers (to struct, string, int, etc) are accepted as the in value.
//
// Struct fields are only marshalled if they are exported (have an upper case
// first letter), and are marshalled using the field name lowercased as the
// default key. Custom keys may be defined via the "yaml" name in the field tag:
// the content preceding the first comma is used as the key, and the following
// comma-separated options are used to tweak the marshalling process. Conflicting
// names result in an error.
//
// The field tag format accepted is:
//
//	`(...) yaml:"[<key>][,<flag1>[,<flag2>]]" (...)`
//
// The following flags are currently supported:
//
//	omitempty    Only include the field if it's not set to the zero
//	             value for the type or to empty slices or maps.
//	             Zero valued structs will be omitted if all their public
//	             fields are zero, unless they implement an IsZero
//	             method (see the IsZeroer interface type), in which
//	             case the field will be excluded if IsZero returns true.
//
//	flow         Marshal using a flow style (useful for structs,
//	             sequences and maps).
//
//	inline       Inline the field, which must be a struct or a map,
//	             causing all of its fields or keys to be processed as if
//	             they were part of the outer struct. For maps, keys must
//	             not conflict with the yaml keys of other struct fields.
//
// In addition, if the key is "-", the field is ignored.
func Marshal(in interface{}) ([]byte, error) {
	if n, ok := nodeOf(in); ok {
		return marshalNode(n)
	}
	out, err := oneyaml.Marshal(in, writeOptions(0, false)...)
	if err != nil {
		return nil, convertError(err)
	}
	return out, nil
}

// writeOptions returns the options for writing: the v3 layout, indented by
// indent (0 for the default of 4).
func writeOptions(indent int, compact bool) []oneyaml.Option {
	opts := []oneyaml.Option{v3Option(), oneyaml.Indent(indent)}
	if compact {
		opts = append(opts, compactOption())
	}
	return opts
}

// An Encoder writes YAML values to an output stream.
type Encoder struct {
	w       io.Writer
	enc     *oneyaml.Encoder
	indent  int
	compact bool
	docs    int
	closed  bool
}

// NewEncoder returns a new encoder that writes to w. The Encoder should be
// closed after use to flush all data to w.
func NewEncoder(w io.Writer) *Encoder { return &Encoder{w: w} }

// Encode writes the YAML encoding of v to the stream. If multiple items are
// encoded to the stream, the second and subsequent document will be preceded
// with a "---" document separator, but the first will not.
//
// See the documentation for Marshal for details about the conversion of Go
// values to YAML.
func (e *Encoder) Encode(v interface{}) error {
	if e.closed {
		return errors.New("yaml: expected STREAM-START")
	}
	if n, ok := nodeOf(v); ok {
		return e.encodeNode(n)
	}
	if e.enc == nil {
		e.enc = oneyaml.NewEncoder(e.w, writeOptions(e.indent, e.compact)...)
	}
	e.docs++
	return convertError(e.enc.Encode(v))
}

// SetIndent changes the indentation used when encoding. It takes effect for the
// first Encode only, as the layout is fixed once writing has started.
func (e *Encoder) SetIndent(spaces int) {
	if spaces < 0 {
		panic("yaml: cannot indent to a negative number of spaces")
	}
	e.indent = spaces
}

// CompactSeqIndent makes it so that '- ' is considered part of the indentation.
func (e *Encoder) CompactSeqIndent() { e.compact = true }

// DefaultSeqIndent makes it so that '- ' is not considered part of the
// indentation.
func (e *Encoder) DefaultSeqIndent() { e.compact = false }

// Close closes the encoder by writing any remaining data. It does not write a
// stream terminating string "...".
func (e *Encoder) Close() error {
	e.closed = true
	if e.enc != nil {
		return e.enc.Close()
	}
	return nil
}

// convertError turns the core's errors into yaml.v3's: a *TypeError for
// mismatches (all collected messages), the hook's own error as it was returned,
// and "yaml: ..." errors for the rest. Limit errors are returned as they are.
func convertError(err error) error {
	if err == nil {
		return nil
	}
	var col *compatopt.Collected
	if errors.As(err, &col) {
		msgs := make([]string, len(col.Items))
		for i, it := range col.Items {
			if it.Verbatim {
				msgs[i] = it.Msg
			} else {
				msgs[i] = fmt.Sprintf("line %d: %s", it.Line, it.Msg)
			}
		}
		return &TypeError{Errors: msgs}
	}
	var se *oneyaml.SyntaxError
	if errors.As(err, &se) {
		if se.Category == oneyaml.CategoryDuplicateKey {
			if se.FirstLine > 0 && se.Key != "" {
				// yaml.v3 words it with the line of the first key only.
				return &TypeError{Errors: []string{fmt.Sprintf("line %d: mapping key %q already defined at line %d", se.Line, se.Key, se.FirstLine)}}
			}
			return &TypeError{Errors: []string{fmt.Sprintf("line %d: %s", se.Line, se.Msg)}}
		}
		return fmt.Errorf("yaml: line %d: %s", se.Line, se.Msg)
	}
	var le *oneyaml.LimitError
	if errors.As(err, &le) {
		return err
	}
	var te *oneyaml.TypeError
	if errors.As(err, &te) {
		if te.Err != nil {
			return te.Err
		}
		return errors.New("yaml: " + te.Msg)
	}
	return err
}

func encodeBase64(s string) string {
	const lineLen = 70
	enc := base64.StdEncoding.EncodeToString([]byte(s))
	if len(enc) < lineLen {
		return enc
	}
	var b strings.Builder
	for i := 0; i < len(enc); i += lineLen {
		j := min(i+lineLen, len(enc))
		b.WriteString(enc[i:j])
		b.WriteByte('\n')
	}
	return b.String()
}
