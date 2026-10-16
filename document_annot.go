package oneyaml

import (
	"strings"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/pointer"
)

// Rename changes the key of the mapping entry at ptr to newKey, in place. The
// value, the entry's position in its mapping, its comments and the layout around
// it stay as they are; only the key text changes. The new key is written in the
// style of the old one (plain, single- or double-quoted) when that style can hold
// it, and otherwise in the nearest style that can: a plain key that needs quoting
// becomes single-quoted, and one with a line break or an unprintable character
// becomes double-quoted. The text is always read as a string: renaming the key 1
// to "2" gives the quoted key '2'. An anchor or tag on the key stays.
//
// After a successful Rename, Get of the parent pointer with newKey gives the old
// value and the old key is gone. Renaming a key to the text it already has changes
// nothing.
//
// Rename refuses, and leaves the document unchanged, with:
//   - ErrKeyExists when the mapping already has a key reading as newKey;
//   - ErrNotAnEntry when ptr is the root or addresses an item of a sequence;
//   - ErrNotFound when ptr does not exist;
//   - ErrAliasWrite when ptr leads through an alias (renaming an entry whose value
//     is an alias is allowed, as replacing it is);
//   - ErrAmbiguousKey when the key at ptr occurs twice;
//   - ErrUnsupportedEdit for a key that is empty, a collection, an alias or a block
//     scalar, for the merge key "<<", for a new key longer than 1000 bytes or not
//     valid UTF-8, and for a tag on the key that would not fit the new text.
//
// The edit parses the whole document again, so a series of renames in a very large
// document costs more than a series of Set calls. The document a view from Doc
// selects is the one edited.
func (d *Document) Rename(ptr, newKey string) error {
	if d.isNil() {
		return &EditError{Op: "rename", Pointer: ptr, Kind: ErrNilDocument}
	}
	return d.s.annotate(d.idx, "rename", ptr, 2, func(c *edit.Ctx, toks []string, safe bool) (annotPlan, error) {
		loc, err := c.Resolve(toks, true)
		if err != nil {
			return annotPlan{}, err
		}
		if loc.Node == 0 {
			return annotPlan{}, edit.ErrNotFound
		}
		repls, sp, key, err := c.Rename(loc, newKey, safe)
		if err != nil || len(repls) == 0 {
			return annotPlan{}, err
		}
		return annotPlan{repls: repls, sp: sp, skip: key, verify: func(nt *cst.Tree, out []byte) bool {
			return string(edit.DecodeScalar(nil, nt, out, key)) == newKey
		}}, nil
	})
}

// SetHeadComment replaces the comment on the lines directly above the entry or item
// at ptr (the comment Node.HeadComment reads) by text, or removes it when text is
// empty. Each line of text without a leading "#" gets "# " in front, a blank line
// inside the text becomes a bare "#", and the lines are written at the entry's
// indentation. The root pointer "" sets the comment above the first content line of
// the document, below a "---" marker line.
//
// A head comment stands on lines of its own, so an entry that shares its line with
// another (the first key of a mapping that is an item written after "- ") has none:
// SetHeadComment refuses it with ErrUnsupportedEdit, as it does an entry of a flow
// collection. Text that is not valid UTF-8 or holds a line separator other than
// the line feed, or another control character, is refused with ErrInvalidComment.
// ErrNotFound and ErrAliasWrite are reported as for Set. The document reads back
// with the same values and only the comment lines changed.
func (d *Document) SetHeadComment(ptr, text string) error {
	if d.isNil() {
		return &EditError{Op: "head-comment", Pointer: ptr, Kind: ErrNilDocument}
	}
	lines, err := edit.CommentLines(text)
	if err != nil {
		return &EditError{Op: "head-comment", Pointer: ptr, Kind: editKind(err)}
	}
	want := strings.Join(lines, "\n")
	return d.s.annotate(d.idx, "head-comment", ptr, 1, func(c *edit.Ctx, toks []string, safe bool) (annotPlan, error) {
		loc, err := c.Resolve(toks, true)
		if err != nil {
			return annotPlan{}, err
		}
		if loc.Node == 0 {
			return annotPlan{}, edit.ErrNotFound
		}
		n := loc.Node
		repls, sp, err := c.HeadComment(n, lines)
		if err != nil || len(repls) == 0 {
			return annotPlan{}, err
		}
		doc := c.Doc
		return annotPlan{repls: repls, sp: sp, verify: func(nt *cst.Tree, out []byte) bool {
			s, e := edit.NewCtx(nt, out, doc).HeadCommentSpan(n)
			return edit.TrimComment(string(out[s:e])) == want
		}}, nil
	})
}

