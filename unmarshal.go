package oneyaml

import (
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"unicode/utf8"

	"oneyaml.dev/go/internal/bind"
	"oneyaml.dev/go/internal/encoding"
	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/scan"
)

// session is a parser and a binder wired together. Sessions are pooled so a
// call to Unmarshal reuses the buffers of the one before it.
type session struct {
	p *parse.Parser
	b *bind.Binder
}

var sessionPool = sync.Pool{New: func() any {
	return &session{p: parse.New(nil, 0), b: bind.New(bind.Config{})}
}}

func bindConfig(cfg *config, inputLen int) bind.Config {
	l := cfg.limits
	return bind.Config{
		KnownFields: cfg.knownFields, MergeKeys: cfg.mergeKeys,
		MaxDepth: l.MaxDepth, MaxAliasNodes: l.MaxAliasNodes, MaxAliasBytes: l.MaxAliasBytes,
		AliasRatio: cfg.aliasRatio, ExactNodes: cfg.exactNodes, ExactBytes: cfg.exactBytes, InputBytes: inputLen, MaxScalarBytes: l.MaxScalarBytes, MaxNumberDigits: l.MaxNumberDigits,
		Dialect: cfg.dialect, Spend: cfg.spend,
	}
}

// parserDepth maps the depth limit to the parser's convention: negative turns
// the check off.
func parserDepth(cfg *config) int {
	if cfg.limits.MaxDepth <= 0 {
		return -1
	}
	return cfg.limits.MaxDepth
}

func (s *session) reset(src []byte, cfg *config) {
	s.p.Reset(src, parserDepth(cfg))
	s.b.Reset(s.p, bindConfig(cfg, len(src)))
}

func (s *session) release() {
	s.b.Release()
	s.p.Reset(nil, 0)
}

// Unmarshal reads the first YAML document in data into the value v points to.
//
// v must be a non-nil pointer. Structs are filled by key (see the tag rules on
// Marshal); maps, slices, arrays, pointers, interfaces and the scalar kinds are
// filled by shape. A plain scalar is read with the YAML 1.2 Core schema, so
// "yes" and "0755" are strings. A quoted scalar is always a string, and an
// explicit tag such as !!int or !!str overrides the resolution. A null value
// leaves a struct field, number or string as it was and clears a pointer, map,
// slice or interface.
//
// Into an interface{} a mapping becomes map[string]any, or map[any]any when a
// key is not a string; a sequence becomes []any; an integer becomes int, or
// uint64 or *big.Int when it does not fit; a float becomes float64; null
// becomes nil; and everything else is a string, timestamps and !!binary text
// included. A tag the Core schema does not resolve is ignored.
//
// Types may read themselves. The hooks are tried in this order: Unmarshaler
// (UnmarshalYAML with a Node), the older form UnmarshalYAML(func(any) error)
// error that yaml.v2, yaml.v3 and goccy use, encoding.TextUnmarshaler for a
// scalar, then json.Unmarshaler. A json.Unmarshaler is given the value as JSON
// text, so types written for encoding/json (Kubernetes quantities, say) need no
// YAML code; a value JSON cannot hold, such as a mapping with a key that is not a
// string or .nan, is a *TypeError. A plain number is passed with the digits as
// written, so 12345678901234567890.5 reaches UnmarshalJSON unrounded; inside a
// mapping or sequence, integers keep every digit and floats are rounded to
// float64. A type with an UnmarshalYAML method of any other form, yaml.v3's
// UnmarshalYAML(*yaml.Node) error for one, is a *TypeError and is not filled by
// its fields. A null value clears a pointer target without
// calling any hook; of the other targets, only an Unmarshaler is called for a
// null value.
// time.Time reads a timestamp, time.Duration its String form, *big.Int any
// integer, and []byte a !!binary scalar (or the text of any other scalar).
//
// A Node is a view into a Document and is not a target: decoding into one is a
// *TypeError. Read it with Document.Get(pointer, &node).
//
// A mapping key that occurs twice is an error, judged after resolution, so 1 and
// 0x1 clash, and so do 1e400 and 10e399, which are the same number. Two keys
// that are different numbers but round to the same float64 (1e-400 and 2e-400,
// or 1e400 and .inf) are a *TypeError that says so, since a map cannot hold
// them apart; a key repeated after an entry that did not fit is still a
// duplicate. Merge keys are ordinary keys unless the
// MergeKeys option is given. Unknown struct keys are skipped unless KnownFields
// is given.
//
// Aliases are decoded by replaying the anchored node; the alias limits bound
// how much that may produce. If data holds more than one document only the
// first is read; if it holds none, v is left unchanged and the error is nil.
//
// The error is a *SyntaxError for malformed input, a *TypeError for values that
// do not fit their targets and a *LimitError when a limit is exceeded. A value
// that does not fit is skipped and its target keeps what it held, and the rest
// of the document is still read; the *TypeError describes the first mismatch and
// lists the others in its More field. A nil pointer target stays nil when its
// value does not fit. At most 100 mismatches are collected; the read stops at
// the 100th. A syntax error, a limit error or an error from a hook of your own
// stops the read at once, and the error returned is that error alone: the
// mismatches collected before it are dropped. UTF-16 and UTF-32 input, with or without a
// byte order mark, is converted to UTF-8 first; the offsets of errors in such
// input index the UTF-8 form.
func Unmarshal(data []byte, v any, opts ...Option) error {
	cfg := newConfig(opts)
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &TypeError{GoType: goTypeName(v), Msg: "Unmarshal needs a non-nil pointer, got " + goTypeName(v)}
	}
	if max := cfg.limits.MaxInputBytes; max > 0 && len(data) > max {
		return &LimitError{Limit: "input size", Max: max, Option: "Limits"}
	}
	src, _, err := encoding.Convert(data)
	if err != nil {
		return convertError(err, src, &cfg, 0, 0)
	}
	s := sessionPool.Get().(*session)
	s.reset(src, &cfg)
	ok, err := s.b.NextDocument()
	if err == nil && ok {
		if ap, isAny := v.(*any); isAny {
			var x any
			if x, err = s.b.DecodeAny(); err == nil {
				*ap = x
			}
		} else {
			err = s.b.Decode(rv.Elem())
		}
		if err == nil && cfg.dialect != bind.DialectCore {
			err = collectedErr(s.b, src, 0)
		}
	}
	if err != nil {
		err = convertError(err, src, &cfg, 0, 0)
	}
	s.release()
	sessionPool.Put(s)
	return err
}

