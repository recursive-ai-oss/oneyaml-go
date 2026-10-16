// Package scan is a lossless YAML 1.2 scanner.
//
// The scanner reads UTF-8 only. UTF-16 and UTF-32 input is detected here and
// reported with category Encoding; the public entry points transcode such
// input to UTF-8 before scanning and record the original encoding so that
// output can be written back in it.
//
// It turns a []byte into tokens. Every token is a pair of byte offsets into
// the source; nothing is copied or converted to a string. Whatever lies between
// two consecutive tokens is trivia: spaces, tabs, comments, line breaks and a
// leading byte order mark. Tokens plus trivia cover the input exactly, so a
// consumer that keeps the offsets can reproduce the source byte for byte.
//
// The scanner follows the structure of the classic libyaml scanner (a token
// queue, a stack of block indents and one "simple key" slot per flow level),
// because that is the shape that handles implicit keys without backtracking.
// Block collection and key tokens are inserted retroactively when a ':' shows
// that an earlier token was a key.
package scan

import (
	"strconv"
	"unicode/utf8"
)

// Kind is the type of a Token.
type Kind uint8

// Token kinds. Block collection starts, BlockEnd and Key are synthesized:
// they have zero width and sit where the construct begins or ends.
const (
	StreamStart        Kind = iota + 1 // zero width, after any byte order mark
	StreamEnd                          // zero width, at the end of the input
	Directive                          // "%YAML ..." or "%TAG ..." line, without the trailing comment
	DocumentStart                      // "---"
	DocumentEnd                        // "..."
	BlockSequenceStart                 // zero width, at the first "-"
	BlockMappingStart                  // zero width, at the first key
	BlockEnd                           // zero width, where indentation closes a block
	FlowSequenceStart                  // "["
	FlowSequenceEnd                    // "]"
	FlowMappingStart                   // "{"
	FlowMappingEnd                     // "}"
	BlockEntry                         // "-"
	FlowEntry                          // ","
	Key                                // "?" for an explicit key, zero width for an implicit one
	Value                              // ":"
	Alias                              // "*name"
	Anchor                             // "&name"
	Tag                                // "!", "!!str", "!e!x" or "!<verbatim>"
	Scalar                             // a scalar; see Style
)

var kindNames = [...]string{
	StreamStart: "StreamStart", StreamEnd: "StreamEnd", Directive: "Directive",
	DocumentStart: "DocumentStart", DocumentEnd: "DocumentEnd",
	BlockSequenceStart: "BlockSequenceStart", BlockMappingStart: "BlockMappingStart",
	BlockEnd: "BlockEnd", FlowSequenceStart: "FlowSequenceStart",
	FlowSequenceEnd: "FlowSequenceEnd", FlowMappingStart: "FlowMappingStart",
	FlowMappingEnd: "FlowMappingEnd", BlockEntry: "BlockEntry", FlowEntry: "FlowEntry",
	Key: "Key", Value: "Value", Alias: "Alias", Anchor: "Anchor", Tag: "Tag", Scalar: "Scalar",
}

// String returns the kind name.
func (k Kind) String() string {
	if int(k) < len(kindNames) && kindNames[k] != "" {
		return kindNames[k]
	}
	return "Unknown"
}

// Style is the presentation style of a scalar.
type Style uint8

// Scalar styles.
const (
	Plain Style = iota
	SingleQuoted
	DoubleQuoted
	Literal
	Folded
)

// Token is one lexical unit. The span [Start, End) indexes the source.
//
// For a Scalar the span covers the whole presentation: the quotes of a quoted
// scalar, or the header and every content line (including the line break that
// ends the last one, and trailing blank lines) of a block scalar. Indent is
// only set for block scalars: it is the number of spaces that precede the
// content of every line, whether given by the header or detected.
type Token struct {
	Kind   Kind
	Style  Style
	Start  int
	End    int
	Indent int
}

// DefaultMaxDepth is the nesting limit used when New is given 0.
const DefaultMaxDepth = 10000

// Per flow level state bits.
const (
	flowIsMap     = 1 << iota // the level is a flow mapping
	flowValueSeen             // a ':' was seen in the current entry
)

type simpleKey struct {
	possible  bool
	required  bool
	afterTab  bool
	multiline bool // inside a flow mapping, where a key may span lines
	tokenNo   int  // absolute number of the token that would become the key
	pos       int
	col       int
}

// Scanner produces tokens from a byte slice. It is not safe for concurrent use.
// A Scanner and its internal slices can be reused with Reset.
type Scanner struct {
	src    []byte
	pos    int
	bomEnd int
	badAt  int // offset of the first byte that may not appear in a stream, or len(src)+1
	badCat Category
	badMsg string
	// badSoft marks a bad byte that is legal inside a quoted scalar: DEL, the C1
	// controls and U+FFFE/U+FFFF are in nb-json but not in c-printable, so only a
	// quoted scalar may hold them.
	badSoft  bool
	maxDepth int

	lineStart int
	cachePos  int
	cacheCol  int

	tokens []Token
	head   int
	parsed int // tokens already handed out

	indent        int
	indents       []int
	allowKey      bool
	tabSince      bool // a tab separated the previous token from the next on this line
	keys          []simpleKey
	firstPossible int
	saveMin       int   // lowest key slot saved since the last line break
	staleWork     int   // slots examined by staleKeys; the scaling test reads it
	boms          []int // offsets of every U+FEFF after the leading one
	bomCur        int
	prefixOK      int // offsets up to here were found to be a document prefix
	keysLine      int
	flow          int
	jsonLike      bool    // the last token was a quoted scalar or a flow collection end, inside flow context
	flowState     []uint8 // per flow level: flowIsMap, flowValueSeen
	flowOpens     []int   // per flow level: offset of its opening bracket, for messages
	quoteStart    int     // offset of the quoted scalar being read, for messages
	lastKind      Kind

	valueErr bool
	skipped  Token
	withhold bool // the error is about a ":" or a missing ":"; see Pending

	started bool
	ended   bool
	err     error
}

