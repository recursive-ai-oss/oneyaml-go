package yaml

import (
	"strings"
	"unicode/utf8"

	"oneyaml.dev/go/internal/resolve"
)

// Kind is the kind of a Node.
type Kind uint32

// The kinds of node.
const (
	DocumentNode Kind = 1 << iota
	SequenceNode
	MappingNode
	ScalarNode
	AliasNode
)

// Style is a set of flags describing how a Node is, or is to be, written.
type Style uint32

// The style flags.
const (
	TaggedStyle Style = 1 << iota
	DoubleQuotedStyle
	SingleQuotedStyle
	LiteralStyle
	FoldedStyle
	FlowStyle
)

// Node represents an element in the YAML document hierarchy. While documents
// are typically read into and written from higher level types, Node is an
// intermediate representation that gives detailed control over the content.
//
// When a Node is written again its original text is not preserved: it is
// rendered afresh, keeping comments next to the data they describe.
//
// A Node may be a field or element of any value that is read or written:
//
//	var person struct {
//	        Name    string
//	        Address yamlv3.Node
//	}
//	err := yamlv3.Unmarshal(data, &person)
//
// or be read by itself:
//
//	var person yamlv3.Node
//	err := yamlv3.Unmarshal(data, &person)
type Node struct {
	// Kind says whether the node is a document, a mapping, a sequence, a scalar
	// or an alias to another node. The data type of a scalar is given by the
	// ShortTag and LongTag methods.
	Kind Kind

	// Style is the appearance of the node.
	Style Style

	// Tag is the YAML tag that defines the data type of the value. When reading
	// it is always the resolved tag, even if none was written. When writing, an
	// unset tag is implied from the node's properties, and a set one is only
	// written under TaggedStyle or when the implied tag differs from it.
	Tag string

	// Value is the unescaped and unquoted text of a scalar.
	Value string

	// Anchor is the name of the anchor of this node, which aliases refer to.
	Anchor string

	// Alias is the node an alias points to. Valid only when Kind is AliasNode.
	Alias *Node

	// Content holds the nodes contained in a document, a mapping (key, value,
	// key, value, ...) or a sequence.
	Content []*Node

	// HeadComment holds the comment lines before the node, with no blank line
	// between them and the node.
	HeadComment string

	// LineComment holds the comment at the end of the line the node is on.
	LineComment string

	// FootComment holds the comment lines after the node and before a blank line.
	FootComment string

	// Line and Column are the position of the node in the text it was read from,
	// counted from 1. They are ignored when writing.
	Line   int
	Column int

	// hook is the call to a user's UnmarshalYAML method this node was built for;
	// nil for every other node.
	hook *hookCall
}

// IsZero returns whether the node has all of its fields unset.
func (n *Node) IsZero() bool {
	return n.Kind == 0 && n.Style == 0 && n.Tag == "" && n.Value == "" && n.Anchor == "" && n.Alias == nil && n.Content == nil &&
		n.HeadComment == "" && n.LineComment == "" && n.FootComment == "" && n.Line == 0 && n.Column == 0
}

const (
	nullTag      = "!!null"
	boolTag      = "!!bool"
	strTag       = "!!str"
	intTag       = "!!int"
	floatTag     = "!!float"
	timestampTag = "!!timestamp"
	seqTag       = "!!seq"
	mapTag       = "!!map"
	binaryTag    = "!!binary"
	mergeTag     = "!!merge"

	longTagPrefix = "tag:yaml.org,2002:"
)

func shortTag(tag string) string {
	if strings.HasPrefix(tag, longTagPrefix) {
		return "!!" + tag[len(longTagPrefix):]
	}
	return tag
}

func longTag(tag string) string {
	if strings.HasPrefix(tag, "!!") {
		return longTagPrefix + tag[2:]
	}
	return tag
}

// resolveTag is the tag yaml.v3 gives a plain scalar with this text.
func resolveTag(value string) string {
	var buf [64]byte
	var r resolve.Result
	if len(value) <= len(buf) {
		r = resolve.V3(append(buf[:0], value...))
	} else {
		r = resolve.V3([]byte(value))
	}
	switch r.Kind {
	case resolve.Null:
		return nullTag
	case resolve.Bool:
		return boolTag
	case resolve.Int:
		return intTag
	case resolve.Float:
		return floatTag
	case resolve.Timestamp:
		return timestampTag
	}
	return strTag
}

// LongTag returns the long form of the tag that indicates the data type for the
// node. If the Tag field isn't set, one is computed from the node's properties.
func (n *Node) LongTag() string { return longTag(n.ShortTag()) }

// ShortTag returns the short form of the tag that indicates the data type for the
// node. If the Tag field isn't set, one is computed from the node's properties.
func (n *Node) ShortTag() string {
	if n.indicatedString() {
		return strTag
	}
	if n.Tag == "" || n.Tag == "!" {
		switch n.Kind {
		case MappingNode:
			return mapTag
		case SequenceNode:
			return seqTag
		case AliasNode:
			if n.Alias != nil {
				return n.Alias.ShortTag()
			}
		case ScalarNode:
			return resolveTag(n.Value)
		case 0:
			// The zero value is a null, which is convenient.
			if n.IsZero() {
				return nullTag
			}
		}
		return ""
	}
	return shortTag(n.Tag)
}

func (n *Node) indicatedString() bool {
	return n.Kind == ScalarNode &&
		(shortTag(n.Tag) == strTag ||
			(n.Tag == "" || n.Tag == "!") && n.Style&(SingleQuotedStyle|DoubleQuotedStyle|LiteralStyle|FoldedStyle) != 0)
}

// SetString sets the node to a string value and picks a pleasant style for its
// content.
func (n *Node) SetString(s string) {
	n.Kind = ScalarNode
	if utf8.ValidString(s) {
		n.Value = s
		n.Tag = strTag
	} else {
		n.Value = encodeBase64(s)
		n.Tag = binaryTag
	}
	if strings.Contains(n.Value, "\n") {
		n.Style = LiteralStyle
	}
}