func goTypeName(v any) string {
	if v == nil {
		return "nil"
	}
	return reflect.TypeOf(v).String()
}

// convertError turns an internal error into the public error types. lineBase and
// offBase place a segment of a larger stream: they are the lines and bytes that
// came before it.
func convertError(err error, src []byte, cfg *config, lineBase, offBase int) error {
	switch e := err.(type) {
	case *scan.Error:
		if e.Category == scan.Limit {
			return &LimitError{Limit: "depth", Max: cfg.limits.MaxDepth, Option: "Limits"}
		}
		return &SyntaxError{Line: e.Line + lineBase, Column: e.Column, Offset: e.Offset + offBase,
			Category: e.Category.String(), Msg: e.Msg}
	case *bind.Error:
		switch e.Kind {
		case bind.KindLimit:
			return &LimitError{Limit: e.Limit, Max: e.Max, Option: e.Option, Path: e.Path}
		case bind.KindSyntax:
			if e.Category == CategoryDuplicateKey {
				// Keys written differently that read as one float are not duplicates.
				if _, _, ok := bind.FirstKey(src, e.Offset); !ok {
					if first, ok := bind.EarlierRounding(src, e.Offset); ok {
						return floatKeyError(&bind.Error{Kind: bind.KindType, Offset: e.Offset, First: first, GoType: "float64", Category: "float-key"}, src, lineBase, offBase)
					}
				}
			}
			return syntaxFromBind(e, src, lineBase, offBase)
		}
		if e.Category == "float-key" {
			return floatKeyError(e, src, lineBase, offBase)
		}
		return typeErrors(e, src, lineBase)
	}
	return err
}

// floatKeyError reports two keys that read as the same float64. Written as the
// same number they are an ordinary duplicate; as different numbers they are a
// conversion limit of the target.
func floatKeyError(e *bind.Error, src []byte, lineBase, offBase int) error {
	earlier, ok1 := bind.KeyText(src, e.First)
	later, ok2 := bind.KeyText(src, e.Offset)
	if !ok1 || !ok2 || sameDecimal(earlier, later) {
		return sameValueKeys(e, earlier, later, src, lineBase, offBase)
	}
	return typeErrors(e, src, lineBase)
}