// New returns a Scanner for src. maxDepth bounds block plus flow nesting: 0
// selects DefaultMaxDepth and a negative value disables the check.
func New(src []byte, maxDepth int) *Scanner {
	s := &Scanner{}
	s.Reset(src, maxDepth)
	return s
}

// Reset prepares s to scan src, keeping allocated buffers.
func (s *Scanner) Reset(src []byte, maxDepth int) {
	if maxDepth == 0 {
		maxDepth = DefaultMaxDepth
	}
	*s = Scanner{
		src:       src,
		maxDepth:  maxDepth,
		tokens:    s.tokens[:0],
		indents:   s.indents[:0],
		keys:      s.keys[:0],
		flowState: s.flowState[:0],
		flowOpens: s.flowOpens[:0],
		boms:      s.boms[:0],
	}
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		s.pos = 3
		s.bomEnd = 3
	}
	s.lineStart = s.pos
	s.cachePos = s.pos
	s.badAt = len(src) + 1
	if enc := detectWide(src); enc != "" {
		s.badAt, s.badCat, s.badMsg = 0, Encoding, "input is "+enc+"; the scanner reads UTF-8 only"
		return
	}
	s.checkEncoding(s.pos)
}

// Source returns the slice being scanned.
func (s *Scanner) Source() []byte { return s.src }

// Peek returns the next token without consuming it. The pointer is valid until
// the next call to Peek or Skip. After an error, tokens already queued are
// still delivered, then the error is returned. A consumer that wants to hand
// out a token only once the one after it is known to be scannable (a token
// may turn out to be an implicit key) checks Pending after Skip.
func (s *Scanner) Peek() (*Token, error) {
	if s.err == nil {
		if err := s.fetchMore(); err != nil {
			s.err = err
			s.withhold = s.valueErr
		}
	}
	if s.head >= len(s.tokens) && s.err == nil {
		// Past the end of the stream: keep answering StreamEnd.
		s.push(StreamEnd, len(s.src), len(s.src))
	}
	if s.head < len(s.tokens) {
		return &s.tokens[s.head], nil
	}
	return nil, s.err
}

// Pending returns the scanner's error once every queued token has been skipped
// and nil otherwise. A scanner error means the token scanned after the last
// queued one is faulty, so the consumer must not act on the token it just
// skipped.
func (s *Scanner) Pending() error {
	if s.err != nil && s.withhold && s.head >= len(s.tokens) && !(s.skipped.Kind == Scalar && s.skipped.Style != Plain) {
		return s.err
	}
	return nil
}

// Skip consumes the token returned by the last Peek.
func (s *Scanner) Skip() {
	s.skipped = s.tokens[s.head]
	s.head++
	s.parsed++
	if s.head == len(s.tokens) {
		s.tokens = s.tokens[:0]
		s.head = 0
	}
}

// where describes the position of offset off in a message.
func (s *Scanner) where(off int) string {
	e := NewError(s.src, off, Syntax, "")
	return "line " + strconv.Itoa(e.Line) + ", column " + strconv.Itoa(e.Column)
}

// flowIndentMsg words the error for a line that ends a flow collection by its
// indentation. The input is most likely missing a closing bracket.
func (s *Scanner) flowIndentMsg() string {
	if n := len(s.flowOpens); n > 0 {
		off := s.flowOpens[n-1]
		kind := "sequence"
		close := "]"
		if s.src[off] == '{' {
			kind, close = "mapping", "}"
		}
		return "the flow " + kind + " opened at " + s.where(off) + " is not closed: expected ',' or '" + close +
			"' before this line; add the missing '" + close + "' or indent this line past its parent"
	}
	return "flow content is not indented enough"
}

func (s *Scanner) errAt(off int, cat Category, msg string, value ...bool) error {
	s.valueErr = len(value) > 0
	return NewError(s.src, off, cat, msg)
}

// detectWide recognises UTF-16 and UTF-32 by the byte order mark or by the
// null-byte pattern of a first ASCII character [YAML 1.2 section 5.2].
func detectWide(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	switch {
	case len(b) >= 4 && b[0] == 0 && b[1] == 0 && b[2] == 0xFE && b[3] == 0xFF,
		len(b) >= 4 && b[0] == 0 && b[1] == 0 && b[2] == 0 && b[3] != 0:
		return "UTF-32 (big endian)"
	case len(b) >= 4 && b[0] == 0xFF && b[1] == 0xFE && b[2] == 0 && b[3] == 0,
		len(b) >= 4 && b[0] != 0 && b[1] == 0 && b[2] == 0 && b[3] == 0:
		return "UTF-32 (little endian)"
	case b[0] == 0xFE && b[1] == 0xFF, b[0] == 0 && b[1] != 0:
		return "UTF-16 (big endian)"
	case b[0] == 0xFF && b[1] == 0xFE, b[0] != 0 && b[1] == 0:
		return "UTF-16 (little endian)"
	}
	return ""
}

// asciiOK marks the ASCII bytes that may appear in a stream: printable
// characters, tab and the two line break characters.
var asciiOK = func() (t [256]bool) {
	for c := 0x20; c < 0x7F; c++ {
		t[c] = true
	}
	t['\t'], t['\n'], t['\r'] = true, true, true
	return
}()

