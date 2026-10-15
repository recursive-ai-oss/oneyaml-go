package oneyaml

import (
	"errors"
	"fmt"
	"strings"

	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/scan"
)

// ErrInvalidPointer means a string is not an RFC 6901 JSON Pointer.
var ErrInvalidPointer = errors.New("oneyaml: invalid JSON pointer")

// errEdit means an edit could not be written so that it reads back exactly. It
// should not happen; the document is left unchanged.
var errEdit = errors.New("oneyaml: edit could not be applied")

// errInternal means the edit engine lost track of the document. It should not
// happen. The text is unchanged; the document refuses further edits.
var errInternal = errors.New("oneyaml: internal error in the edit engine")

// ErrUnsupportedEdit means the edit is valid but the document layout is one the
// edit engine does not rewrite. The document is left unchanged.
var ErrUnsupportedEdit = errors.New("oneyaml: unsupported edit")

// editKind maps an engine error to the public error kinds, keeping the detail.
func editKind(err error) error {
	detail := err.Error()
	switch {
	case errors.Is(err, edit.ErrAlias):
		return wrapKind(ErrAliasWrite, detail)
	case errors.Is(err, edit.ErrAmbiguous):
		return wrapKind(ErrAmbiguousKey, detail)
	case errors.Is(err, edit.ErrAnchorInUse):
		return wrapKind(ErrAnchorInUse, detail)
	case errors.Is(err, edit.ErrNotFound):
		return wrapKind(ErrNotFound, detail)
	case errors.Is(err, edit.ErrDepth):
		return &LimitError{Limit: "depth", Max: edit.MaxValueDepth}
	case errors.Is(err, edit.ErrUnsupported):
		return wrapKind(ErrUnsupportedEdit, detail)
	}
	return err
}

// wrapKind returns sentinel with the engine's detail after it, or the sentinel
// alone when the detail only repeats it.
func wrapKind(sentinel error, detail string) error {
	d := trimKind(detail)
	if d == "" || d == unprefix(sentinel.Error()) {
		return sentinel
	}
	return fmt.Errorf("%w: %s", sentinel, d)
}

func trimKind(s string) string {
	if i := strings.Index(s, ": "); i >= 0 {
		return s[i+2:]
	}
	return s
}

// docSyntaxError converts a scanner or parser error.
func docSyntaxError(err error, limits LimitSet) error {
	var se *scan.Error
	if !errors.As(err, &se) {
		return err
	}
	if se.Category == scan.Limit {
		return &LimitError{Limit: "depth", Max: limits.MaxDepth, Option: "Limits or NoLimits"}
	}
	return &SyntaxError{Line: se.Line, Column: se.Column, Offset: se.Offset, Category: se.Category.String(), Msg: se.Msg}
}
