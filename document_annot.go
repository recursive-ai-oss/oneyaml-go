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
//   - ErrAnchorInUse when the key carries an anchor that an alias later in the
//     document uses, as Remove does, since the alias would read the new key;
//   - ErrUnsupportedEdit for a key that is empty, a collection, an alias or a block
//     scalar, for the merge key "<<", for a new key longer than 1000 bytes or not
//     valid UTF-8, and for a tag on the key that would not fit the new text.
//
// The edit parses only the entries around the key when it can, as Set does, and
// the whole document otherwise. The document a view from Doc selects is the one
// edited.
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
		var oldHash, newHash uint32
		var hashed bool
		if c.HasKeyIndex(loc.Parent) { // the index follows the rename, if there is one
			if kb, ok := c.KeyBytes(loc.Key); ok {
				oldHash, newHash, hashed = edit.KeyHash(kb), edit.KeyHashString(newKey), true
			}
		}
		repls, sp, key, err := c.Rename(loc, newKey, safe)
		if err != nil || len(repls) == 0 {
			return annotPlan{}, err
		}
		return annotPlan{repls: repls, sp: sp, skip: key, parent: loc.Parent, anchor: loc.Node, keys: true, keyHash: oldHash, newHash: newHash, hashed: hashed, verify: func(nt *cst.Tree, out []byte, _ int, off int32) bool {
			return string(edit.DecodeScalar(nil, nt, out, key+off)) == newKey
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
		return annotPlan{repls: repls, sp: sp, parent: loc.Parent, anchor: n, verify: func(nt *cst.Tree, out []byte, d int, off int32) bool {
			s, e := edit.NewCtxLagging(nt, out, d).HeadCommentSpan(n + off)
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
// empty. A node inside a flow collection has a place only when nothing, not even
// a comma, follows it on its line, as for the last item of a collection written
// one item per line; otherwise, and for the root block collection and a value
// that something else follows on its line, SetLineComment refuses with
// ErrUnsupportedEdit. ErrNotFound and ErrAliasWrite are reported as for Set. The
// document reads back with the same values and only the comment changed.
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
		return annotPlan{repls: repls, sp: sp, parent: loc.Parent, anchor: n, verify: func(nt *cst.Tree, out []byte, d int, off int32) bool {
			s, e := edit.NewCtxLagging(nt, out, d).LineCommentSpan(n + off)
			return edit.TrimComment(string(out[s:e])) == comment
		}}, nil
	})
}

// annotPlan is an edit that changes one stretch of text and leaves every node as it
// was: a key rename or a comment.
type annotPlan struct {
	repls []edit.Repl
	sp    edit.Splice
	skip  int32 // the node whose text the splice replaces
	// parent is the container the edit is in, and anchor a node of its entry: where
	// a region edit looks. keys is set when the edit changes the text of a key.
	parent, anchor int32
	keys, hashed   bool
	// For a rename in a mapping that has a key index: what the index needs to follow
	// the key (the hash of its old text, and the new text).
	keyHash, newHash uint32
	// verify reads the new text back as intended. nt is the new tree and doc the
	// document in it; off is added to the number of a node of the old tree to give
	// its number in nt.
	verify func(nt *cst.Tree, out []byte, doc int, off int32) bool
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
		gen := s.gen
		if s.editRegion(c, &planned{repls: pl.repls, parent: pl.parent, anchor: pl.anchor, check: func(nt *cst.Tree, out []byte, rg *regionInfo) bool {
			// Only the run the region replaces is compared; the rest of the tree is not
			// touched by an edit inside it.
			if rg == nil || s.tree.Nodes[rg.cn].Flow || pl.anchor < rg.lo || pl.anchor > rg.hi || (pl.skip != 0 && (pl.skip < rg.lo || pl.skip > rg.hi)) {
				return false
			}
			return edit.SameRegionShape(&s.tree, rg.lo, rg.hi, rg.cn, nt, rg.shift, pl.sp, pl.skip) && pl.verify(nt, out, 0, 2-rg.lo)
		}}) {
			if !pl.keys {
				s.ed.cache.KeepKeys(gen, s.gen) // no node moved and no key changed
			} else if pl.hashed {
				s.ed.cache.RenameKey(pl.parent, pl.skip, pl.keyHash, pl.newHash, gen, s.gen) // no node moved
			}
			if op == "rename" && s.ranging() {
				s.noteEditAt(doc, opReplace, toks)
			}
			return nil
		}
		// The whole stream is parsed again. The check needs the old tree, and parsing
		// the new text reuses the slices of the current one.
		old := cst.Tree{Nodes: append([]cst.Node(nil), s.tree.Nodes...), Props: append([]cst.Prop(nil), s.tree.Props...), Docs: append([]cst.Doc(nil), s.tree.Docs...)}
		check := func(nt *cst.Tree, out []byte, _ *regionInfo) bool {
			return edit.SameShape(&old, nt, pl.sp, pl.skip) && pl.verify(nt, out, doc, 0)
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
