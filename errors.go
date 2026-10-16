package oneyaml

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"oneyaml.dev/go/internal/bind"
)

// Sentinel errors for the kinds of failure a document operation can report.
// Match them with errors.Is.
var (
	// ErrNotFound means a pointer or path addressed nothing.
	ErrNotFound = errors.New("oneyaml: path not found")
	// ErrNilDocument means a method that changes or decodes a document was called on
	// a nil *Document, which is what a failed Parse returns.
	ErrNilDocument = errors.New("oneyaml: nil Document")
	// ErrAliasWrite means an edit would write through an alias, or, with
	// MergeKeys, into the "<<" merge source a key comes from.
	ErrAliasWrite = errors.New("oneyaml: write through an alias")
	// ErrAmbiguousKey means a key occurs more than once in its mapping, or, with
	// MergeKeys, a "<<" merge brings it in from two places of one source, so
	// Document.Get, Node.Find or an edit cannot choose one value.
	ErrAmbiguousKey = errors.New("oneyaml: ambiguous key")
	// ErrTestFailed means a patch "test" operation did not match.
	ErrTestFailed = errors.New("oneyaml: patch test failed")
	// ErrAnchorInUse means an anchor cannot be removed while an alias uses it.
	ErrAnchorInUse = errors.New("oneyaml: anchor in use")
)

// ErrUnknownField is the kind of a [*TypeError] for a key that no field of the
// target struct has, which only happens under [KnownFields]. Match it with
// errors.Is; the error is found even when it is one of several type errors.
var ErrUnknownField = errors.New("oneyaml: unknown field")

// Each error type answers errors.Is for its kind, so one call tells what went
// wrong and errors.As gives the details: a SyntaxError matches ErrSyntax and the
// sentinel of its Category, a LimitError matches ErrLimit and the sentinel of its
// Limit, a TypeError matches its Kind, and an EditError matches its Kind, which
// may itself be a LimitError.

// Sentinels for the kinds of a [*SyntaxError].
var (
	// ErrSyntax matches every [*SyntaxError], whatever its Category.
	ErrSyntax = errors.New("oneyaml: syntax error")
	// ErrEncoding matches a SyntaxError of [CategoryEncoding].
	ErrEncoding = errors.New("oneyaml: invalid encoding")
	// ErrDuplicateKey matches a SyntaxError of [CategoryDuplicateKey].
	ErrDuplicateKey = errors.New("oneyaml: duplicate mapping key")
)

// Sentinels for the limits of a [*LimitError].
var (
	// ErrLimit matches every [*LimitError], whichever limit it is.
	ErrLimit = errors.New("oneyaml: limit exceeded")
	// ErrMaxDepth matches a LimitError of [LimitMaxDepth].
	ErrMaxDepth = errors.New("oneyaml: nesting depth limit exceeded")
	// ErrMaxAliasNodes matches a LimitError of [LimitMaxAliasNodes].
	ErrMaxAliasNodes = errors.New("oneyaml: alias expansion node limit exceeded")
	// ErrMaxAliasBytes matches a LimitError of [LimitMaxAliasBytes].
	ErrMaxAliasBytes = errors.New("oneyaml: alias expansion size limit exceeded")
	// ErrMaxInputBytes matches a LimitError of [LimitMaxInputBytes].
	ErrMaxInputBytes = errors.New("oneyaml: input size limit exceeded")
	// ErrMaxScalarBytes matches a LimitError of [LimitMaxScalarBytes].
	ErrMaxScalarBytes = errors.New("oneyaml: scalar size limit exceeded")
	// ErrMaxDocuments matches a LimitError of [LimitMaxDocuments].
	ErrMaxDocuments = errors.New("oneyaml: document count limit exceeded")
	// ErrMaxNumberDigits matches a LimitError of [LimitMaxNumberDigits].
	ErrMaxNumberDigits = errors.New("oneyaml: number digit limit exceeded")
	// ErrMaxPatchWorkRatio matches a LimitError of [LimitMaxPatchWorkRatio].
	ErrMaxPatchWorkRatio = errors.New("oneyaml: patch work limit exceeded")
)

// Category is the kind of a [SyntaxError]. The set is closed: a limit that is
// hit is a [*LimitError], not a syntax error. Switch on it, or match with
// errors.Is the sentinel ErrSyntax, ErrEncoding or ErrDuplicateKey.
type Category string