// syntaxFromBind builds the SyntaxError for a binding failure. A duplicate key
// names the key and says where it first appeared.
func syntaxFromBind(e *bind.Error, src []byte, lineBase, offBase int) *SyntaxError {
	pos := scan.NewError(src, e.Offset, scan.Syntax, "")
	se := &SyntaxError{Line: pos.Line + lineBase, Column: pos.Column, Offset: e.Offset + offBase,
		Category: e.Category, Msg: e.Msg}
	if e.Category != CategoryDuplicateKey {
		return se
	}
	se.Msg = "a mapping key is repeated: it equals an earlier key once converted to the target type"
	if key, first, ok := bind.FirstKey(src, e.Offset); ok {
		fp := scan.NewError(src, first, scan.Syntax, "")
		se.Key, se.FirstLine, se.FirstColumn = key, fp.Line+lineBase, fp.Column
		se.Msg = fmt.Sprintf("mapping key %s already defined at line %d, column %d", quoteKey(key), se.FirstLine, se.FirstColumn)
	}
	return se
}

// quoteKey quotes a key for a message, shortening a long one.
func quoteKey(k string) string {
	const max = 40
	if len(k) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(k[cut]) {
			cut--
		}
		return strconv.Quote(k[:cut]) + "..."
	}
	return strconv.Quote(k)
}

// typeErrors builds the TypeError for a binding failure. An aggregate becomes one
// TypeError for the first mismatch with the rest in More.
// pathBudgetMin is the least number of bytes of path text kept for the mismatches
// of one read. The budget is the larger of this and the size of the input, so
// the paths cannot cost more than the input did: a deep document with many bad
// values reports as many of them as fit and always reports the first.
const pathBudgetMin = 1 << 20

func typeErrors(e *bind.Error, src []byte, lineBase int) *TypeError {
	if e.Multi == nil {
		return typeError(e, e.FullPath(), src, lineBase)
	}
	all := e.Flatten(max(len(src), pathBudgetMin))
	out := typeError(all[0].Err, all[0].Path, src, lineBase)
	for _, m := range all[1:] {
		out.More = append(out.More, typeError(m.Err, m.Path, src, lineBase))
	}
	return out
}

func typeError(e *bind.Error, path string, src []byte, lineBase int) *TypeError {
	pos := scan.NewError(src, e.Offset, scan.Syntax, "")
	te := &TypeError{Path: path, Line: pos.Line + lineBase, Column: pos.Column, GoType: e.GoType, Msg: e.Msg, Err: e.Err}
	if e.Unknown {
		te.Kind = ErrUnknownField
	}
	if inner, ok := e.Err.(*TypeError); ok {
		// A hook returned the error of a decode it made itself: report that
		// error once, at the position of the value the hook was given.
		te.Path, te.GoType, te.Msg, te.Err = path+inner.Path, inner.GoType, inner.Msg, inner.Err
	}
	if e.Category == "float-key" {
		fp := scan.NewError(src, e.First, scan.Syntax, "")
		te.Msg = fmt.Sprintf("the keys at line %d, column %d and line %d, column %d are different numbers that round to the same float64, which a map cannot hold apart; quote them to read them as strings",
			fp.Line+lineBase, fp.Column, te.Line, te.Column)
	}
	return te
}

// sameValueKeys builds the duplicate-key error for two keys that are spelled
// differently but are the same number.
func sameValueKeys(e *bind.Error, earlier, later string, src []byte, lineBase, offBase int) *SyntaxError {
	pos := scan.NewError(src, e.Offset, scan.Syntax, "")
	fp := scan.NewError(src, e.First, scan.Syntax, "")
	se := &SyntaxError{Line: pos.Line + lineBase, Column: pos.Column, Offset: e.Offset + offBase, Category: CategoryDuplicateKey,
		Key: later, FirstLine: fp.Line + lineBase, FirstColumn: fp.Column}
	se.Msg = fmt.Sprintf("mapping key %s has the same value as the key %s at line %d, column %d",
		quoteKey(later), quoteKey(earlier), se.FirstLine, se.FirstColumn)
	return se
}
