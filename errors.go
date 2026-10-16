package oneyaml

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for the kinds of failure a document operation can report.
// Match them with errors.Is.
var (
	// ErrNotFound means a pointer or path addressed nothing.
	ErrNotFound = errors.New("oneyaml: path not found")
	// ErrNilDocument means a method that changes or decodes a document was called on
	// a nil *Document, which is what a failed Parse returns.
	ErrNilDocument = errors.New("oneyaml: nil Document")
	// ErrAliasWrite means an edit would write through an alias.
	ErrAliasWrite = errors.New("oneyaml: write through an alias")
	// ErrAmbiguousKey means a key occurs more than once, so an edit cannot choose.
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

// The values of [SyntaxError.Category]. The set is closed: a limit that is hit
// is a [*LimitError], not a syntax error.
const (
	// CategorySyntax is any violation of the YAML grammar, including characters
	// that YAML does not allow and an alias to an anchor that was never defined.
	CategorySyntax = "syntax"
	// CategoryEncoding is input that is not valid UTF-8, UTF-16 or UTF-32.
	CategoryEncoding = "encoding"
	// CategoryDuplicateKey is a mapping that has the same key twice. Key,
	// FirstLine and FirstColumn say which key and where it first appeared.
	CategoryDuplicateKey = "duplicate-key"
)

// SyntaxError reports YAML that is not well formed.
type SyntaxError struct {
	// Line and Column are 1-based, counted in characters; Offset is the byte offset.
	Line, Column, Offset int
	// Category is a short stable name for the kind of error: one of the Category
	// constants. Switch on it rather than on Msg.
	Category string
	// Msg describes the problem in terms of what the input got wrong.
	Msg string
	// Key, FirstLine and FirstColumn describe a CategoryDuplicateKey error: the
	// text of the repeated key and the 1-based position of its first occurrence.
	// They are zero when the first occurrence is not known, which happens when
	// two keys are equal only after conversion to the target type.
	Key                    string
	FirstLine, FirstColumn int
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
// read, and every mismatch is reported. The error returned describes the first
// one and More holds the others, in input order, so errors.As finds a
// *TypeError whether there was one problem or many. Syntax errors, limit errors
// and errors from your own hooks still stop at once. At most 100 mismatches are
// kept.
type TypeError struct {
	// Path is the RFC 6901 pointer to the value, empty for the root.
	Path string
	// Line and Column locate the value in the input; zero when writing.
	Line, Column int
	// GoType is the Go type involved.
	GoType string
	// Msg describes the problem.
	Msg string
	// Err is the error a MarshalYAML or MarshalText method returned, if that was the cause.
	Err error
	// Kind is the sentinel error for the cause when there is one, for now only
	// [ErrUnknownField]. errors.Is matches it.
	Kind error
	// More holds the mismatches after this one when a read found several. Each
	// has an empty More.
	More []*TypeError
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
		return fmt.Sprintf("%s%s: %s", loc, e.Path, e.Msg)
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

// LimitError reports that a limit was exceeded.
type LimitError struct {
	// Limit names the limit, for example "depth".
	Limit string
	// Max is the bound that was exceeded.
	Max int
	// Option names the option that raises the bound.
	Option string
	// Path is the RFC 6901 pointer to the value where the limit was hit, if known.
	Path string
}

// Error names the limit, where it was hit and the option that raises it.
func (e *LimitError) Error() string {
	s := fmt.Sprintf("oneyaml: %s limit of %d exceeded", e.Limit, e.Max)
	if e.Path != "" {
		s += " at " + e.Path
	}
	if e.Option != "" {
		s += " (raise it with " + e.Option + ")"
	}
	return s
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