// The values of [SyntaxError.Category].
const (
	// CategorySyntax is any violation of the YAML grammar, including characters
	// that YAML does not allow and an alias to an anchor that was never defined.
	CategorySyntax Category = "syntax"
	// CategoryEncoding is input that is not valid UTF-8, UTF-16 or UTF-32.
	CategoryEncoding Category = "encoding"
	// CategoryDuplicateKey is a mapping that has the same key twice. Key,
	// FirstLine and FirstColumn say which key and where it first appeared.
	CategoryDuplicateKey Category = "duplicate-key"
)

// SyntaxError reports YAML that is not well formed.
type SyntaxError struct {
	// Line and Column are 1-based, counted in characters; Offset is the byte offset.
	Line, Column, Offset int
	// Category is the kind of error: one of the Category constants. Switch on it
	// or match it with errors.Is, rather than reading Msg.
	Category Category
	// Msg describes the problem in terms of what the input got wrong.
	Msg string
	// Key, FirstLine and FirstColumn describe a CategoryDuplicateKey error: the
	// text of the repeated key and the 1-based position of its first occurrence.
	// They are zero when the first occurrence is not known, which happens when
	// two keys are equal only after conversion to the target type.
	Key                    string
	FirstLine, FirstColumn int
}

// Is reports whether target is [ErrSyntax] or the sentinel of the Category of e.
func (e *SyntaxError) Is(target error) bool {
	switch target {
	case ErrSyntax:
		return true
	case ErrEncoding:
		return e.Category == CategoryEncoding
	case ErrDuplicateKey:
		return e.Category == CategoryDuplicateKey
	}
	return false
}

// Error returns the message with its line and column.
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("oneyaml: line %d, column %d: %s", e.Line, e.Column, e.Msg)
}

// TypeError reports a value that cannot be converted: a YAML value that does not
// fit its Go target when reading, or a Go value with no YAML form when writing.
//
// When reading, a mismatch does not stop the decode. The value that does not fit
// is skipped and its target is left as it was (an element of a slice or array is
// left zero, and a map entry is not added); the rest of the document is still
// read, and every mismatch is reported, up to the bound below. The error returned describes the first
// one and More holds the others, in input order, so errors.As finds a
// *TypeError whether there was one problem or many. At most 100 mismatches are
// collected; the read stops at the 100th. The first mismatch is always reported
// whole, and the paths of the ones after it together take no more than the
// larger of the input size and 1 MiB: when the next would pass that, collecting
// stops and the later mismatches are left out, so a document with very long
// keys (keys reached through aliases, say) may report fewer than 100. A value
// that no hook can take (a type whose UnmarshalYAML has the wrong form, or a
// Node used as a target) is a mismatch like any other. Syntax errors, limit
// errors and errors from your own hooks stop the read at once and are returned
// alone, without the mismatches collected before them. When a hook returns the
// error of a Node.Decode it made (or of the function a func-style hook is
// given), the mismatches in it are reported as if the hook were not there,
// every one of them, at any depth of nested hooks: each mismatch is reported at
// the line and column of its own value. Its path is the path of the hook's value followed by the path
// inside the node the hook decoded, so a hook that decodes the node it was given
// reports the same paths as a read without the hook. Inside the hook, the error
// of its own Node.Decode holds the paths inside that node; a path of more than
// 4 KiB that runs through hooks nested in one another is completed when the
// outermost read ends, so it stays incomplete in an error the hook keeps or
// wraps (with %w, say) instead of returning it. An error a hook returns from
// reading other text, such as an Unmarshal of its own input, is reported at the
// hook's value with the hook's type as GoType; the position and path inside the
// other text are part of Msg.
//
// Error shortens a Path longer than 512 bytes to its first and last 256 bytes
// joined by "..."; the Path field always holds the whole pointer.
type TypeError struct {
	// Path is the RFC 6901 pointer to the value, empty for the root.
	Path string
	// Line and Column locate the value in the input; zero when writing.
	Line, Column int
	// GoType is the Go type involved: the target of a value that does not fit,
	// or the type that has no YAML form. It is nil when no type was involved.
	GoType reflect.Type
	// Msg describes the problem.
	Msg string
	// Err is the error one of your hooks returned, such as an UnmarshalYAML,
	// MarshalYAML or MarshalText method, if that was the cause. errors.Is and
	// errors.As look inside it.
	Err error
	// Kind is the sentinel error for the cause when there is one, for now only
	// [ErrUnknownField]. errors.Is matches it.
	Kind error
	// More holds the mismatches after this one when a read found several. Each
	// has an empty More.
	More []*TypeError
	// sc, from and pre are set on the errors of a decode made from the Node a hook
	// was given, so that the read around the hook can tell them from an error about
	// other text. sc is the call the Node was handed to and from the node that was
	// decoded. pre holds, outermost first, the path text of the hooks around a
	// path too long to copy at every level; the outermost read puts it in front of
	// Path once.
	sc   *bind.Scope
	from int32
	pre  *pathLink
}