// checkEncoding finds the first byte at or after from that cannot be part of a
// YAML stream. UTF-16 and UTF-32 are rejected in Reset rather than transcoded:
// transcoding would change every offset, and offsets into the caller's own
// bytes are what make lossless edits possible.
func (s *Scanner) checkEncoding(from int) {
	b := s.src
	i := from
	n := len(b)
	s.badAt, s.badSoft = len(b)+1, false
	for i < n {
		for i < n && asciiOK[b[i]] {
			i++
		}
		if i >= n {
			break
		}
		c := b[i]
		if c < utf8.RuneSelf {
			s.badAt, s.badCat, s.badMsg = i, Syntax, "control character is not allowed in YAML"
			s.badSoft = c == 0x7F
			return
		}
		r, size := utf8.DecodeRune(b[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			s.badAt, s.badCat, s.badMsg = i, Encoding, "invalid UTF-8"
			return
		case r == 0xFEFF:
			// Allowed only as trivia before a document or inside quoted scalars;
			// the scanner decides where.
			s.boms = append(s.boms, i)
		case r == 0x85 || r >= 0xA0 && r <= 0xD7FF || r >= 0xE000 && r <= 0xFFFD || r >= 0x10000:
		default:
			s.badAt, s.badCat, s.badMsg = i, Syntax, "character is not allowed in YAML"
			s.badSoft = true
			return
		}
		i += size
	}
}

// column returns the 0-based column of pos in characters. The scanner only
// moves forward, so the last answer is cached and the total cost per line is
// linear.
func (s *Scanner) column(pos int) int {
	if s.cachePos < s.lineStart || s.cachePos > pos {
		s.cachePos = s.lineStart
		s.cacheCol = 0
	}
	c := s.cacheCol
	b := s.src
	for i := s.cachePos; i < pos; i++ {
		if b[i]&0xC0 != 0x80 {
			c++
		}
	}
	s.cachePos, s.cacheCol = pos, c
	return c
}

func (s *Scanner) at(i int) byte {
	if i < len(s.src) {
		return s.src[i]
	}
	return 0
}

func isBreak(c byte) bool { return c == '\n' || c == '\r' }

// blankz reports whether position i holds a space, tab, line break or the end
// of the input.
func (s *Scanner) blankz(i int) bool {
	if i >= len(s.src) {
		return true
	}
	c := s.src[i]
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isFlowInd(c byte) bool { return c == ',' || c == '[' || c == ']' || c == '{' || c == '}' }

// skipBreak consumes the line break at pos (LF, CRLF or CR).
func (s *Scanner) skipBreak() {
	if s.src[s.pos] == '\r' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '\n' {
		s.pos += 2
	} else {
		s.pos++
	}
	s.lineStart = s.pos
}

func (s *Scanner) push(k Kind, start, end int) {
	s.tokens = append(s.tokens, Token{Kind: k, Start: start, End: end})
	s.lastKind = k
	s.jsonLike = false
}

func (s *Scanner) fetchMore() error {
	// Drop consumed tokens so the queue stays short. Tokens are addressed
	// relative to head, so moving them down is safe.
	if s.head >= 32 && s.head*2 >= len(s.tokens) {
		n := copy(s.tokens, s.tokens[s.head:])
		s.tokens = s.tokens[:n]
		s.head = 0
	}
	for {
		if s.ended {
			return nil
		}
		// Always keep one token of lookahead beyond the head, so that a fault in
		// the successor is known before the head is acted on.
		need := len(s.tokens)-s.head < 2
		if !need {
			for s.firstPossible < len(s.keys) && !s.keys[s.firstPossible].possible {
				s.firstPossible++
			}
			if s.firstPossible < len(s.keys) && s.keys[s.firstPossible].tokenNo == s.parsed {
				need = true
			}
		}
		if !need {
			return nil
		}
		n := len(s.tokens)
		if err := s.fetchNext(); err != nil {
			return err
		}
		if s.badAt < s.pos || (s.ended && s.badAt <= len(s.src)) {
			s.tokens = s.tokens[:n]
			return s.errAt(s.badAt, s.badCat, s.badMsg)
		}
	}
}

func (s *Scanner) fetchNext() error {
	err := s.fetchToken()
	s.tabSince = false
	return err
}

func (s *Scanner) fetchToken() error {
	if !s.started {
		s.started = true
		s.indent = -1
		s.allowKey = true
		s.keys = append(s.keys, simpleKey{})
		s.push(StreamStart, s.pos, s.pos)
		return nil
	}
	if err := s.scanToNextToken(); err != nil {
		return err
	}
	if err := s.staleKeys(); err != nil {
		return err
	}
	col := s.column(s.pos)
	var c byte
	atLineStart := s.pos == s.lineStart
	target := col
	if s.pos >= len(s.src) {
		target = -1
	} else {
		c = s.src[s.pos]
		if atLineStart && (c == '%' || (c == '-' || c == '.') && s.docMarker(s.pos)) {
			target = -1
		}
	}
	// Close one block per call, so a deep nesting that ends at once does not
	// queue its whole depth of BlockEnd tokens.
	if s.flow == 0 && s.indent > target {
		s.push(BlockEnd, s.pos, s.pos)
		n := len(s.indents) - 1
		s.indent = s.indents[n]
		s.indents = s.indents[:n]
		return nil
	}
	if s.pos >= len(s.src) {
		return s.fetchStreamEnd()
	}
	start, n := s.pos, len(s.tokens)
	if err := s.dispatch(c, col, atLineStart); err != nil {
		return err
	}
	if len(s.boms) > 0 && !(s.tokens[len(s.tokens)-1].Kind == Scalar && s.tokens[len(s.tokens)-1].Style == DoubleQuoted || s.tokens[len(s.tokens)-1].Kind == Scalar && s.tokens[len(s.tokens)-1].Style == SingleQuoted) {
		if at := s.bomIn(start, s.pos); at >= 0 {
			s.tokens = s.tokens[:n]
			return s.errAt(at, Syntax, "a byte order mark cannot appear here")
		}
	}
	return nil
}

func (s *Scanner) dispatch(c byte, col int, atLineStart bool) error {
	switch {
	case c == '%' && atLineStart:
		return s.fetchDirective()
	case c == '-' && atLineStart && s.docMarker(s.pos):
		return s.fetchDocIndicator(DocumentStart)
	case c == '.' && atLineStart && s.docMarker(s.pos):
		return s.fetchDocIndicator(DocumentEnd)
	case c == '[':
		return s.fetchFlowStart(FlowSequenceStart, col)
	case c == '{':
		return s.fetchFlowStart(FlowMappingStart, col)
	case c == ']':
		return s.fetchFlowEnd(FlowSequenceEnd)
	case c == '}':
		return s.fetchFlowEnd(FlowMappingEnd)
	case c == ',':
		return s.fetchFlowEntry()
	case c == '-' && s.blankz(s.pos+1):
		return s.fetchBlockEntry(col)
	case c == '?' && s.blankz(s.pos+1):
		return s.fetchKey(col)
	case c == ':' && s.isValueIndicator():
		return s.fetchValue(col)
	case c == '*':
		return s.fetchAnchor(Alias, col)
	case c == '&':
		return s.fetchAnchor(Anchor, col)
	case c == '!':
		return s.fetchTag(col)
	case (c == '|' || c == '>') && s.flow == 0:
		return s.fetchBlockScalar(col)
	case c == '\'':
		return s.fetchQuoted(false, col)
	case c == '"':
		return s.fetchQuoted(true, col)
	case s.plainStart(c):
		return s.fetchPlain(col)
	}
	return s.errAt(s.pos, Syntax, "found a character that cannot start any token")
}

// docMarker reports whether "---" or "..." (the same three bytes) starts at i
// and is followed by a blank or the end of input.
func (s *Scanner) docMarker(i int) bool {
	b := s.src
	return i+2 < len(b) && (b[i] == '-' || b[i] == '.') && b[i] == b[i+1] && b[i] == b[i+2] && s.blankz(i+3)
}

// isValueIndicator decides whether the ':' at pos is a value indicator. In
// flow context a ':' directly after a JSON-like node (quoted scalar or flow
// collection) needs no following space, and ':' before a flow indicator also
// counts [YAML 1.2 productions 147, 152].
func (s *Scanner) isValueIndicator() bool {
	if s.blankz(s.pos + 1) {
		return true
	}
	if s.flow > 0 {
		return isFlowInd(s.at(s.pos+1)) || s.jsonLike
	}
	return false
}

func (s *Scanner) plainStart(c byte) bool {
	if s.blankz(s.pos) {
		return false
	}
	switch c {
	case '-', '?', ':':
		n := s.pos + 1
		if s.blankz(n) {
			return false
		}
		return s.flow == 0 || !isFlowInd(s.src[n])
	case ',', '[', ']', '{', '}', '#', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`':
		return false
	}
	return true
}

// scanToNextToken skips trivia: spaces, tabs, comments and line breaks.
func (s *Scanner) scanToNextToken() error {
	crossed := false
	b := s.src
	for {
		if s.pos >= len(b) {
			return nil
		}
		c := b[s.pos]
		switch {
		case c == ' ':
			s.pos++
		case c == '\t':
			if s.flow == 0 {
				s.tabSince = true
			}
			s.pos++
		case c == '#':
			if s.pos > s.lineStart && !s.blankz(s.pos-1) {
				return s.errAt(s.pos, Syntax, "a comment must be separated from other tokens by white space")
			}
			from := s.pos
			for s.pos < len(b) && !isBreak(b[s.pos]) {
				s.pos++
			}
			if at := s.bomIn(from, s.pos); at >= 0 {
				return s.errAt(at, Syntax, "a byte order mark cannot appear in a comment")
			}
		case isBreak(c):
			s.skipBreak()
			crossed = true
			s.tabSince = false
			if s.flow == 0 {
				s.allowKey = true
			}
		case c == 0xEF && s.pos == s.lineStart && s.pos+2 < len(b) && b[s.pos+1] == 0xBB && b[s.pos+2] == 0xBF:
			// A byte order mark belongs to a document prefix [production 211]: at the
			// start of the stream, after "...", or directly before a document marker.
			// Inside a document it is not allowed. On a line of its own, or before a
			// comment, it is the prefix of a following document only if nothing but
			// comments and blank lines separate it from a marker or the end of the input.
			after := s.pos + 3
			if !(s.lastKind == StreamStart || s.lastKind == DocumentEnd || s.docMarker(after) || s.prefixOnly(after)) {
				return s.errAt(s.pos, Syntax, "a byte order mark is only allowed before a document")
			}
			s.pos = after
			// The mark is not part of the line: a "---" after it is still at column 0.
			s.lineStart = s.pos
		default:
			if crossed && s.flow > 0 && s.indent >= 0 && s.leadingSpaces() <= s.indent &&
				!(s.pos == s.lineStart && s.docMarker(s.pos)) {
				return s.errAt(s.pos, Syntax, s.flowIndentMsg())
			}
			return nil
		}
	}
}

// prefixOnly reports whether only white space, comments and line breaks lie
// between offset i and a document marker or the end of the input. The scan
// is remembered, so a run of marked comment lines is looked at once.
func (s *Scanner) prefixOnly(i int) bool {
	if i <= s.prefixOK {
		return true
	}
	b := s.src
	p := i
	for {
		for p < len(b) && (b[p] == ' ' || b[p] == '\t') {
			p++
		}
		if p < len(b) && b[p] == '#' {
			for p < len(b) && !isBreak(b[p]) {
				p++
			}
		}
		if p >= len(b) {
			s.prefixOK = len(b)
			return true
		}
		if !isBreak(b[p]) {
			return false
		}
		p = breakEnd(b, p)
		for p+2 < len(b) && b[p] == 0xEF && b[p+1] == 0xBB && b[p+2] == 0xBF {
			p += 3 // every prefix line may start with its own mark
		}
		if s.docMarker(p) {
			s.prefixOK = p
			return true
		}
	}
}

// bomIn returns the offset of the first U+FEFF in [a, b), or -1. Offsets are
// queried in increasing order, so a cursor makes the total cost linear.
func (s *Scanner) bomIn(a, b int) int {
	if len(s.boms) == 0 {
		return -1
	}
	for s.bomCur < len(s.boms) && s.boms[s.bomCur] < a {
		s.bomCur++
	}
	if s.bomCur < len(s.boms) && s.boms[s.bomCur] < b {
		return s.boms[s.bomCur]
	}
	return -1
}

// leadingSpaces counts the spaces that start the current line; a tab does not
// count as indentation.
func (s *Scanner) leadingSpaces() int {
	n := 0
	for s.lineStart+n < len(s.src) && s.src[s.lineStart+n] == ' ' {
		n++
	}
	return n
}

// staleKeys drops simple keys that can no longer be keys: they were saved on an
// earlier line, or are more than 1024 characters back [YAML 1.2 section 7.4.2, 8.2.2].
func (s *Scanner) staleKeys() error {
	if s.lineStart != s.keysLine {
		s.keysLine = s.lineStart
		// Only keys saved since the last line break are still alive, and they are
		// at or above saveMin. Keys in a flow mapping may span lines and stay.
		for i := s.saveMin; i < len(s.keys); i++ {
			s.staleWork++
			k := &s.keys[i]
			if k.possible && !k.multiline {
				if k.required {
					return s.errAt(s.pos, Syntax, "could not find expected ':'", true)
				}
				k.possible = false
			}
		}
		s.saveMin = len(s.keys)
	}
	// The key the consumer is waiting on holds the token queue; it cannot be
	// a key once it is more than 1024 characters back, so let it go now.
	for s.firstPossible < len(s.keys) {
		k := &s.keys[s.firstPossible]
		if !k.possible {
			s.firstPossible++
			continue
		}
		if k.multiline || s.pos-k.pos <= 1024 || s.column(s.pos)-k.col <= 1024 {
			break
		}
		if k.required {
			return s.errAt(s.pos, Syntax, "could not find expected ':'", true)
		}
		k.possible = false
	}
	return nil
}

func (s *Scanner) saveKey(col int) error {
	if !s.allowKey {
		return nil
	}
	if err := s.removeKey(); err != nil {
		return err
	}
	i := len(s.keys) - 1
	s.keys[i] = simpleKey{
		possible:  true,
		required:  s.flow == 0 && s.indent == col,
		afterTab:  s.tabSince,
		multiline: s.flow > 0 && s.flowState[s.flow-1]&flowIsMap != 0,
		tokenNo:   s.parsed + len(s.tokens) - s.head,
		pos:       s.pos,
		col:       col,
	}
	if i < s.firstPossible {
		s.firstPossible = i
	}
	if i < s.saveMin {
		s.saveMin = i
	}
	return nil
}

func (s *Scanner) removeKey() error {
	k := &s.keys[len(s.keys)-1]
	if k.possible && k.required {
		return s.errAt(s.pos, Syntax, "could not find expected ':'", true)
	}
	k.possible = false
	return nil
}

func (s *Scanner) depthCheck() error {
	if s.maxDepth > 0 && len(s.indents)+s.flow >= s.maxDepth {
		return s.errAt(s.pos, Limit, "maximum nesting depth exceeded")
	}
	return nil
}

func (s *Scanner) rollIndent(col, number int, kind Kind, pos int, tab bool) error {
	if s.flow > 0 || s.indent >= col {
		return nil
	}
	if tab {
		return s.errAt(pos, Syntax, "a tab cannot precede a nested block collection")
	}
	if err := s.depthCheck(); err != nil {
		return err
	}
	s.indents = append(s.indents, s.indent)
	s.indent = col
	t := Token{Kind: kind, Start: pos, End: pos}
	if number < 0 {
		s.tokens = append(s.tokens, t)
	} else {
		s.insert(number-s.parsed+s.head, t)
	}
	return nil
}

func (s *Scanner) insert(i int, t Token) {
	s.tokens = append(s.tokens, Token{})
	copy(s.tokens[i+1:], s.tokens[i:])
	s.tokens[i] = t
}

func (s *Scanner) unroll(col int) {
	if s.flow > 0 {
		return
	}
	for s.indent > col {
		s.push(BlockEnd, s.pos, s.pos)
		n := len(s.indents) - 1
		s.indent = s.indents[n]
		s.indents = s.indents[:n]
	}
}

func (s *Scanner) fetchStreamEnd() error {
	s.unroll(-1)
	for i := range s.keys {
		k := &s.keys[i]
		if k.possible && k.required {
			return s.errAt(s.pos, Syntax, "could not find expected ':'", true)
		}
		k.possible = false
	}
	s.allowKey = false
	s.push(StreamEnd, s.pos, s.pos)
	s.ended = true
	return nil
}

func (s *Scanner) fetchDirective() error {
	s.unroll(-1)
	if err := s.removeKey(); err != nil {
		return err
	}
	s.allowKey = false
	start := s.pos
	b := s.src
	p := start
	for p < len(b) && !isBreak(b[p]) {
		if b[p] == '#' && p > start && (b[p-1] == ' ' || b[p-1] == '\t') {
			break
		}
		p++
	}
	for p > start && (b[p-1] == ' ' || b[p-1] == '\t') {
		p--
	}
	s.pos = p
	s.push(Directive, start, p)
	return nil
}

func (s *Scanner) fetchDocIndicator(k Kind) error {
	s.unroll(-1)
	if err := s.removeKey(); err != nil {
		return err
	}
	s.allowKey = false
	s.push(k, s.pos, s.pos+3)
	s.pos += 3
	return nil
}

func (s *Scanner) fetchFlowStart(k Kind, col int) error {
	if err := s.saveKey(col); err != nil {
		return err
	}
	if err := s.depthCheck(); err != nil {
		return err
	}
	s.keys = append(s.keys, simpleKey{})
	var fs uint8
	if k == FlowMappingStart {
		fs = flowIsMap
	}
	s.flowState = append(s.flowState, fs)
	s.flowOpens = append(s.flowOpens, s.pos)
	s.flow++
	s.allowKey = true
	s.push(k, s.pos, s.pos+1)
	s.pos++
	return nil
}

func (s *Scanner) fetchFlowEnd(k Kind) error {
	if err := s.removeKey(); err != nil {
		return err
	}
	if s.flow > 0 {
		s.keys = s.keys[:len(s.keys)-1]
		s.flowState = s.flowState[:len(s.flowState)-1]
		s.flowOpens = s.flowOpens[:len(s.flowOpens)-1]
		if s.firstPossible > len(s.keys) {
			s.firstPossible = len(s.keys)
		}
		s.flow--
	}
	s.allowKey = false
	s.push(k, s.pos, s.pos+1)
	s.pos++
	if s.flow > 0 {
		s.jsonLike = true
	}
	return nil
}

func (s *Scanner) fetchFlowEntry() error {
	if err := s.removeKey(); err != nil {
		return err
	}
	s.allowKey = true
	if s.flow > 0 {
		s.flowState[s.flow-1] &^= flowValueSeen
	}
	s.push(FlowEntry, s.pos, s.pos+1)
	s.pos++
	return nil
}

func (s *Scanner) fetchBlockEntry(col int) error {
	if s.flow == 0 {
		if !s.allowKey {
			return s.errAt(s.pos, Syntax, "block sequence entries are not allowed in this context")
		}
		if err := s.rollIndent(col, -1, BlockSequenceStart, s.pos, s.tabSince); err != nil {
			return err
		}
	}
	if err := s.removeKey(); err != nil {
		return err
	}
	s.allowKey = true
	s.tabSince = false
	s.push(BlockEntry, s.pos, s.pos+1)
	s.pos++
	return nil
}

func (s *Scanner) fetchKey(col int) error {
	if s.flow == 0 {
		if !s.allowKey {
			return s.errAt(s.pos, Syntax, "mapping keys are not allowed in this context")
		}
		if err := s.rollIndent(col, -1, BlockMappingStart, s.pos, s.tabSince); err != nil {
			return err
		}
	}
	if err := s.removeKey(); err != nil {
		return err
	}
	s.allowKey = s.flow == 0
	s.tabSince = false
	s.push(Key, s.pos, s.pos+1)
	s.pos++
	return nil
}

// Errors about a ":" are reported one character after it: the prefix up to the
// colon can still be valid (it may continue as a plain scalar), so the first
// character that cannot belong is the one that follows.
func (s *Scanner) fetchValue(col int) error {
	// After a plain key (or an alias), a flow collection may not touch the
	// colon: the entry has an empty value and the collection cannot follow it
	// without a separator [productions 147, 149]. An empty key is left alone.
	if c := s.at(s.pos + 1); s.flow > 0 && !s.jsonLike && (c == '[' || c == '{') && (s.lastKind == Scalar || s.lastKind == Alias) {
		return s.errAt(s.pos+1, Syntax, "a flow collection after ':' needs white space before it", true)
	}
	k := &s.keys[len(s.keys)-1]
	// A key found for this ':' may be at a lower flow level only if the flow
	// collection that was the key has just closed; the slot of this level then
	// holds it, because closing popped the inner slots.
	if k.possible && !k.multiline && s.column(s.pos)-k.col > 1024 {
		if k.required {
			return s.errAt(s.pos+1, Syntax, "could not find expected ':'", true)
		}
		k.possible = false
	}
	if k.possible && s.flow == 0 && k.afterTab && s.indent < k.col {
		return s.errAt(k.pos, Syntax, "a tab cannot precede a nested block collection", true)
	}
	if k.possible {
		s.insert(k.tokenNo-s.parsed+s.head, Token{Kind: Key, Start: k.pos, End: k.pos})
		if err := s.rollIndent(k.col, k.tokenNo, BlockMappingStart, k.pos, k.afterTab); err != nil {
			return err
		}
		k.possible = false
		s.allowKey = false
	} else {
		if s.flow > 0 && s.flowState[s.flow-1]&flowValueSeen != 0 {
			return s.errAt(s.pos+1, Syntax, "mapping values are not allowed in this context", true)
		}
		if s.flow == 0 {
			if !s.allowKey {
				return s.errAt(s.pos+1, Syntax, "mapping values are not allowed in this context", true)
			}
			if err := s.rollIndent(col, -1, BlockMappingStart, s.pos, s.tabSince); err != nil {
				return err
			}
		}
		s.allowKey = s.flow == 0
	}
	s.tabSince = false
	if s.flow > 0 {
		s.flowState[s.flow-1] |= flowValueSeen
	}
	s.push(Value, s.pos, s.pos+1)
	s.pos++
	return nil
}

func (s *Scanner) fetchAnchor(k Kind, col int) error {
	if err := s.saveKey(col); err != nil {
		return err
	}
	s.allowKey = false
	start := s.pos
	s.pos++
	n := s.pos
	for s.pos < len(s.src) && !s.blankz(s.pos) && !isFlowInd(s.src[s.pos]) {
		s.pos++
	}
	if s.pos == n {
		return s.errAt(s.pos, Syntax, "an anchor or alias needs a name")
	}
	if c := s.at(s.pos); s.pos < len(s.src) && (c == '[' || c == '{') {
		// Properties must be separated from the content they apply to [production 96].
		return s.errAt(s.pos, Syntax, "an anchor or alias must be followed by white space")
	}
	s.push(k, start, s.pos)
	return nil
}

// checkPercent verifies the '%' at pos is followed by two hexadecimal digits.
// The error points at the first character that is not one [production 39].
func (s *Scanner) checkPercent() error {
	for k := 1; k <= 2; k++ {
		if i := s.pos + k; i >= len(s.src) || !isHex(s.src[i]) {
			return s.errAt(i, Syntax, "a '%' in a tag needs two hexadecimal digits")
		}
	}
	return nil
}

func isTagChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '-', ';', '/', '?', ':', '@', '&', '=', '+', '$', '_', '.', '~', '*', '\'', '(', ')', '%', '!', '#':
		return true
	}
	return false
}

func (s *Scanner) fetchTag(col int) error {
	if err := s.saveKey(col); err != nil {
		return err
	}
	s.allowKey = false
	start := s.pos
	s.pos++
	b := s.src
	if s.at(s.pos) == '<' {
		s.pos++
		n := s.pos
		for s.pos < len(b) && b[s.pos] != '>' && (isTagChar(b[s.pos]) || isFlowInd(b[s.pos]) || b[s.pos] == ',') {
			if b[s.pos] == '%' {
				if err := s.checkPercent(); err != nil {
					return err
				}
			}
			s.pos++
		}
		if s.pos == n || s.pos >= len(b) || b[s.pos] != '>' {
			return s.errAt(s.pos, Syntax, "malformed verbatim tag")
		}
		if s.pos == n+1 && b[n] == '!' {
			// A verbatim tag is a local tag ("!" and a name) or a URI; a bare "!" is neither.
			return s.errAt(s.pos, Syntax, "a verbatim tag must be a local tag or a URI")
		}
		s.pos++
	} else {
		bangs := 0
		for s.pos < len(b) && isTagChar(b[s.pos]) {
			switch b[s.pos] {
			case '%':
				if err := s.checkPercent(); err != nil {
					return err
				}
			case '!':
				// A shorthand is "!suffix", "!!suffix" or "!handle!suffix" [productions 38-40].
				if bangs++; bangs > 1 {
					return s.errAt(s.pos, Syntax, "a tag shorthand has at most one handle")
				}
			}
			s.pos++
		}
	}
	if c := s.at(s.pos); s.pos < len(b) && (c == '[' || c == '{') {
		return s.errAt(s.pos, Syntax, "a tag must be followed by white space")
	}
	if !s.blankz(s.pos) && !(s.flow > 0 && isFlowInd(b[s.pos])) {
		return s.errAt(s.pos, Syntax, "a tag must be followed by white space")
	}
	s.push(Tag, start, s.pos)
	return nil
}

func (s *Scanner) fetchPlain(col int) error {
	if err := s.saveKey(col); err != nil {
		return err
	}
	s.allowKey = false
	b := s.src
	start := s.pos
	end := start
	endLS := s.lineStart
	ls := s.lineStart
	p := start
	indent := s.indent + 1
	for {
		segStart := p
		for p < len(b) {
			c := b[p]
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				break
			}
			if c == ':' {
				if p+1 >= len(b) || s.blankz(p+1) || (s.flow > 0 && isFlowInd(b[p+1])) {
					break
				}
			} else if s.flow > 0 && isFlowInd(c) {
				break
			}
			p++
		}
		if p == segStart {
			break
		}
		end, endLS = p, ls
		// Look ahead over white space and breaks to see if the scalar continues.
		crossed := false
		q := p
		for q < len(b) {
			c := b[q]
			if c == ' ' || c == '\t' {
				q++
			} else if isBreak(c) {
				if c == '\r' && q+1 < len(b) && b[q+1] == '\n' {
					q++
				}
				q++
				ls = q
				crossed = true
			} else {
				break
			}
		}
		if q >= len(b) {
			break
		}
		p = q
		if b[p] == '#' {
			break
		}
		if crossed {
			if p == ls && (s.docMarker(p) || p+2 < len(b) && b[p] == 0xEF && b[p+1] == 0xBB && b[p+2] == 0xBF) {
				break
			}
			// Indentation is spaces; a tab does not count towards it [production 69].
			sp := 0
			for ls+sp < p && b[ls+sp] == ' ' {
				sp++
			}
			if s.flow == 0 {
				if sp < indent {
					break
				}
			} else if s.indent >= 0 && sp <= s.indent {
				return s.errAt(p, Syntax, s.flowIndentMsg())
			}
		}
	}
	s.pos = end
	s.lineStart = endLS
	s.push(Scalar, start, end)
	s.tokens[len(s.tokens)-1].Style = Plain
	return nil
}

func (s *Scanner) fetchQuoted(double bool, col int) error {
	if err := s.saveKey(col); err != nil {
		return err
	}
	s.allowKey = false
	b := s.src
	start := s.pos
	s.quoteStart = start
	q := byte('\'')
	if double {
		q = '"'
	}
	p := start + 1
	for {
		if p >= len(b) {
			return s.errAt(len(b), Syntax, "the quoted scalar that starts at "+s.where(start)+" is never closed")
		}
		c := b[p]
		switch {
		case c == q:
			if !double && p+1 < len(b) && b[p+1] == '\'' {
				p += 2
				continue
			}
			p++
			goto done
		case c == '\\' && double:
			if p+1 >= len(b) {
				return s.errAt(len(b), Syntax, "the quoted scalar that starts at "+s.where(start)+" is never closed")
			}
			e := b[p+1]
			n := 0
			switch e {
			case '0', 'a', 'b', 't', '\t', 'n', 'v', 'f', 'r', 'e', ' ', '"', '/', '\\', 'N', '_', 'L', 'P':
				p += 2
				continue
			case 'x':
				n = 2
			case 'u':
				n = 4
			case 'U':
				n = 8
			case '\n', '\r':
				// Escaped line break: handled like a break below.
				p++
				if err := s.quotedBreak(&p); err != nil {
					return err
				}
				continue
			default:
				return s.errAt(p+1, Syntax, "invalid escape sequence in double-quoted scalar")
			}
			var v uint32
			for i := 0; i < n; i++ {
				if p+2+i >= len(b) || !isHex(b[p+2+i]) {
					return s.errAt(min(p+2+i, len(b)), Syntax, "invalid hexadecimal escape in double-quoted scalar")
				}
				v = v<<4 | uint32(hexVal(b[p+2+i]))
			}
			// A value past U+10FFFF or a lone surrogate names no character. Refuse it
			// here, where the quoted scalar is read, so every consumer of the tokens
			// (the document tree included) rejects it and none has to decode the
			// scalar to find out. An escaped high surrogate directly followed by an
			// escaped low one is a pair and names one character above the BMP.
			if v >= 0xD800 && v <= 0xDBFF {
				m, ok := LowSurrogateEscape(b[p+2+n:])
				if !ok {
					return s.errAt(p, Syntax, "escape is not a Unicode scalar value")
				}
				n += m
			} else if v > utf8.MaxRune || v >= 0xDC00 && v <= 0xDFFF {
				return s.errAt(p, Syntax, "escape is not a Unicode scalar value")
			}
			p += 2 + n
		case isBreak(c):
			if err := s.quotedBreak(&p); err != nil {
				return err
			}
		default:
			p++
		}
	}
done:
	for s.badSoft && s.badAt < p {
		// The character is legal inside quotes, but what follows it in the scalar
		// must still be valid, so continue right after it. Each byte is examined
		// once, so the total cost stays linear.
		next := s.badAt + 1
		if b[s.badAt] >= utf8.RuneSelf {
			_, size := utf8.DecodeRune(b[s.badAt:])
			next = s.badAt + size
		}
		s.checkEncoding(next)
	}
	s.pos = p
	st := SingleQuoted
	if double {
		st = DoubleQuoted
	}
	s.push(Scalar, start, p)
	s.tokens[len(s.tokens)-1].Style = st
	s.jsonLike = s.flow > 0
	return nil
}

// LowSurrogateEscape reports whether b starts with a \u or \U escape of a low
// surrogate, and how many bytes it takes. It is what must follow the escape of a
// high surrogate in a double-quoted scalar.
func LowSurrogateEscape(b []byte) (n int, ok bool) {
	if len(b) < 2 || b[0] != '\\' {
		return 0, false
	}
	switch b[1] {
	case 'u':
		n = 4
	case 'U':
		n = 8
	default:
		return 0, false
	}
	if len(b) < 2+n {
		return 0, false
	}
	var v uint32
	for _, c := range b[2 : 2+n] {
		if !isHex(c) {
			return 0, false
		}
		v = v<<4 | uint32(hexVal(c))
	}
	return 2 + n, v >= 0xDC00 && v <= 0xDFFF
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// hexVal is the value of the hexadecimal digit c; the caller has checked isHex.
func hexVal(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c <= 'F':
		return c - 'A' + 10
	}
	return c - 'a' + 10
}

// quotedBreak handles a line break inside a quoted scalar at *pp: it moves past
// the break and the next line's indentation, and checks the continuation line
// is indented enough and does not start with a document marker.
func (s *Scanner) quotedBreak(pp *int) error {
	b := s.src
	p := *pp
	for p < len(b) && isBreak(b[p]) {
		if b[p] == '\r' && p+1 < len(b) && b[p+1] == '\n' {
			p++
		}
		p++
		s.lineStart = p
		if p == s.lineStart && s.docMarker(p) {
			return s.errAt(p, Syntax, "document marker inside a quoted scalar")
		}
		// skip leading white space of the next line
		q := p
		for q < len(b) && (b[q] == ' ' || b[q] == '\t') {
			q++
		}
		if q < len(b) && isBreak(b[q]) {
			p = q
			continue
		}
		sp := 0
		for p+sp < q && b[p+sp] == ' ' {
			sp++
		}
		if q < len(b) && s.indent >= 0 && sp <= s.indent {
			return s.errAt(q, Syntax, "the quoted scalar that starts at "+s.where(s.quoteStart)+" is never closed: add the closing "+string(b[s.quoteStart])+" before this line, or indent this line past its parent")
		}
		p = q
		break
	}
	*pp = p
	return nil
}
