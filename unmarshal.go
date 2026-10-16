package oneyaml

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
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
// plain yes is a string and plain 0755 is the integer 755. A quoted scalar, such
// as "0755", is always a string, and an explicit tag such as !!int or !!str
// overrides the resolution. A null value
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
// string or .nan, is a *TypeError. Numbers are passed with the digits as
// written, on their own or inside a mapping or sequence, so
// 12345678901234567890.5 reaches UnmarshalJSON unrounded; a form JSON cannot
// spell, such as 0x1F or .5, is rewritten as a JSON number. A type with an UnmarshalYAML method of any other form, yaml.v3's
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
// 0x1 clash, and so do 1e400 and 10e399, or 1e5 and 1e00005, which are the same
// number. Two keys that are different numbers but round to the same float64
// (1e-400 and 2e-400, or 1e400 and .inf) are a *TypeError that says so, since a
// map cannot hold them apart. A key is a duplicate even when one of its entries
// did not fit its target, so a: 1 followed by a: x is a duplicate-key error. All
// .nan keys are one key, except in a map with string keys, whose keys are
// compared as text. A key that no field of a struct target has counts too, as do
// the keys inside a value the target ignores, so a: 1 twice is refused even
// when the struct has no field a. A repeated key written as an alias is
// reported at the alias. Merge keys are ordinary keys unless the MergeKeys
// option is given. Unknown struct keys are skipped unless KnownFields is given.
//
// A \u or \U escape in a double-quoted scalar must name a Unicode scalar
// value. An escaped UTF-16 surrogate pair, as JSON writes a character past
// U+FFFF, is one character ("\ud83d\ude00" is U+1F600); a lone or mismatched
// surrogate, or a value past U+10FFFF, is a *SyntaxError.
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
// the 100th, and fewer are reported when their paths are very long (see
// TypeError). A syntax error, a limit error or an error from a hook of your own
// stops the read at once, and the error returned is that error alone: the
// mismatches collected before it are dropped. A syntax error after the value,
// such as a stray closing bracket, is reported even when the value did not fit. UTF-16 and UTF-32 input, with or
// without a byte order mark, is converted to UTF-8 first; the offsets of errors
// in such input index the UTF-8 form.
func Unmarshal(data []byte, v any, opts ...Option) error {
	cfg := newConfig(opts)
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &TypeError{GoType: reflect.TypeOf(v), Msg: "Unmarshal needs a non-nil pointer, got " + goTypeName(v)}
	}
	if max := cfg.limits.MaxInputBytes; max > 0 && len(data) > max {
		return limitErr(LimitMaxInputBytes, "input size", max, "")
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
			return limitErr(LimitMaxDepth, "depth", cfg.limits.MaxDepth, "")
		}
		return &SyntaxError{Line: e.Line + lineBase, Column: e.Column, Offset: e.Offset + offBase,
			Category: Category(e.Category.String()), Msg: e.Msg}
	case *bind.Error:
		switch e.Kind {
		case bind.KindLimit:
			if e.Fixed {
				// No option lifts this bound, so none is suggested.
				le := fixedLimitErr(Limit(e.Limit), e.What, e.Max)
				le.Path = e.FullPath()
				return le
			}
			return limitErr(Limit(e.Limit), e.What, e.Max, e.FullPath())
		case bind.KindSyntax:
			if Category(e.Category) == CategoryDuplicateKey {
				// Keys written differently that read as one float are not duplicates.
				if _, _, ok := bind.FirstKey(src, e.Offset); !ok {
					if first, ok := bind.EarlierRounding(src, e.Offset); ok {
						return floatKeyError(&bind.Error{Kind: bind.KindType, Offset: e.Offset, First: first, GoType: reflect.TypeFor[float64](), Category: "float-key"}, src, lineBase, offBase, cfg.spend)
					}
					// Spelled differently, read alike: 1 and 0x1, null and ~.
					if first, ok := bind.EarlierSameValue(src, e.Offset); ok {
						earlier, _ := bind.KeyText(src, first)
						later, _ := bind.KeyText(src, e.Offset)
						return sameValueKeys(&bind.Error{Offset: e.Offset, First: first}, earlier, later, src, lineBase, offBase)
					}
				}
			}
			return syntaxFromBind(e, src, lineBase, offBase)
		}
		if e.Category == "float-key" {
			return floatKeyError(e, src, lineBase, offBase, cfg.spend)
		}
		return typeErrors(e, src, lineBase, cfg.spend)
	}
	return err
}