// All returns this error and the ones in More, in input order.
func (e *TypeError) All() []*TypeError {
	if len(e.More) == 0 {
		return []*TypeError{e}
	}
	return append([]*TypeError{e.single()}, e.More...)
}

// single returns e without its More list.
func (e *TypeError) single() *TypeError {
	c := *e
	c.More = nil
	return &c
}

func (e *TypeError) line() string {
	loc := ""
	if e.Line > 0 {
		loc = fmt.Sprintf("line %d, column %d: ", e.Line, e.Column)
	}
	if e.Path != "" {
		return fmt.Sprintf("%s%s: %s", loc, shortPath(e.Path), e.Msg)
	}
	return loc + e.Msg
}

// Error returns the message with the position and path of the value. With
// several mismatches it lists them one to a line.
func (e *TypeError) Error() string {
	if len(e.More) == 0 {
		return "oneyaml: " + e.line()
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "oneyaml: %d type errors:", len(e.More)+1)
	sb.WriteString("\n  " + e.line())
	for _, m := range e.More {
		sb.WriteString("\n  " + m.line())
	}
	return sb.String()
}

// Unwrap returns the error from a user hook, if there was one.
func (e *TypeError) Unwrap() error { return e.Err }

// Is reports whether target is the Kind of this error or of one in More.
func (e *TypeError) Is(target error) bool {
	if target == nil {
		return false
	}
	if e.Kind == target {
		return true
	}
	for _, m := range e.More {
		if m.Kind == target {
			return true
		}
	}
	return false
}

// Limit names one of the bounds of a [LimitSet] by the name of its field. Its
// values are the Limit constants, and each has a sentinel that errors.Is
// matches, such as [ErrMaxDepth] for [LimitMaxDepth].
type Limit string

// The values of [LimitError.Limit], one for each field of [LimitSet].
const (
	// LimitMaxDepth is the deepest nesting of collections.
	LimitMaxDepth Limit = "MaxDepth"
	// LimitMaxAliasNodes is the nodes that alias expansion may produce.
	LimitMaxAliasNodes Limit = "MaxAliasNodes"
	// LimitMaxAliasBytes is the scalar bytes that alias expansion may produce.
	// A patch that copies more than this much text also reports it.
	LimitMaxAliasBytes Limit = "MaxAliasBytes"
	// LimitMaxInputBytes is the largest input accepted. A patch that is too long,
	// and an edit or patch that would make the document too large, also report
	// it, as does the fixed 2 GiB bound on the size of a Document.
	LimitMaxInputBytes Limit = "MaxInputBytes"
	// LimitMaxScalarBytes is the longest single scalar accepted.
	LimitMaxScalarBytes Limit = "MaxScalarBytes"
	// LimitMaxDocuments is the most documents accepted in one stream.
	LimitMaxDocuments Limit = "MaxDocuments"
	// LimitMaxNumberDigits is the most digits of a number that is converted.
	LimitMaxNumberDigits Limit = "MaxNumberDigits"
	// LimitMaxPatchWorkRatio is the text that the edits of one patch may read again.
	LimitMaxPatchWorkRatio Limit = "MaxPatchWorkRatio"
)

// LimitError reports that a limit was exceeded.
//
// Match it with errors.Is against [ErrLimit] for any limit or, for one, the
// sentinel such as [ErrMaxDepth]; read the fields with errors.As.
type LimitError struct {
	// Limit is the bound that was exceeded, named by its [LimitSet] field. Switch
	// on it rather than on What.
	Limit Limit
	// What says in words what the bound applies to, such as "alias expansion
	// nodes" or "patch output size", for messages. Its wording may change.
	What string
	// Max is the bound that was exceeded.
	Max int
	// Option names the option and the [LimitSet] field that raise the bound, in
	// the form "Limits (MaxDepth)"; NoLimits removes every bound. It is empty for
	// a bound that no option changes: the nesting of a patch value (10,000
	// levels), the nesting of typed Go values (100,000 levels) and the 2 GiB size
	// of a Document. It is also empty in the errors of the compat packages, which
	// take no limit options.
	Option string
	// Path is the RFC 6901 pointer to where the limit was hit: the value being
	// read (alias expansion, scalar size, number digits, the nesting of typed
	// values), or, for most patch limits, the pointer of the operation that hit
	// it. It is empty when there is no single value to point at: every limit
	// Parse reports, the size of the input, the number of documents, nesting that
	// the parser stopped at, the nesting of a patch value, a search of "<<"
	// merge sources, and the size of the text that an edit or a merge patch would
	// leave. A limit hit by a decode inside an UnmarshalYAML hook comes back as
	// the error the hook returned, wrapped in a [*TypeError] at the hook's value,
	// and its Path is the pointer in the whole document, as in a [*TypeError].
	Path string

	// rel, sc and from are set on the error of a decode made from the Node a hook
	// was given, while the read around the hook has not yet put its path in front
	// (see TypeError.sc).
	rel  bool
	sc   *bind.Scope
	from int32
}

