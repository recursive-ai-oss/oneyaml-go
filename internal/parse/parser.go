// Package parse turns scan tokens into YAML events with a pull API.
//
// An Event holds only integers: byte spans into the source and a few flags.
// Nothing is allocated per event, and scalar values are not materialised;
// AppendScalar decodes one into a caller-supplied buffer when it is wanted.
// Nesting is tracked with an explicit stack, so input depth never becomes Go
// recursion.
package parse

import (
	"io"

	"oneyaml.dev/go/internal/scan"
)

// Error is the error type of the scanner and the parser.
type Error = scan.Error

// Category classifies an Error.
type Category = scan.Category

// Error categories.
const (
	Syntax   = scan.Syntax
	Encoding = scan.Encoding
	Limit    = scan.Limit
)

// EventKind is the type of an Event.
type EventKind uint8

// Event kinds.
const (
	StreamStart EventKind = iota + 1
	StreamEnd
	DocumentStart
	DocumentEnd
	SequenceStart
	SequenceEnd
	MappingStart
	MappingEnd
	Scalar
	Alias
)

var eventNames = [...]string{
	StreamStart: "StreamStart", StreamEnd: "StreamEnd", DocumentStart: "DocumentStart",
	DocumentEnd: "DocumentEnd", SequenceStart: "SequenceStart", SequenceEnd: "SequenceEnd",
	MappingStart: "MappingStart", MappingEnd: "MappingEnd", Scalar: "Scalar", Alias: "Alias",
}

// String returns the kind name.
func (k EventKind) String() string {
	if int(k) < len(eventNames) && eventNames[k] != "" {
		return eventNames[k]
	}
	return "Unknown"
}

// Event is one parse event. All offsets index the source given to the parser.
//
// Start and End:
//   - Scalar: the whole presentation, quotes and block header included (see
//     scan.Token). An empty node has Start == End.
//   - Alias: "*name"; the name is src[Start+1:End].
//   - SequenceStart, MappingStart: the "[" or "{" for flow collections, a zero
//     width span at the first "-" or key for block collections, and a zero
//     width span for the implicit mapping of a "[a: b]" pair.
//   - SequenceEnd, MappingEnd: the closing "]" or "}", or a zero width span
//     where the block ended.
//   - DocumentStart, DocumentEnd: the "---" or "..." marker when Explicit, else
//     a zero width span where the document begins or ends.
//   - StreamStart, StreamEnd: zero width.
//
// AnchorStart:AnchorEnd is the anchor name (without "&") and TagStart:TagEnd
// the tag exactly as written, both empty when absent. Use AppendTag for the
// resolved tag. Properties on an Alias never occur.
type Event struct {
	Kind     EventKind
	Style    scan.Style // Scalar only
	Flow     bool       // collection written in flow style ("[...]", "{...}")
	Pair     bool       // MappingStart: the implicit single-pair mapping inside a flow sequence
	Explicit bool       // DocumentStart/DocumentEnd: the marker is present

	Start, End             int
	AnchorStart, AnchorEnd int
	TagStart, TagEnd       int

	// Indent is the content indentation of a block scalar (see scan.Token).
	Indent int

	// VersionMajor and VersionMinor are set on a DocumentStart that follows a
	// %YAML directive, and zero otherwise.
	VersionMajor, VersionMinor int
}

// HasAnchor reports whether the event carries an anchor.
func (e *Event) HasAnchor() bool { return e.AnchorEnd > e.AnchorStart }

// HasTag reports whether the event carries a tag.
func (e *Event) HasTag() bool { return e.TagEnd > e.TagStart }

type state uint8

const (
	sStreamStart state = iota
	sDocStart
	sDocContent
	sDocEnd
	sBlockNode
	sBlockNodeOrIndentlessSeq
	sFlowNode
	sBlockSeqFirstEntry
	sBlockSeqEntry
	sIndentlessSeqEntry
	sBlockMapFirstKey
	sBlockMapKey
	sBlockMapValue
	sFlowSeqFirstEntry
	sFlowSeqEntry
	sFlowSeqEntryMapKey
	sFlowSeqEntryMapValue
	sFlowSeqEntryMapEnd
	sFlowMapFirstKey
	sFlowMapKey
	sFlowMapValue
	sFlowMapEmptyValue
	sEnd
)