// floatKeyError reports two keys that read as the same float64. Written as the
// same number they are an ordinary duplicate; as different numbers they are a
// conversion limit of the target.
func floatKeyError(e *bind.Error, src []byte, lineBase, offBase int, sp *bind.AliasSpend) error {
	earlier, ok1 := bind.KeyText(src, e.First)
	later, ok2 := bind.KeyText(src, e.Offset)
	if !ok1 || !ok2 || sameDecimal(earlier, later) {
		return sameValueKeys(e, earlier, later, src, lineBase, offBase)
	}
	return typeErrors(e, src, lineBase, sp)
}

// syntaxFromBind builds the SyntaxError for a binding failure. A duplicate key
// names the key and says where it first appeared.
func syntaxFromBind(e *bind.Error, src []byte, lineBase, offBase int) *SyntaxError {
	pos := scan.NewError(src, e.Offset, scan.Syntax, "")
	se := &SyntaxError{Line: pos.Line + lineBase, Column: pos.Column, Offset: e.Offset + offBase,
		Category: Category(e.Category), Msg: e.Msg}
	if Category(e.Category) != CategoryDuplicateKey {
		return se
	}
	se.Msg = "a mapping key is repeated: it equals an earlier key once converted to the target type"
	if e.Offset >= 0 && e.Offset < len(src) && src[e.Offset] == '*' {
		// The key is an alias; the error is at the alias, not at the node it names.
		end := e.Offset + 1
		for end < len(src) && !isAliasEnd(src[end]) {
			end++
		}
		se.Msg = "the alias " + quoteKey(string(src[e.Offset:end])) + " used as a mapping key repeats an earlier key"
		return se
	}
	if key, first, ok := bind.FirstKey(src, e.Offset); ok {
		fp := scan.NewError(src, first, scan.Syntax, "")
		se.Key, se.FirstLine, se.FirstColumn = key, fp.Line+lineBase, fp.Column
		se.Msg = fmt.Sprintf("mapping key %s already defined at line %d, column %d", quoteKey(key), se.FirstLine, se.FirstColumn)
	}
	return se
}

// isAliasEnd reports whether c ends the name of an alias.
func isAliasEnd(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ',', '[', ']', '{', '}':
		return true
	}
	return false
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

// pathBudgetMin is the least number of bytes of path text kept for the mismatches
// of one read. The budget is the larger of this and the size of the input, so
// the paths cannot cost more than the input did: a deep document with many bad
// values reports as many of them as fit and always reports the first.
const pathBudgetMin = 1 << 20

// pathLink is a part of a path that has not been joined yet. A chain starts at
// the outermost part; n is the length of this part and all after it.
type pathLink struct {
	seg  string
	next *pathLink
	n    int
}

// joinLinks returns the text of a chain followed by rest.
func joinLinks(l *pathLink, rest string) string {
	var b strings.Builder
	b.Grow(l.n + len(rest))
	for ; l != nil; l = l.next {
		b.WriteString(l.seg)
	}
	b.WriteString(rest)
	return b.String()
}

// pathBudget is the path text that the mismatches after the first may still take.
type pathBudget struct {
	left *int
	// inner is set for the decode of a hook's Node, whose paths the read around
	// the hook counts again in full: it charges only what it adds in front.
	inner bool
}

func (b pathBudget) take(n int) bool {
	if n > *b.left {
		return false
	}
	*b.left -= n
	return true
}

