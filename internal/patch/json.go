// Package patch holds the parts of RFC 6902 and RFC 7396 patches that do not
// depend on the document: reading a JSON patch, comparing a node with a patch
// value under the data model, and copying a value so that it can be written
// elsewhere in the same document.
package patch

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"oneyaml.dev/go/internal/edit"
)

// ErrSyntax means a patch is not well formed: invalid JSON, or JSON that is not a
// patch.
var ErrSyntax = errors.New("invalid patch")

// ErrDepth means the patch nests deeper than the allowed depth.
var ErrDepth = errors.New("patch nested too deeply")

// Op is one operation of a patch.
type Op struct {
	Name string // add, remove, replace, move, copy or test
	Path string
	From string
	// Value is the operand of add, replace and test.
	Value edit.Value
}

type frame struct {
	v    edit.Value
	seen map[string]struct{} // member names of a large object
}

type jparser struct {
	b        []byte
	i        int
	maxDepth int
}

func (p *jparser) fail(format string, a ...any) error {
	return fmt.Errorf("%w: JSON at offset %d: %s", ErrSyntax, p.i, fmt.Sprintf(format, a...))
}

func (p *jparser) ws() {
	for p.i < len(p.b) {
		switch p.b[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

// ParseValue reads one JSON value, keeping the order of object members. Nesting
// is tracked with an explicit stack, so hostile depth cannot exhaust the goroutine
// stack; maxDepth (and never more than edit.MaxValueDepth) bounds it. An object
// that names a member twice is refused.
func ParseValue(data []byte, maxDepth int) (edit.Value, error) {
	if maxDepth <= 0 || maxDepth > edit.MaxValueDepth {
		maxDepth = edit.MaxValueDepth
	}
	// JSON text is UTF-8 (RFC 8259, section 8.1). Refuse anything else up front: a
	// reader that substituted U+FFFD would change member names silently and could
	// make two different names collide.
	if !utf8.Valid(data) {
		i := 0
		for i < len(data) {
			r, n := utf8.DecodeRune(data[i:])
			if r == utf8.RuneError && n <= 1 {
				break
			}
			i += n
		}
		return edit.Value{}, fmt.Errorf("%w: JSON at offset %d: invalid UTF-8", ErrSyntax, i)
	}
	p := &jparser{b: data, maxDepth: maxDepth}
	var stack []frame
	for {
		// Read the start of a value (or the key and value of an object member).
		p.ws()
		if p.i >= len(p.b) {
			return edit.Value{}, p.fail("unexpected end of input")
		}
		var val edit.Value
		complete := false
		if n := len(stack); n > 0 && stack[n-1].v.Kind == edit.Map && len(stack[n-1].v.Items)%2 == 0 {
			// A member name comes first.
			if p.b[p.i] != '"' {
				return edit.Value{}, p.fail("object member name must be a string")
			}
			k, err := p.str()
			if err != nil {
				return edit.Value{}, err
			}
			fr := &stack[n-1]
			if fr.seen != nil {
				if _, dup := fr.seen[k]; dup {
					return edit.Value{}, p.fail("duplicate object member %q", k)
				}
				fr.seen[k] = struct{}{}
			} else {
				for j := 0; j < len(fr.v.Items); j += 2 {
					if fr.v.Items[j].Text == k {
						return edit.Value{}, p.fail("duplicate object member %q", k)
					}
				}
				if len(fr.v.Items) >= 32 {
					fr.seen = make(map[string]struct{}, 64)
					for j := 0; j < len(fr.v.Items); j += 2 {
						fr.seen[fr.v.Items[j].Text] = struct{}{}
					}
					fr.seen[k] = struct{}{}
				}
			}
			fr.v.Items = append(fr.v.Items, edit.Str(k))
			p.ws()
			if p.i >= len(p.b) || p.b[p.i] != ':' {
				return edit.Value{}, p.fail("expected ':'")
			}
			p.i++
			p.ws()
			if p.i >= len(p.b) {
				return edit.Value{}, p.fail("unexpected end of input")
			}
		}
		switch c := p.b[p.i]; {
		case c == '{' || c == '[':
			if len(stack) >= p.maxDepth {
				return edit.Value{}, fmt.Errorf("%w: more than %d levels", ErrDepth, p.maxDepth)
			}
			p.i++
			k := edit.Seq
			closer := byte(']')
			if c == '{' {
				k, closer = edit.Map, '}'
			}
			p.ws()
			if p.i < len(p.b) && p.b[p.i] == closer {
				p.i++
				val, complete = edit.Value{Kind: k}, true
			} else {
				stack = append(stack, frame{v: edit.Value{Kind: k}})
				continue
			}
		case c == '"':
			s, err := p.str()
			if err != nil {
				return edit.Value{}, err
			}
			val, complete = edit.Str(s), true
		default:
			v, err := p.scalar()
			if err != nil {
				return edit.Value{}, err
			}
			val, complete = v, true
		}
		// A value is complete: attach it, closing containers as they end.
		for complete {
			n := len(stack)
			if n == 0 {
				p.ws()
				if p.i != len(p.b) {
					return edit.Value{}, p.fail("trailing data after the value")
				}
				return val, nil
			}
			top := &stack[n-1]
			top.v.Items = append(top.v.Items, val)
			p.ws()
			if p.i >= len(p.b) {
				return edit.Value{}, p.fail("unexpected end of input")
			}
			closer := byte(']')
			if top.v.Kind == edit.Map {
				closer = '}'
			}
			switch p.b[p.i] {
			case ',':
				p.i++
				complete = false
			case closer:
				p.i++
				val = top.v
				stack = stack[:n-1]
			default:
				return edit.Value{}, p.fail("expected ',' or '%c'", closer)
			}
		}
	}
}

func (p *jparser) scalar() (edit.Value, error) {
	b := p.b
	start := p.i
	switch {
	case hasWord(b, p.i, "true"):
		p.i += 4
		return edit.Value{Kind: edit.Bool, Text: "true"}, nil
	case hasWord(b, p.i, "false"):
		p.i += 5
		return edit.Value{Kind: edit.Bool, Text: "false"}, nil
	case hasWord(b, p.i, "null"):
		p.i += 4
		return edit.Value{Kind: edit.Null, Text: "null"}, nil
	}
	i := p.i
	if i < len(b) && b[i] == '-' {
		i++
	}
	switch {
	case i < len(b) && b[i] == '0':
		i++
	case i < len(b) && b[i] >= '1' && b[i] <= '9':
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
	default:
		return edit.Value{}, p.fail("unexpected character %q", b[start])
	}
	kind := edit.Int
	if i < len(b) && b[i] == '.' {
		kind = edit.Float
		i++
		d := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
		if i == d {
			p.i = i
			return edit.Value{}, p.fail("digits expected after '.'")
		}
	}
	if i < len(b) && (b[i] == 'e' || b[i] == 'E') {
		kind = edit.Float
		i++
		if i < len(b) && (b[i] == '+' || b[i] == '-') {
			i++
		}
		d := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
		if i == d {
			p.i = i
			return edit.Value{}, p.fail("digits expected in the exponent")
		}
	}
	p.i = i
	return edit.Value{Kind: kind, Text: string(b[start:i])}, nil
}

func hasWord(b []byte, i int, w string) bool {
	return len(b)-i >= len(w) && string(b[i:i+len(w)]) == w
}

// str reads a JSON string; p.i is at the opening quote.
func (p *jparser) str() (string, error) {
	b := p.b
	p.i++
	start := p.i
	// Fast path: no escapes.
	for p.i < len(b) {
		c := b[p.i]
		if c == '"' {
			s := b[start:p.i]
			if !utf8.Valid(s) {
				return "", p.fail("invalid UTF-8 in a string")
			}
			p.i++
			return string(s), nil
		}
		if c == '\\' {
			break
		}
		if c < 0x20 {
			return "", p.fail("control character in a string")
		}
		p.i++
	}
	out := append([]byte(nil), b[start:p.i]...)
	for p.i < len(b) {
		c := b[p.i]
		switch {
		case c == '"':
			if !utf8.Valid(out) {
				return "", p.fail("invalid UTF-8 in a string")
			}
			p.i++
			return string(out), nil
		case c < 0x20:
			return "", p.fail("control character in a string")
		case c != '\\':
			out = append(out, c)
			p.i++
			continue
		}
		p.i++
		if p.i >= len(b) {
			break
		}
		e := b[p.i]
		p.i++
		switch e {
		case '"', '\\', '/':
			out = append(out, e)
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			r, ok := p.hex4()
			if !ok {
				return "", p.fail("bad \\u escape")
			}
			if r >= 0xD800 && r < 0xDC00 {
				if p.i+1 < len(b) && b[p.i] == '\\' && b[p.i+1] == 'u' {
					p.i += 2
					lo, ok := p.hex4()
					if !ok || lo < 0xDC00 || lo >= 0xE000 {
						return "", p.fail("bad surrogate pair")
					}
					r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
				} else {
					return "", p.fail("lone surrogate")
				}
			} else if r >= 0xDC00 && r < 0xE000 {
				return "", p.fail("lone surrogate")
			}
			out = utf8.AppendRune(out, r)
		default:
			return "", p.fail("bad escape \\%c", e)
		}
	}
	return "", p.fail("unterminated string")
}

func (p *jparser) hex4() (rune, bool) {
	if p.i+4 > len(p.b) {
		return 0, false
	}
	var r rune
	for _, c := range p.b[p.i : p.i+4] {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			r |= rune(c-'A') + 10
		default:
			return 0, false
		}
	}
	p.i += 4
	return r, true
}

// ParseOps reads an RFC 6902 patch: a JSON array of operation objects. Members
// other than the ones an operation uses are ignored, as the RFC says. The index
// of the first bad operation is returned with the error, or -1 for a patch that
// is not an array; the operations returned then end with that one, as far as it
// was read, so the error can name it.
func ParseOps(data []byte, maxDepth int) ([]Op, int, error) {
	root, err := ParseValue(data, maxDepth)
	if err != nil {
		return nil, -1, err
	}
	return OpsFromValue(root)
}

// OpsFromValue reads the operations of a patch from its value: a sequence of
// mappings with the members "op", "path", "from" and "value", whether the value
// came from JSON or from YAML. It reports as ParseOps does.
func OpsFromValue(root edit.Value) ([]Op, int, error) {
	if root.Kind != edit.Seq {
		return nil, -1, fmt.Errorf("%w: a patch is a JSON array of operations", ErrSyntax)
	}
	ops := make([]Op, 0, len(root.Items))
	for i := range root.Items {
		it := &root.Items[i]
		if it.Kind != edit.Map {
			return nil, i, fmt.Errorf("%w: operation is not an object", ErrSyntax)
		}
		var op Op
		var hasPath, hasFrom, hasValue, hasOp, badFrom bool
		// fail reports an error in operation i with what has been read of it, so the
		// error can name the operation and path.
		fail := func(err error) ([]Op, int, error) { return append(ops, op), i, err }
		for j := 0; j < len(it.Items); j += 2 {
			v := &it.Items[j+1]
			switch it.Items[j].Text {
			case "op":
				if v.Kind != edit.String {
					return fail(fmt.Errorf("%w: \"op\" must be a string", ErrSyntax))
				}
				op.Name, hasOp = v.Text, true
			case "path":
				if v.Kind != edit.String {
					return fail(fmt.Errorf("%w: \"path\" must be a string", ErrSyntax))
				}
				op.Path, hasPath = v.Text, true
			case "from":
				// Only move and copy read "from"; other operations ignore the member
				// (RFC 6902, section 4), so a wrong type is judged once the op is known.
				if v.Kind != edit.String {
					badFrom = true
					break
				}
				op.From, hasFrom = v.Text, true
			case "value":
				op.Value, hasValue = *v, true
			}
		}
		if !hasOp {
			return fail(fmt.Errorf("%w: operation has no \"op\"", ErrSyntax))
		}
		if !hasPath {
			return fail(fmt.Errorf("%w: operation has no \"path\"", ErrSyntax))
		}
		switch op.Name {
		case "add", "replace", "test":
			if !hasValue {
				return fail(fmt.Errorf("%w: %s has no \"value\"", ErrSyntax, op.Name))
			}
		case "move", "copy":
			if badFrom {
				return fail(fmt.Errorf("%w: \"from\" must be a string", ErrSyntax))
			}
			if !hasFrom {
				return fail(fmt.Errorf("%w: %s has no \"from\"", ErrSyntax, op.Name))
			}
		case "remove":
		default:
			return fail(fmt.Errorf("%w: unknown operation %q", ErrSyntax, op.Name))
		}
		ops = append(ops, op)
	}
	return ops, -1, nil
}
