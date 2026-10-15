package oneyaml

import (
	"oneyaml.dev/go/internal/bind"
	"oneyaml.dev/go/internal/compatopt"
	"oneyaml.dev/go/internal/scan"
)

// The compat packages select their dialect through internal hooks, so the public
// API gains no options of its own.
func init() {
	compatopt.V3 = func() any { return Option(func(c *config) { c.dialect = bind.DialectV3 }) }
	compatopt.V3Compact = func() any { return Option(func(c *config) { c.compactSeq = true }) }
}

// collectedErr returns the type errors a binder collected for the document it
// just read, or nil. lineBase is the number of lines before src in the stream.
func collectedErr(b *bind.Binder, src []byte, lineBase int) error {
	c := b.Collected()
	if len(c) == 0 {
		return nil
	}
	out := &compatopt.Collected{Items: make([]compatopt.Item, len(c))}
	for i, e := range c {
		pos := scan.NewError(src, e.Offset, scan.Syntax, "")
		out.Items[i] = compatopt.Item{Line: pos.Line + lineBase, Column: pos.Column, Msg: e.Msg, Verbatim: e.Category == "verbatim"}
	}
	return out
}