// typeErrors builds the TypeError for a binding failure. An aggregate becomes one
// TypeError for the first mismatch with the rest in More. sp is the budget shared with the reads made
// from the Nodes of an enclosing hook, nil for an outermost read: the paths of
// all the decodes of one read then take no more than one budget between them.
func typeErrors(e *bind.Error, src []byte, lineBase int, sp *bind.AliasSpend) *TypeError {
	budget := max(len(src), pathBudgetMin)
	var bud pathBudget
	if sp != nil {
		bud = pathBudget{left: sp.PathBudget(budget), inner: true}
	} else {
		bud = pathBudget{left: &budget}
	}
	var out []*TypeError
	if e.Multi == nil {
		out = typeErrorList(nil, e, e.FullPath(), src, lineBase, bud)
	} else {
		for _, m := range e.Flatten(*bud.left) {
			out = typeErrorList(out, m.Err, m.Path, src, lineBase, bud)
			if len(out) >= maxTypeErrors {
				break
			}
		}
	}
	if sp == nil {
		// The paths kept in parts are joined now, once. They were counted against
		// the budget when they were made.
		for _, te := range out {
			if te.pre != nil {
				te.Path, te.pre = joinLinks(te.pre, te.Path), nil
			}
		}
	}
	return joinTypeErrors(out)
}

// maxTypeErrors is the most mismatches one read reports.
const maxTypeErrors = 100

// joinTypeErrors makes the first error the head of the list, with the others in More.
func joinTypeErrors(list []*TypeError) *TypeError {
	out := list[0]
	if len(list) > 1 {
		out.More = list[1:len(list):len(list)]
	}
	return out
}

// typeErrorList appends the TypeErrors for e, found at path, to out. Most
// errors give one. A hook that returned the error of a decode it made from its
// own Node (Node.Decode from inside UnmarshalYAML) gives the errors of that
// decode, each at the position of its own value and with the hook's path in
// front, so the position is where the bad value is in the input, as it is
// without a hook. The first error of a read is always kept whole; the others
// are kept while their paths fit the budget, and the list ends at the first that
// does not.
func typeErrorList(out []*TypeError, e *bind.Error, path string, src []byte, lineBase int, bud pathBudget) []*TypeError {
	inner, ok := e.Err.(*TypeError)
	if !ok || !e.Own {
		te := typeError(e, path, src, lineBase)
		if absoluteLimit(e.Err, path+e.Rel, bud.inner) {
			te.Msg = unprefix(e.Err.Error())
		}
		if len(out) > 0 && !bud.take(len(te.Path)) {
			return out
		}
		return append(out, te)
	}
	prefix := path + e.Rel
	for _, x := range inner.All() {
		if len(out) >= maxTypeErrors {
			break
		}
		n := len(prefix) + len(x.Path)
		if x.pre != nil {
			n += x.pre.n
		}
		cost := n
		if bud.inner {
			cost = len(prefix)
		}
		if len(out) > 0 && !bud.take(cost) {
			break
		}
		c := *x
		c.More, c.sc, c.from = nil, nil, 0
		if absoluteLimit(c.Err, prefix, bud.inner) {
			c.Msg = unprefix(c.Err.Error())
		}
		if x.pre == nil && !bud.inner {
			c.Path = prefix + x.Path
		} else {
			c.pre = &pathLink{seg: prefix, next: x.pre, n: n - len(x.Path)}
		}
		if c.Line > 0 {
			c.Line += lineBase
		}
		out = append(out, &c)
	}
	return out
}

// absoluteLimit finds the *LimitError that err is or wraps, if it is the error
// of a decode made in a hook and still relative to that decode's node, and puts
// prefix, the path of the decoded node, in front of its path. It changes the
// error in place because a hook that wraps the error (fmt.Errorf with %w) hands
// back a chain that cannot be copied. Inside another hook the path is still
// relative to that hook's node, so the error stays marked when inner is set.
// It reports whether it rewrote a path.
func absoluteLimit(err error, prefix string, inner bool) bool {
	var le *LimitError
	if !errors.As(err, &le) || !le.rel {
		return false
	}
	le.Path = prefix + le.Path
	le.rel, le.sc, le.from = inner, nil, 0
	return true
}

func typeError(e *bind.Error, path string, src []byte, lineBase int) *TypeError {
	pos := scan.NewError(src, e.Offset, scan.Syntax, "")
	te := &TypeError{Path: path, Line: pos.Line + lineBase, Column: pos.Column, GoType: e.GoType, Msg: e.Msg, Err: e.Err}
	if e.Unknown {
		te.Kind = ErrUnknownField
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