type tagDirective struct{ hs, he, ps, pe int }

// Parser is a pull parser. The zero value is not usable; call New. A Parser
// can be reused with Reset to keep its buffers.
type Parser struct {
	sc     scan.Scanner
	src    []byte
	state  state
	states []state
	tags   []tagDirective

	started      bool // a document has been seen
	explicitEnd  bool // the previous document ended with "..."
	err          error
	version      [2]int
	sawDirective bool
	markerEnd    int // end offset of the last "..." marker
}

// New returns a Parser for src. maxDepth bounds the nesting of block and flow
// collections: 0 selects the default (scan.DefaultMaxDepth), negative disables
// the limit.
func New(src []byte, maxDepth int) *Parser {
	p := &Parser{}
	p.Reset(src, maxDepth)
	return p
}

// Reset prepares p to parse src, keeping allocated buffers.
func (p *Parser) Reset(src []byte, maxDepth int) {
	p.sc.Reset(src, maxDepth)
	p.src = src
	p.state = sStreamStart
	p.states = p.states[:0]
	p.tags = p.tags[:0]
	p.started, p.explicitEnd, p.sawDirective = false, false, false
	p.err = nil
}

// Source returns the slice being parsed.
func (p *Parser) Source() []byte { return p.src }

// expectedKeyMsg words the error for a token that cannot start a mapping key. A
// tab among the leading white space is the usual cause, and says so.
func (p *Parser) expectedKeyMsg(tok int) string {
	ls := tok
	for ls > 0 && p.src[ls-1] != '\n' && p.src[ls-1] != '\r' {
		ls--
	}
	for i := ls; i < tok; i++ {
		if p.src[i] == '\t' {
			return "a tab cannot be used for indentation; indent with spaces"
		}
	}
	return "did not find the expected key; check the indentation of this line"
}

func (p *Parser) errAt(off int, msg string) error {
	return scan.NewError(p.src, off, Syntax, msg)
}

func (p *Parser) push(s state) { p.states = append(p.states, s) }

func (p *Parser) pop() {
	n := len(p.states) - 1
	p.state = p.states[n]
	p.states = p.states[:n]
}

// Next returns the next event. After the StreamEnd event it returns io.EOF.
// After an error it keeps returning that error. The events delivered before
// an error follow the specification's document structure: a document ends
// only at its "..." marker, at the next document start or at the end of the
// stream, never because of a fault.
func (p *Parser) Next() (Event, error) {
	if p.err != nil {
		return Event{}, p.err
	}
	if p.state == sEnd {
		return Event{}, io.EOF
	}
	ev, err := p.step()
	if err != nil {
		p.err = err
		return Event{}, err
	}
	return ev, nil
}

func (p *Parser) peek() (*scan.Token, error) { return p.sc.Peek() }

// skipFinal consumes the current token for an event that is returned at once.
// If the scanner failed on the token after it, the event is withheld: the
// reference notation only reports a token once its successor was read.
func (p *Parser) skipFinal() error {
	p.sc.Skip()
	return p.sc.Pending()
}

func hasBreak(b []byte) bool {
	for _, c := range b {
		if c == '\n' || c == '\r' {
			return true
		}
	}
	return false
}

// skipBlockEnd consumes a BlockEnd token for an end event. A directive right
// after it means the document was never closed, which is an error that must
// come before the end event.
func (p *Parser) skipBlockEnd() error {
	if err := p.skipFinal(); err != nil {
		return err
	}
	t, err := p.peek()
	if err == nil && t.Kind == scan.Directive {
		return p.errAt(t.Start, "a directive must follow a document end marker \"...\"")
	}
	return nil
}