// SetLineComment replaces the comment after the value at ptr on its line (the
// comment Node.LineComment reads) by text, or removes it when text is empty. Text
// without a leading "#" gets "# " in front. Text with a line break is refused with
// ErrInvalidComment, as is text that is not valid UTF-8 or holds a control
// character.
//
// The comment goes after the value: after a scalar, after the closing bracket of a
// flow collection that is the value, after the header of a block scalar ("|", ">-"),
// and after the colon or dash of an entry whose value is a block collection or
// empty. A node inside a flow collection, the root block collection, and a value
// that something else follows on its line have no such place: SetLineComment
// refuses them with ErrUnsupportedEdit. ErrNotFound and ErrAliasWrite are reported
// as for Set. The document reads back with the same values and only the comment
// changed.
func (d *Document) SetLineComment(ptr, text string) error {
	if d.isNil() {
		return &EditError{Op: "line-comment", Pointer: ptr, Kind: ErrNilDocument}
	}
	comment := ""
	if strings.TrimSpace(text) != "" {
		if strings.ContainsAny(text, "\n\r\u0085\u2028\u2029") {
			return &EditError{Op: "line-comment", Pointer: ptr, Kind: wrapKind(ErrInvalidComment, "a line comment cannot hold a line break")}
		}
		lines, err := edit.CommentLines(text)
		if err != nil {
			return &EditError{Op: "line-comment", Pointer: ptr, Kind: editKind(err)}
		}
		if len(lines) != 1 {
			return &EditError{Op: "line-comment", Pointer: ptr, Kind: wrapKind(ErrInvalidComment, "a line comment cannot hold a line break")}
		}
		comment = lines[0]
	}
	return d.s.annotate(d.idx, "line-comment", ptr, 1, func(c *edit.Ctx, toks []string, safe bool) (annotPlan, error) {
		loc, err := c.Resolve(toks, true)
		if err != nil {
			return annotPlan{}, err
		}
		if loc.Node == 0 {
			return annotPlan{}, edit.ErrNotFound
		}
		n := loc.Node
		repls, sp, err := c.LineComment(n, comment)
		if err != nil || len(repls) == 0 {
			return annotPlan{}, err
		}
		doc := c.Doc
		return annotPlan{repls: repls, sp: sp, verify: func(nt *cst.Tree, out []byte) bool {
			s, e := edit.NewCtx(nt, out, doc).LineCommentSpan(n)
			return edit.TrimComment(string(out[s:e])) == comment
		}}, nil
	})
}

// annotPlan is an edit that changes one stretch of text and leaves every node as it
// was: a key rename or a comment.
type annotPlan struct {
	repls  []edit.Repl
	sp     edit.Splice
	skip   int32 // the node whose text the splice replaces
	verify func(nt *cst.Tree, out []byte) bool
}

// annotate runs a rename or comment edit on document doc. mk plans it on the current
// tree; the text is kept only if it parses to the same tree as before with the
// splice applied, and verify reads the new text back as intended. attempts is how
// often to plan again (with safe set) when the text is refused.
func (s *docStream) annotate(doc int, op, ptr string, attempts int, mk func(c *edit.Ctx, toks []string, safe bool) (annotPlan, error)) error {
	s.freezeRanges()
	fail := func(kind error) error { return &EditError{Op: op, Pointer: ptr, Kind: kind} }
	if doc < 0 || doc >= len(s.tree.Docs) {
		return fail(ErrNotFound)
	}
	toks, err := pointer.Parse(ptr)
	if err != nil {
		return fail(ErrInvalidPointer)
	}
	if s.es().lost {
		return fail(errInternal)
	}
	var limitErr error
	for attempt := 0; attempt < attempts; attempt++ {
		c := s.ctx(doc)
		pl, err := mk(c, toks, attempt == 1)
		if err != nil {
			return fail(editKind(err))
		}
		if len(pl.repls) == 0 {
			return nil
		}
		// The check needs the old tree, and parsing the new text reuses the slices of
		// the current one.
		old := cst.Tree{Nodes: append([]cst.Node(nil), s.tree.Nodes...), Props: append([]cst.Prop(nil), s.tree.Props...), Docs: append([]cst.Doc(nil), s.tree.Docs...)}
		check := func(nt *cst.Tree, out []byte, _ *regionInfo) bool {
			return edit.SameShape(&old, nt, pl.sp, pl.skip) && pl.verify(nt, out)
		}
		done, lerr, err := s.applyRepls(pl.repls, check)
		if err != nil {
			return fail(err)
		}
		if lerr != nil && limitErr == nil {
			limitErr = lerr
		}
		if done {
			if op == "rename" && s.ranging() {
				s.noteEditAt(doc, opReplace, toks)
			}
			return nil
		}
	}
	if limitErr != nil {
		return fail(limitErr)
	}
	return fail(errEdit)
}
