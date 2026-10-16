package oneyaml

import (
	"errors"
	"io"
	"sync"

	"oneyaml.dev/go/internal/bind"
	"oneyaml.dev/go/internal/emit"
)

// Marshaler is implemented by types that choose their own YAML form. The value
// MarshalYAML returns is written in place of the receiver; it may be any value
// Marshal accepts, including another Marshaler.
type Marshaler interface {
	MarshalYAML() (any, error)
}

// poolLimit is the largest buffer kept for reuse; bigger ones are left to the
// collector so that one huge document does not pin memory.
const poolLimit = 1 << 20

var encoderPool = sync.Pool{New: func() any { return emit.New(emit.Config{}) }}

// Marshal returns v as a YAML document.
//
// Output is block style with a two-space indent, ends with a newline and has no
// document marker. Strings are written plain only when every common reader would
// read them back as the same string; "yes", "0755" and "1:30" are quoted. Map
// keys are sorted and struct fields follow declaration order, so the same value
// always gives the same bytes.
//
// Struct fields are named by the yaml tag, or by the json tag when there is no
// yaml tag, or by the lower-cased field name. The tag options are omitempty,
// omitzero, inline and flow. A nil map, slice, pointer or interface is written
// as null.
//
// Types may implement Marshaler, encoding.TextMarshaler or json.Marshaler, tried
// in that order. The JSON a json.Marshaler returns is written as the equivalent
// YAML value (a JSON string as a plain string where it is safe, an object as a
// mapping with sorted keys). A type with a MarshalYAML method that is not
// MarshalYAML() (any, error) is a *TypeError. A Node is written as its value.
// A struct field tagged omitempty is left out when it is a struct whose fields
// are all zero, or whose IsZero method returns true.
//
// time.Time is written as an RFC 3339 timestamp, time.Duration as its String form, *big.Int as an
// integer, and []byte as a !!binary scalar. A string that is not valid UTF-8 is
// also written as !!binary, because no other form keeps its bytes; some readers
// (goccy) return such a scalar as []byte rather than a string.
//
// Marshal reports a *TypeError for a value with no YAML form (channels,
// functions, complex numbers) and a *LimitError when nesting or a cycle exceeds
// the depth limit.
func Marshal(v any, opts ...Option) ([]byte, error) {
	cfg := newConfig(opts)
	e := encoderPool.Get().(*emit.Encoder)
	e.Configure(cfg.emitConfig())
	e.Reset()
	err := e.Document(v)
	var out []byte
	if err == nil {
		out = append([]byte(nil), e.Bytes()...)
	}
	if cap(e.Bytes()) <= poolLimit {
		encoderPool.Put(e)
	}
	if err != nil {
		return nil, convertEmitError(err)
	}
	return out, nil
}

// convertEmitError turns the encoder's internal error into the public types.
func convertEmitError(err error) error {
	var ee *emit.Error
	if !errors.As(err, &ee) {
		return err
	}
	// A depth or cycle error would repeat the same path thousands of times.
	if ee.Kind == emit.KindDepth {
		return limitErr(LimitMaxDepth, "depth", ee.Max, "")
	}
	if ee.Kind == emit.KindNumberDigits {
		return limitErr(LimitMaxNumberDigits, "number digits", ee.Max, ee.PathString())
	}
	te := &TypeError{Msg: ee.Detail, Err: ee.Err}
	if ee.Kind != emit.KindCycle {
		te.Path = ee.PathString()
	}
	if ee.Type != nil {
		te.GoType = ee.Type
	}
	return te
}

// An Encoder writes YAML documents to an output stream.
type Encoder struct {
	w      io.Writer
	enc    *emit.Encoder
	cfg    config
	n      int
	closed bool
}

// NewEncoder returns an Encoder that writes to w.
func NewEncoder(w io.Writer, opts ...Option) *Encoder {
	cfg := newConfig(opts)
	e := emit.New(cfg.emitConfig())
	return &Encoder{w: w, enc: e, cfg: cfg}
}

// Encode writes v as the next document of the stream. Documents after the first
// are preceded by a "---" line. Each document is written to the underlying
// writer with a single Write call, and nothing is written if v cannot be
// encoded.
func (e *Encoder) Encode(v any) error {
	if e.closed {
		return errors.New("oneyaml: Encode after Close")
	}
	e.enc.Reset()
	if e.n > 0 {
		e.enc.Buffer(append(e.enc.Bytes(), "---\n"...))
	}
	if err := e.enc.Document(v); err != nil {
		return convertEmitError(err)
	}
	e.n++
	_, err := e.w.Write(e.enc.Bytes())
	return err
}

// Close ends the stream. It does not close the underlying writer. Encode returns
// an error after Close.
func (e *Encoder) Close() error {
	e.closed = true
	return nil
}

// emitConfig returns the encoder settings for cfg.
func (c *config) emitConfig() emit.Config {
	ec := emit.Config{Indent: c.indent, MaxDepth: c.limits.MaxDepth, MaxNumberDigits: c.limits.MaxNumberDigits, CompactSeq: c.compactSeq}
	if c.dialect == bind.DialectV3 {
		ec.Dialect = emit.DialectV3
	}
	return ec
}