// startsLine reports whether only spaces precede offset i on its line.
func (p *Parser) startsLine(i int) bool {
	for i > 0 {
		i--
		switch p.src[i] {
		case ' ':
		case '\n', '\r':
			return true
		default:
			return false
		}
	}
	return true
}

func emptyScalar(at int) Event {
	return Event{Kind: Scalar, Style: scan.Plain, Start: at, End: at}
}

// props holds the anchor and tag read before a node.
type props struct{ as, ae, ts, te int }

func (pr *props) set() bool { return pr.ae > pr.as || pr.te > pr.ts }

func (p *Parser) step() (Event, error) {
	for {
		switch p.state {
		case sStreamStart:
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			if t.Kind != scan.StreamStart {
				return Event{}, p.errAt(t.Start, "did not find the expected stream start")
			}
			ev := Event{Kind: StreamStart, Start: t.Start, End: t.End}
			if err := p.skipFinal(); err != nil {
				return Event{}, err
			}
			p.state = sDocStart
			return ev, nil

		case sDocStart:
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			for t.Kind == scan.DocumentEnd {
				// A stray "..." between documents is allowed.
				p.markerEnd = t.End
				p.sc.Skip()
				p.explicitEnd = true
				if t, err = p.peek(); err != nil {
					return Event{}, err
				}
			}
			if p.explicitEnd && t.Kind != scan.StreamEnd && !hasBreak(p.src[p.markerEnd:t.Start]) {
				return Event{}, p.errAt(t.Start, "unexpected content after the document end marker")
			}
			allowBare := !p.started || p.explicitEnd
			switch {
			case allowBare && t.Kind != scan.Directive && t.Kind != scan.DocumentStart && t.Kind != scan.StreamEnd:
				p.tags = p.tags[:0]
				p.started = true
				p.version = [2]int{}
				p.push(sDocEnd)
				p.state = sBlockNode
				return Event{Kind: DocumentStart, Start: t.Start, End: t.Start}, nil
			case t.Kind != scan.StreamEnd:
				if t.Kind == scan.Directive && p.started && !p.explicitEnd {
					return Event{}, p.errAt(t.Start, "a directive must follow a document end marker \"...\"")
				}
				if err := p.directives(); err != nil {
					return Event{}, err
				}
				if t, err = p.peek(); err != nil {
					return Event{}, err
				}
				if t.Kind != scan.DocumentStart {
					return Event{}, p.errAt(t.Start, "did not find the expected document start \"---\"")
				}
				ev := Event{Kind: DocumentStart, Explicit: true, Start: t.Start, End: t.End,
					VersionMajor: p.version[0], VersionMinor: p.version[1]}
				p.started = true
				if err := p.skipFinal(); err != nil {
					return Event{}, err
				}
				p.push(sDocEnd)
				p.state = sDocContent
				return ev, nil
			default:
				ev := Event{Kind: StreamEnd, Start: t.Start, End: t.End}
				if err := p.skipFinal(); err != nil {
					return Event{}, err
				}
				p.state = sEnd
				return ev, nil
			}

		case sDocContent:
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			switch t.Kind {
			case scan.Directive:
				return Event{}, p.errAt(t.Start, "a directive must follow a document end marker \"...\"")
			case scan.DocumentStart, scan.DocumentEnd, scan.StreamEnd:
				p.pop()
				return emptyScalar(t.Start), nil
			}
			p.state = sBlockNode

		case sDocEnd:
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			ev := Event{Kind: DocumentEnd, Start: t.Start, End: t.Start}
			p.explicitEnd = false
			switch t.Kind {
			case scan.DocumentEnd:
				ev.Explicit, ev.End = true, t.End
				p.explicitEnd = true
				p.markerEnd = t.End
				if err := p.skipFinal(); err != nil {
					return Event{}, err
				}
			case scan.DocumentStart, scan.StreamEnd:
			default:
				// Anything else cannot follow the root node. The document stays open.
				return Event{}, p.errAt(t.Start, "did not find the expected end of the document")
			}
			p.state = sDocStart
			return ev, nil

		case sBlockNode:
			ev, ok, err := p.node(true, false)
			if err != nil || ok {
				return ev, err
			}
		case sBlockNodeOrIndentlessSeq:
			ev, ok, err := p.node(true, true)
			if err != nil || ok {
				return ev, err
			}
		case sFlowNode:
			ev, ok, err := p.node(false, false)
			if err != nil || ok {
				return ev, err
			}

		case sBlockSeqFirstEntry, sBlockSeqEntry:
			if p.state == sBlockSeqFirstEntry {
				p.sc.Skip()
			}
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			switch t.Kind {
			case scan.BlockEntry:
				p.sc.Skip()
				if t, err = p.peek(); err != nil {
					return Event{}, err
				}
				if t.Kind != scan.BlockEntry && t.Kind != scan.BlockEnd {
					p.push(sBlockSeqEntry)
					p.state = sBlockNode
					continue
				}
				p.state = sBlockSeqEntry
				return emptyScalar(t.Start), nil
			case scan.BlockEnd:
				ev := Event{Kind: SequenceEnd, Start: t.Start, End: t.Start}
				if err := p.skipBlockEnd(); err != nil {
					return Event{}, err
				}
				p.pop()
				return ev, nil
			}
			return Event{}, p.errAt(t.Start, "expected '- ' to continue the sequence; check the indentation of this line")

		case sIndentlessSeqEntry:
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			if t.Kind != scan.BlockEntry {
				ev := Event{Kind: SequenceEnd, Start: t.Start, End: t.Start}
				p.pop()
				return ev, nil
			}
			p.sc.Skip()
			if t, err = p.peek(); err != nil {
				return Event{}, err
			}
			switch t.Kind {
			case scan.BlockEntry, scan.Key, scan.Value, scan.BlockEnd:
				p.state = sIndentlessSeqEntry
				return emptyScalar(t.Start), nil
			}
			p.push(sIndentlessSeqEntry)
			p.state = sBlockNode

		case sBlockMapFirstKey, sBlockMapKey:
			if p.state == sBlockMapFirstKey {
				p.sc.Skip()
			}
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			switch t.Kind {
			case scan.Key:
				p.sc.Skip()
				if t, err = p.peek(); err != nil {
					return Event{}, err
				}
				switch t.Kind {
				case scan.Key, scan.Value, scan.BlockEnd:
					p.state = sBlockMapValue
					return emptyScalar(t.Start), nil
				}
				p.push(sBlockMapValue)
				p.state = sBlockNodeOrIndentlessSeq
				continue
			case scan.Value:
				// A value with no key before it: the key is empty.
				p.state = sBlockMapValue
				return emptyScalar(t.Start), nil
			case scan.BlockEnd:
				ev := Event{Kind: MappingEnd, Start: t.Start, End: t.Start}
				if err := p.skipBlockEnd(); err != nil {
					return Event{}, err
				}
				p.pop()
				return ev, nil
			}
			// A token at the start of a line may still be the beginning of an implicit
			// key, so it is acceptable up to its last character and the first character
			// that cannot belong is the one after it.
			off := t.Start
			if t.End > t.Start && p.startsLine(t.Start) {
				off = t.End
			}
			return Event{}, p.errAt(off, p.expectedKeyMsg(t.Start))

		case sBlockMapValue:
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			if t.Kind == scan.Value {
				p.sc.Skip()
				if t, err = p.peek(); err != nil {
					return Event{}, err
				}
				switch t.Kind {
				case scan.Key, scan.Value, scan.BlockEnd:
				default:
					p.push(sBlockMapKey)
					p.state = sBlockNodeOrIndentlessSeq
					continue
				}
			}
			p.state = sBlockMapKey
			return emptyScalar(t.Start), nil

		case sFlowSeqFirstEntry, sFlowSeqEntry:
			first := p.state == sFlowSeqFirstEntry
			if first {
				p.sc.Skip()
			}
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			if t.Kind != scan.FlowSequenceEnd {
				if !first {
					if t.Kind != scan.FlowEntry {
						return Event{}, p.errAt(t.Start, "expected ',' or ']' after a flow sequence entry (is a closing ']' missing?)")
					}
					p.sc.Skip()
					if t, err = p.peek(); err != nil {
						return Event{}, err
					}
				}
				if t.Kind == scan.Key || t.Kind == scan.Value {
					ev := Event{Kind: MappingStart, Flow: true, Pair: true, Start: t.Start, End: t.Start}
					p.state = sFlowSeqEntryMapKey
					if t.Kind == scan.Key {
						p.sc.Skip()
					}
					return ev, nil
				}
				if t.Kind != scan.FlowSequenceEnd {
					p.push(sFlowSeqEntry)
					p.state = sFlowNode
					continue
				}
			}
			ev := Event{Kind: SequenceEnd, Flow: true, Start: t.Start, End: t.End}
			if err := p.skipFinal(); err != nil {
				return Event{}, err
			}
			p.pop()
			return ev, nil

		case sFlowSeqEntryMapKey:
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			switch t.Kind {
			case scan.Value, scan.FlowEntry, scan.FlowSequenceEnd:
				p.state = sFlowSeqEntryMapValue
				return emptyScalar(t.Start), nil
			}
			p.push(sFlowSeqEntryMapValue)
			p.state = sFlowNode

		case sFlowSeqEntryMapValue:
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			if t.Kind == scan.Value {
				p.sc.Skip()
				if t, err = p.peek(); err != nil {
					return Event{}, err
				}
				if t.Kind != scan.FlowEntry && t.Kind != scan.FlowSequenceEnd {
					p.push(sFlowSeqEntryMapEnd)
					p.state = sFlowNode
					continue
				}
			}
			p.state = sFlowSeqEntryMapEnd
			return emptyScalar(t.Start), nil

		case sFlowSeqEntryMapEnd:
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			p.state = sFlowSeqEntry
			return Event{Kind: MappingEnd, Flow: true, Pair: true, Start: t.Start, End: t.Start}, nil

		case sFlowMapFirstKey, sFlowMapKey:
			first := p.state == sFlowMapFirstKey
			if first {
				p.sc.Skip()
			}
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			if t.Kind != scan.FlowMappingEnd {
				if !first {
					if t.Kind != scan.FlowEntry {
						return Event{}, p.errAt(t.Start, "expected ',' or '}' after a flow mapping entry (is a closing '}' missing?)")
					}
					p.sc.Skip()
					if t, err = p.peek(); err != nil {
						return Event{}, err
					}
				}
				if t.Kind == scan.Key || t.Kind == scan.Value {
					if t.Kind == scan.Key {
						p.sc.Skip()
						if t, err = p.peek(); err != nil {
							return Event{}, err
						}
					}
					switch t.Kind {
					case scan.Value, scan.FlowEntry, scan.FlowMappingEnd:
						p.state = sFlowMapValue
						return emptyScalar(t.Start), nil
					}
					p.push(sFlowMapValue)
					p.state = sFlowNode
					continue
				}
				if t.Kind != scan.FlowMappingEnd {
					p.push(sFlowMapEmptyValue)
					p.state = sFlowNode
					continue
				}
			}
			ev := Event{Kind: MappingEnd, Flow: true, Start: t.Start, End: t.End}
			if err := p.skipFinal(); err != nil {
				return Event{}, err
			}
			p.pop()
			return ev, nil

		case sFlowMapValue, sFlowMapEmptyValue:
			t, err := p.peek()
			if err != nil {
				return Event{}, err
			}
			if p.state == sFlowMapValue && t.Kind == scan.Value {
				p.sc.Skip()
				if t, err = p.peek(); err != nil {
					return Event{}, err
				}
				if t.Kind != scan.FlowEntry && t.Kind != scan.FlowMappingEnd {
					p.push(sFlowMapKey)
					p.state = sFlowNode
					continue
				}
			}
			p.state = sFlowMapKey
			return emptyScalar(t.Start), nil

		default:
			return Event{}, p.errAt(0, "parser is in an invalid state")
		}
	}
}

