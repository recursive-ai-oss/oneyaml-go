package oneyaml

import (
	"bytes"
	"strings"
)

// A TagDirective is a %TAG directive: tags written with Handle in the document
// stand for Prefix followed by the rest of the tag.
type TagDirective struct {
	// Handle is the tag handle as written, such as "!" or "!e!".
	Handle string
	// Prefix is the tag prefix as written.
	Prefix string
}

// ExplicitStart reports whether the document begins with a "---" marker. A
// document without the marker is the first of its stream, or follows one that
// ended with "...". It is false for a view of a document that does not exist.
func (d *Document) ExplicitStart() bool {
	return d.valid() && d.s.tree.Docs[d.idx].ExplicitStart
}

// ExplicitEnd reports whether the document ends with a "..." marker. It is false
// for a view of a document that does not exist.
func (d *Document) ExplicitEnd() bool {
	return d.valid() && d.s.tree.Docs[d.idx].ExplicitEnd
}

// Version returns the version of a %YAML directive the document carries. ok is
// false when it has none, in which case the document is read as YAML 1.2.
func (d *Document) Version() (major, minor int, ok bool) {
	if !d.valid() {
		return 0, 0, false
	}
	doc := &d.s.tree.Docs[d.idx]
	return int(doc.VersionMajor), int(doc.VersionMinor), doc.VersionMajor != 0
}

// TagDirectives returns the %TAG directives written before the document, in
// order. Directives belong to one document: those of an earlier document are not
// included.
func (d *Document) TagDirectives() []TagDirective {
	if !d.valid() {
		return nil
	}
	s := d.s
	from := s.docTextFrom(d.idx)
	to := int(s.tree.Docs[d.idx].StartMarker[0])
	if to <= from || to > len(s.src) {
		return nil
	}
	var out []TagDirective
	for _, line := range bytes.Split(s.src[from:to], []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("%TAG")) {
			continue
		}
		text := string(line)
		if i := strings.Index(text, " #"); i >= 0 {
			text = text[:i]
		}
		if f := strings.Fields(text); len(f) >= 3 && f[0] == "%TAG" {
			out = append(out, TagDirective{Handle: f[1], Prefix: f[2]})
		}
	}
	return out
}

// docTextFrom returns the offset where the text that can hold the directives of
// document doc begins: after the end marker of the document before it, or at the
// document's own start marker when that one ended without a marker.
func (s *docStream) docTextFrom(doc int) int {
	if doc == 0 {
		return 0
	}
	if prev := s.tree.Docs[doc-1]; prev.ExplicitEnd {
		return int(prev.EndMarker[1])
	}
	return int(s.tree.Docs[doc].StartMarker[0])
}