// Error names the limit, where it was hit and the option that raises it.
func (e *LimitError) Error() string {
	what := e.What
	if what == "" {
		what = string(e.Limit)
	}
	s := fmt.Sprintf("oneyaml: %s limit of %d exceeded", what, e.Max)
	if e.Path != "" {
		s += " at " + shortPath(e.Path)
	}
	if e.Option != "" {
		s += " (raise it with " + e.Option + ")"
	}
	return s
}

// Is reports whether target is [ErrLimit] or the sentinel of the Limit of e.
func (e *LimitError) Is(target error) bool {
	if target == ErrLimit {
		return true
	}
	return target == limitSentinel[e.Limit]
}

var limitSentinel = map[Limit]error{
	LimitMaxDepth:          ErrMaxDepth,
	LimitMaxAliasNodes:     ErrMaxAliasNodes,
	LimitMaxAliasBytes:     ErrMaxAliasBytes,
	LimitMaxInputBytes:     ErrMaxInputBytes,
	LimitMaxScalarBytes:    ErrMaxScalarBytes,
	LimitMaxDocuments:      ErrMaxDocuments,
	LimitMaxNumberDigits:   ErrMaxNumberDigits,
	LimitMaxPatchWorkRatio: ErrMaxPatchWorkRatio,
}

// EditError reports a failed document edit or patch operation.
type EditError struct {
	// Op is the operation, for example "replace".
	Op string
	// Pointer is the RFC 6901 pointer the operation addressed. For "move" and
	// "copy" it is the destination.
	Pointer string
	// From is the source pointer of a "move" or "copy".
	From string
	// Patch reports that the operation was part of a patch, and Index is its
	// zero-based position in that patch.
	Patch bool
	Index int
	// Kind is one of the sentinel errors above, or another cause.
	Kind error
}

// Error describes the operation that failed and why. The package prefix appears
// once, and an empty pointer is left out.
func (e *EditError) Error() string {
	why := "failed"
	if e.Kind != nil {
		why = unprefix(e.Kind.Error())
	}
	what := e.Op
	if e.Pointer != "" {
		what = fmt.Sprintf("%s %q", e.Op, e.Pointer)
	}
	switch {
	case e.Patch:
		return fmt.Sprintf("oneyaml: patch operation %d (%s): %s", e.Index, what, why)
	case e.Op == "patch" && e.Pointer == "":
		// The patch text itself was refused; the cause already says so.
		return "oneyaml: " + why
	}
	return fmt.Sprintf("oneyaml: %s: %s", what, why)
}

// unprefix removes the package prefix from a wrapped message, so that a sentinel
// inside another error does not repeat it.
func unprefix(s string) string {
	s = strings.TrimPrefix(s, "oneyaml: ")
	return strings.ReplaceAll(s, ": oneyaml: ", ": ")
}

// Unwrap lets errors.Is match Kind.
func (e *EditError) Unwrap() error { return e.Kind }

// shortPath keeps the start and end of a very long path for a message, so that
// a deeply nested value does not make an error text as long as the nesting. The
// Path field always holds the whole pointer.
func shortPath(p string) string {
	const keep = 256
	if len(p) <= 2*keep {
		return p
	}
	return p[:keep] + "..." + p[len(p)-keep:]
}

// limitErr builds the error for a bound that Limits can raise. what says in
// words what was bounded.
func limitErr(l Limit, what string, max int, path string) *LimitError {
	return &LimitError{Limit: l, What: what, Max: max, Option: "Limits (" + string(l) + ")", Path: path}
}

// fixedLimitErr builds the error for a bound that no option changes.
func fixedLimitErr(l Limit, what string, max int) *LimitError {
	return &LimitError{Limit: l, What: what, Max: max}
}
