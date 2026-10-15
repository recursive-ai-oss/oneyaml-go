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

// SyntaxError reports YAML that is not well formed.
type SyntaxError struct {
	// Line and Column are 1-based, counted in characters; Offset is the byte offset.
	Line, Column, Offset int
	// Category is a short stable name for the kind of error.
	Category string
	// Msg describes the problem.
	Msg string
}

// Error returns the message with its line and column.
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("oneyaml: line %d, column %d: %s", e.Line, e.Column, e.Msg)
}

// TypeError reports a value that cannot be converted: a YAML value that does not
// fit its Go target when reading, or a Go value with no YAML form when writing.
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
}

// Error returns the message with the position and path of the value.
func (e *TypeError) Error() string {
	loc := ""
	if e.Line > 0 {
		loc = fmt.Sprintf("line %d, column %d: ", e.Line, e.Column)
	}
	if e.Path != "" {
		return fmt.Sprintf("oneyaml: %s%s: %s", loc, e.Path, e.Msg)
	}
	return "oneyaml: " + loc + e.Msg
}

// Unwrap returns the error from a user hook, if there was one.
func (e *TypeError) Unwrap() error { return e.Err }

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