// node parses a node and its properties. ok is false when it only changed
// state (it never does today; kept for symmetry with the loop in step).
func (p *Parser) node(block, indentless bool) (Event, bool, error) {
	t, err := p.peek()
	if err != nil {
		return Event{}, false, err
	}
	if t.Kind == scan.Alias {
		ev := Event{Kind: Alias, Start: t.Start, End: t.End}
		if err := p.skipFinal(); err != nil {
			return Event{}, false, err
		}
		p.pop()
		return ev, true, nil
	}
	var pr props
	if t.Kind == scan.Anchor {
		pr.as, pr.ae = t.Start+1, t.End
		p.sc.Skip()
		if t, err = p.peek(); err != nil {
			return Event{}, false, err
		}
		if t.Kind == scan.Anchor {
			return Event{}, false, p.errAt(t.Start, "a node cannot have two anchors")
		}
		if t.Kind == scan.Tag {
			pr.ts, pr.te = t.Start, t.End
			p.sc.Skip()
			if t, err = p.peek(); err != nil {
				return Event{}, false, err
			}
		}
	} else if t.Kind == scan.Tag {
		pr.ts, pr.te = t.Start, t.End
		p.sc.Skip()
		if t, err = p.peek(); err != nil {
			return Event{}, false, err
		}
		if t.Kind == scan.Tag {
			return Event{}, false, p.errAt(t.Start, "a node cannot have two tags")
		}
		if t.Kind == scan.Anchor {
			pr.as, pr.ae = t.Start+1, t.End
			p.sc.Skip()
			if t, err = p.peek(); err != nil {
				return Event{}, false, err
			}
			if t.Kind == scan.Anchor {
				return Event{}, false, p.errAt(t.Start, "a node cannot have two anchors")
			}
		}
	}
	if t.Kind == scan.Alias && pr.set() {
		return Event{}, false, p.errAt(t.Start, "an alias cannot have an anchor or a tag")
	}
	if pr.te > pr.ts {
		if err := p.checkTag(pr.ts, pr.te); err != nil {
			return Event{}, false, err
		}
	}
	ev := Event{AnchorStart: pr.as, AnchorEnd: pr.ae, TagStart: pr.ts, TagEnd: pr.te, Start: t.Start, End: t.End}
	switch {
	case indentless && t.Kind == scan.BlockEntry:
		ev.Kind, ev.End = SequenceStart, t.Start
		p.state = sIndentlessSeqEntry
	case t.Kind == scan.Scalar:
		ev.Kind, ev.Style, ev.Indent = Scalar, t.Style, t.Indent
		if err := p.skipFinal(); err != nil {
			return Event{}, false, err
		}
		p.pop()
	case t.Kind == scan.FlowSequenceStart:
		ev.Kind, ev.Flow = SequenceStart, true
		p.state = sFlowSeqFirstEntry
	case t.Kind == scan.FlowMappingStart:
		ev.Kind, ev.Flow = MappingStart, true
		p.state = sFlowMapFirstKey
	case block && t.Kind == scan.BlockSequenceStart:
		ev.Kind, ev.End = SequenceStart, t.Start
		p.state = sBlockSeqFirstEntry
	case block && t.Kind == scan.BlockMappingStart:
		ev.Kind, ev.End = MappingStart, t.Start
		p.state = sBlockMapFirstKey
	case pr.set():
		ev = emptyScalar(t.Start)
		ev.AnchorStart, ev.AnchorEnd, ev.TagStart, ev.TagEnd = pr.as, pr.ae, pr.ts, pr.te
		p.pop()
	default:
		return Event{}, false, p.errAt(t.Start, "did not find the expected node content")
	}
	return ev, true, nil
}
