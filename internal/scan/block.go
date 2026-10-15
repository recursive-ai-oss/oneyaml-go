package scan

// BlockIndent returns the content indentation of a block scalar whose header
// has the explicit indentation indicator inc (1-9), inside a block whose own
// indentation is parent (-1 at the top level) [YAML 1.2 production 163].
// The scanner stores the result (or the detected indentation) in Token.Indent.
func BlockIndent(parent, inc int) int { return parent + inc }

func (s *Scanner) fetchBlockScalar(col int) error {
	if err := s.removeKey(); err != nil {
		return err
	}
	s.allowKey = true
	b := s.src
	start := s.pos
	style := Literal
	if b[start] == '>' {
		style = Folded
	}
	p := start + 1
	inc := 0
	chomp := false
	for i := 0; i < 2; i++ {
		c := s.at(p)
		switch {
		case (c == '+' || c == '-') && !chomp:
			chomp = true
			p++
		case c >= '1' && c <= '9' && inc == 0:
			inc = int(c - '0')
			p++
		case c == '0':
			return s.errAt(p, Syntax, "a block scalar indentation indicator cannot be 0")
		}
	}
	for p < len(b) && (b[p] == ' ' || b[p] == '\t') {
		p++
	}
	if p < len(b) && b[p] == '#' {
		if !s.blankz(p - 1) {
			return s.errAt(p, Syntax, "a comment must be separated from other tokens by white space")
		}
		for p < len(b) && !isBreak(b[p]) {
			p++
		}
	}
	if p < len(b) && !isBreak(b[p]) {
		return s.errAt(p, Syntax, "unexpected content after a block scalar header")
	}
	p = breakEnd(b, p)
	ls := p
	parent := s.indent

	indent := 0
	if inc > 0 {
		indent = BlockIndent(parent, inc)
		if indent < 0 {
			indent = 0
		}
	} else {
		// Detect the indentation from the first non-empty line.
		q := p
		maxBlank, first, found := 0, 0, false
		for q < len(b) {
			n := 0
			for q+n < len(b) && b[q+n] == ' ' {
				n++
			}
			r := q + n
			if r >= len(b) {
				if n > maxBlank {
					maxBlank = n
				}
				break
			}
			if isBreak(b[r]) {
				if n > maxBlank {
					maxBlank = n
				}
				q = breakEnd(b, r)
				continue
			}
			if r == q && s.docMarker(q) {
				break
			}
			first, found = n, true
			break
		}
		indent = first
		if !found {
			indent = maxBlank
		}
		if indent < parent+1 {
			indent = parent + 1
		}
		if found && maxBlank > indent {
			return s.errAt(p, Syntax, "a leading empty line is indented more than the first content line")
		}
	}

	end, endLS := p, ls
	q := p
	contentSeen := false
	for q < len(b) {
		n := 0
		for n < indent && q+n < len(b) && b[q+n] == ' ' {
			n++
		}
		r := q + n
		if r >= len(b) {
			end, endLS = r, q
			break
		}
		if isBreak(b[r]) {
			q = breakEnd(b, r)
			end, endLS = q, q
			continue
		}
		if n < indent {
			if b[r] == '\t' && !contentSeen {
				return s.errAt(r, Syntax, "a tab cannot be used for indentation after a block scalar")
			}
			break
		}
		if n == 0 && s.docMarker(q) {
			break
		}
		contentSeen = true
		for r < len(b) && !isBreak(b[r]) {
			r++
		}
		if r >= len(b) {
			end, endLS = r, q
			break
		}
		q = breakEnd(b, r)
		end, endLS = q, q
	}
	s.pos = end
	s.lineStart = endLS
	s.push(Scalar, start, end)
	t := &s.tokens[len(s.tokens)-1]
	t.Style = style
	t.Indent = indent
	return nil
}

// breakEnd returns the offset just after the line break at p, or p when there
// is none.
func breakEnd(b []byte, p int) int {
	if p >= len(b) {
		return p
	}
	if b[p] == '\r' {
		if p+1 < len(b) && b[p+1] == '\n' {
			return p + 2
		}
		return p + 1
	}
	if b[p] == '\n' {
		return p + 1
	}
	return p
}
