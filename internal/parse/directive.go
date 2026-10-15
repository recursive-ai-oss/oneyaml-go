package parse

import (
	"oneyaml.dev/go/internal/scan"
)

// directives reads the %YAML and %TAG directives before an explicit document
// start. Reserved directives are skipped.
func (p *Parser) directives() error {
	p.tags = p.tags[:0]
	p.version = [2]int{}
	for {
		t, err := p.peek()
		if err != nil {
			return err
		}
		if t.Kind != scan.Directive {
			return nil
		}
		if err := p.directive(t.Start, t.End); err != nil {
			return err
		}
		p.sc.Skip()
	}
}

func isWS(c byte) bool { return c == ' ' || c == '\t' }

// field returns the next white-space separated field of src[i:e].
func (p *Parser) field(i, e int) (s, end int) {
	for i < e && isWS(p.src[i]) {
		i++
	}
	s = i
	for i < e && !isWS(p.src[i]) {
		i++
	}
	return s, i
}

func (p *Parser) directive(s, e int) error {
	b := p.src
	ns, ne := p.field(s+1, e)
	switch string(b[ns:ne]) { // the conversion does not allocate in a switch
	case "YAML":
		if p.version[0] != 0 {
			return p.errAt(s, "found a duplicate %YAML directive")
		}
		vs, ve := p.field(ne, e)
		xs, xe := p.field(ve, e)
		if vs == ve || xe > xs {
			return p.errAt(s, "malformed %YAML directive")
		}
		maj, min, ok := parseVersion(b[vs:ve])
		if !ok {
			return p.errAt(vs, "malformed version number in %YAML directive")
		}
		if maj != 1 {
			return p.errAt(vs, "unsupported YAML version")
		}
		p.version = [2]int{maj, min}
	case "TAG":
		hs, he := p.field(ne, e)
		ps, pe := p.field(he, e)
		xs, xe := p.field(pe, e)
		if hs == he || ps == pe || xe > xs {
			return p.errAt(s, "malformed %TAG directive")
		}
		if b[hs] != '!' || he-hs > 1 && b[he-1] != '!' {
			return p.errAt(hs, "a tag handle must start and end with '!'")
		}
		for i := hs + 1; i < he-1 && he-hs > 1; i++ {
			c := b[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return p.errAt(i, "invalid character in a tag handle")
			}
		}
		for _, d := range p.tags {
			if string(b[d.hs:d.he]) == string(b[hs:he]) {
				return p.errAt(hs, "found a duplicate %TAG directive")
			}
		}
		p.tags = append(p.tags, tagDirective{hs, he, ps, pe})
	}
	return nil
}

func parseVersion(b []byte) (maj, min int, ok bool) {
	i := 0
	for i < len(b) && b[i] >= '0' && b[i] <= '9' && i < 6 {
		maj = maj*10 + int(b[i]-'0')
		i++
	}
	if i == 0 || i >= len(b) || b[i] != '.' {
		return 0, 0, false
	}
	i++
	j := i
	for i < len(b) && b[i] >= '0' && b[i] <= '9' && i-j < 6 {
		min = min*10 + int(b[i]-'0')
		i++
	}
	return maj, min, i > j && i == len(b)
}

// splitTag splits the tag text at [s,e) into its handle and suffix. A verbatim
// tag "!<...>" has no handle.
func (p *Parser) splitTag(s, e int) (hEnd int, verbatim bool) {
	b := p.src
	if e-s >= 2 && b[s+1] == '<' {
		return s, true
	}
	for i := s + 1; i < e; i++ {
		if b[i] == '!' {
			return i + 1, false
		}
	}
	return s + 1, false
}

const defaultTagPrefix = "tag:yaml.org,2002:"

// lookupPrefix finds the prefix registered for the handle src[s:e] in the
// current document. It returns the prefix as a span of the source; found is
// false when only a default applies (see defaultPrefix).
func (p *Parser) lookupPrefix(s, e int) (ps, pe int, found bool) {
	for i := len(p.tags) - 1; i >= 0; i-- {
		d := p.tags[i]
		if string(p.src[d.hs:d.he]) == string(p.src[s:e]) {
			return d.ps, d.pe, true
		}
	}
	return 0, 0, false
}

// defaultPrefix returns the prefix of the primary "!" and secondary "!!"
// handles when no %TAG directive redefines them.
func (p *Parser) defaultPrefix(s, e int) (string, bool) {
	switch e - s {
	case 1:
		return "!", true
	case 2:
		if p.src[s+1] == '!' {
			return defaultTagPrefix, true
		}
	}
	return "", false
}

// checkTag verifies that the tag at [s,e) can be resolved.
func (p *Parser) checkTag(s, e int) error {
	hEnd, verbatim := p.splitTag(s, e)
	if verbatim {
		return nil
	}
	if hEnd == e && hEnd-s > 1 {
		return p.errAt(s, "a tag shorthand needs a suffix")
	}
	if hEnd-s == 1 || len(p.tags) == 0 && hEnd-s == 2 && p.src[s+1] == '!' {
		return nil
	}
	if _, _, ok := p.lookupPrefix(s, hEnd); !ok {
		if _, ok := p.defaultPrefix(s, hEnd); !ok {
			return p.errAt(s, "found an undefined tag handle")
		}
	}
	return nil
}

// AppendTag appends the resolved tag of ev to dst: the %TAG prefix (or the
// default for "!" and "!!") followed by the percent-decoded suffix. A verbatim
// tag "!<x>" gives "x" and the non-specific tag "!" gives "!". It must be called
// while the document that contains ev is current, before the next DocumentStart
// is read, because %TAG directives are per document.
func (p *Parser) AppendTag(dst []byte, ev *Event) ([]byte, error) {
	if !ev.HasTag() {
		return dst, nil
	}
	s, e := ev.TagStart, ev.TagEnd
	hEnd, verbatim := p.splitTag(s, e)
	if verbatim {
		return appendPercent(dst, p.src[s+2:e-1]), nil
	}
	if hEnd == e && e-s == 1 {
		return append(dst, '!'), nil
	}
	if err := p.checkTag(s, e); err != nil {
		return dst, err
	}
	if ps, pe, ok := p.lookupPrefix(s, hEnd); ok {
		dst = append(dst, p.src[ps:pe]...)
	} else {
		prefix, _ := p.defaultPrefix(s, hEnd)
		dst = append(dst, prefix...)
	}
	return appendPercent(dst, p.src[hEnd:e]), nil
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func appendPercent(dst, b []byte) []byte {
	for i := 0; i < len(b); i++ {
		if b[i] == '%' && i+2 < len(b) && unhex(b[i+1]) >= 0 && unhex(b[i+2]) >= 0 {
			if v := byte(unhex(b[i+1])<<4 | unhex(b[i+2])); v < 0x80 {
				dst = append(dst, v)
				i += 2
				continue
			}
			// An escaped non-ASCII byte stays as written: tag characters are kept
			// exactly as presented [YAML 1.2 section 5.6].
		}
		dst = append(dst, b[i])
	}
	return dst
}
