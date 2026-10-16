package oneyaml

import (
	"errors"
	"fmt"
	"strings"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/patch"
	"oneyaml.dev/go/internal/pointer"
)

// ErrInvalidPatch means a patch is not well formed: it is not valid JSON, it is not
// an array of operations, an operation is unknown or lacks a member it needs, or
// the operation cannot be done at all (moving a value into itself).
var ErrInvalidPatch = errors.New("oneyaml: invalid patch")

// A PatchOp is one RFC 6902 operation for Apply.
type PatchOp struct {
	// Op is "add", "remove", "replace", "move", "copy" or "test".
	Op string
	// Path is the RFC 6901 pointer the operation works on.
	Path string
	// From is the source pointer of "move" and "copy".
	From string
	// Value is the operand of "add", "replace" and "test": anything Marshal
	// accepts, with nil meaning null.
	Value any
}

// PatchAdd returns an "add" operation.
func PatchAdd(path string, v any) PatchOp { return PatchOp{Op: "add", Path: path, Value: v} }

// PatchRemove returns a "remove" operation.
func PatchRemove(path string) PatchOp { return PatchOp{Op: "remove", Path: path} }

// PatchReplace returns a "replace" operation.
func PatchReplace(path string, v any) PatchOp { return PatchOp{Op: "replace", Path: path, Value: v} }

// PatchMove returns a "move" operation.
func PatchMove(from, path string) PatchOp { return PatchOp{Op: "move", Path: path, From: from} }

// PatchCopy returns a "copy" operation.
func PatchCopy(from, path string) PatchOp { return PatchOp{Op: "copy", Path: path, From: from} }

// PatchTest returns a "test" operation.
func PatchTest(path string, v any) PatchOp { return PatchOp{Op: "test", Path: path, Value: v} }

// ApplyPatch applies an RFC 6902 JSON Patch, given as JSON text, to the document.
// The patch is atomic: if any operation fails, including a "test" that does not
// match, the document is left exactly as it was, byte for byte, and the error is
// an *EditError that names the operation.
//
// The operations follow the RFC, with these points where YAML needs a decision:
//   - Pointers address mapping keys by their scalar text, and a key that occurs
//     twice is ambiguous (ErrAmbiguousKey).
//   - Reads follow aliases, including "from" of copy and the paths of "test".
//     Writing below an alias is refused (ErrAliasWrite); replacing the alias node
//     itself is allowed.
//   - "test" compares by the YAML data model under the Core schema: 1 differs
//     from 1.0, 0x1F equals 31, a quoted "1" is a string, an explicit core tag such
//     as !!float decides the type of its scalar, and mappings are equal whatever
//     the order of their keys.
//   - "copy" writes the value without its anchor, so no anchor is defined twice.
//     An alias inside the copied value stays an alias while its anchor is still
//     defined once, before the destination; otherwise it is written out.
//   - "move" refuses an anchored value that aliases still use (ErrAnchorInUse),
//     and a destination inside the value being moved (ErrInvalidPatch).
//   - What a patch may write is bounded. The text copies add up to at most the alias
//     bytes limit (ten times the document, never less than 64 MiB by default), and
//     with a MaxInputBytes limit no step may leave a document larger than it; a
//     patch past either bound fails with a *LimitError and changes nothing.
//     The text its edits have to parse again is bounded too (MaxPatchWorkRatio),
//     and with a MaxInputBytes limit the patch text itself may not be longer than
//     it. NoLimits removes the bounds.
//   - New members go at the end of their mapping, in the style of their
//     siblings; comments outside the edited values stay.
//
// A patch applies to the first document, or to the document a view from Doc
// selects.
func (d *Document) ApplyPatch(rfc6902 []byte) error {
	if d.isNil() {
		return &EditError{Op: "patch", Kind: ErrNilDocument}
	}
	if err := d.s.checkPatchSize(len(rfc6902)); err != nil {
		return &EditError{Op: "patch", Kind: err}
	}
	ops, bad, err := patch.ParseOps(rfc6902, d.s.cfg.limits.MaxDepth)
	if err != nil && bad < 0 && errors.Is(err, patch.ErrSyntax) {
		// Not JSON. A patch kept as YAML (a sequence of operations, as Kustomize
		// users write them) reads the same way.
		if yops, ybad, yerr, ok := d.s.yamlPatchOps(rfc6902); ok {
			ops, bad, err = yops, ybad, yerr
		}
	}
	if err != nil {
		ee := &EditError{Op: "patch", Index: max(bad, 0), Patch: bad >= 0, Kind: kindOf(err, d.s.cfg.limits.MaxDepth)}
		if bad >= 0 && bad < len(ops) {
			// Name the operation and path as far as they were read.
			ee.Op, ee.Pointer = ops[bad].Name, ops[bad].Path
			if ee.Op == "" {
				ee.Op = "patch"
			}
		}
		return ee
	}
	pops := make([]patchStep, len(ops))
	for i := range ops {
		pops[i] = patchStep{op: ops[i].Name, path: ops[i].Path, from: ops[i].From, val: ops[i].Value}
	}
	return d.s.applySteps(d.idx, pops, len(rfc6902))
}

// yamlPatchOps reads text that is not JSON as a patch written in YAML. ok is false
// when the text is not a YAML sequence either, and the caller then reports the JSON
// error. A YAML patch may not use aliases or hold the same key twice in a mapping,
// which JSON cannot either, and must be a single document.
func (s *docStream) yamlPatchOps(text []byte) (ops []patch.Op, bad int, err error, ok bool) {
	var t cst.Tree
	if cst.Build(&t, text, s.depth()) != nil || len(t.Docs) != 1 {
		return nil, 0, nil, false
	}
	root := t.Docs[0].Root
	if root == 0 || t.Nodes[root].Kind != cst.Sequence {
		return nil, 0, nil, false
	}
	v, verr := edit.FromTree(&t, text, root)
	if verr != nil {
		return nil, -1, &LimitError{Limit: "depth", Max: edit.MaxValueDepth, Option: "Limits or NoLimits"}, true
	}
	if msg := yamlPatchProblem(&v); msg != "" {
		return nil, -1, fmt.Errorf("%w: %s", patch.ErrSyntax, msg), true
	}
	ops, bad, err = patch.OpsFromValue(v)
	return ops, bad, err, true
}

// yamlPatchProblem names what in a patch value written in YAML has no JSON
// counterpart and is refused: an alias, or a mapping that names a key twice.
func yamlPatchProblem(root *edit.Value) string {
	stack := []*edit.Value{root}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch v.Kind {
		case edit.Alias:
			return "a patch may not use aliases"
		case edit.Map:
			seen := make(map[string]struct{}, len(v.Items)/2)
			for i := 0; i+1 < len(v.Items); i += 2 {
				k := &v.Items[i]
				if k.Kind == edit.Map || k.Kind == edit.Seq || k.Kind == edit.Alias {
					return "a patch may not use a collection as a key"
				}
				id := string(rune('0'+k.Kind)) + k.Text
				if _, dup := seen[id]; dup {
					return "a mapping names the key " + fmt.Sprintf("%q", k.Text) + " twice"
				}
				seen[id] = struct{}{}
			}
		}
		for i := range v.Items {
			if it := &v.Items[i]; !it.IsScalar() || it.Kind == edit.Alias {
				stack = append(stack, it)
			}
		}
	}
	return ""
}

// Apply applies RFC 6902 operations to the document in order, all or nothing, as
// ApplyPatch does.
func (d *Document) Apply(ops ...PatchOp) error {
	if d.isNil() {
		return &EditError{Op: "patch", Kind: ErrNilDocument}
	}
	steps := make([]patchStep, len(ops))
	size := 0
	for i, o := range ops {
		st := patchStep{op: o.Op, path: o.Path, from: o.From}
		switch o.Op {
		case "add", "replace", "test":
			v, err := valueOf(o.Value, &d.s.cfg)
			if err != nil {
				return &EditError{Op: o.Op, Pointer: o.Path, Index: i, Patch: true, Kind: err}
			}
			st.val = v
		case "remove", "move", "copy":
		default:
			return &EditError{Op: o.Op, Pointer: o.Path, Index: i, Patch: true, Kind: fmt.Errorf("%w: unknown operation %q", ErrInvalidPatch, o.Op)}
		}
		steps[i] = st
		size += len(o.Op) + len(o.Path) + len(o.From) + 32
	}
	return d.s.applySteps(d.idx, steps, size)
}

// kindOf maps a patch-package error to the public kinds.
func kindOf(err error, maxDepth int) error {
	if errors.Is(err, patch.ErrDepth) {
		// With no limit the parser still stops at its own cap; report that one.
		if maxDepth <= 0 || maxDepth > edit.MaxValueDepth {
			maxDepth = edit.MaxValueDepth
		}
		return &LimitError{Limit: "depth", Max: maxDepth, Option: "Limits or NoLimits"}
	}
	if errors.Is(err, patch.ErrSyntax) {
		return fmt.Errorf("%w: %s", ErrInvalidPatch, trimKind(err.Error()))
	}
	return err
}

type patchStep struct {
	op, path, from string
	val            edit.Value
}

// aliasBudget is the bound on the nodes visited below aliases by a comparison.
func (s *docStream) aliasBudget() int {
	n, _ := s.cfg.aliasBounds(len(s.tree.Nodes), len(s.src))
	return n
}

// applySteps runs the steps on document doc and leaves the stream as it was if
// one fails.
func (s *docStream) applySteps(doc int, steps []patchStep, patchLen int) error {
	defer s.beginBatch()()
	writes := 0
	for i := range steps {
		switch steps[i].op {
		case "test":
		case "move", "copy":
			writes += 2 // two edits inside one step
		default:
			writes++
		}
	}
	var snap docSnapshot
	var budget patchBudget
	s.initBudget(&budget)
	b := &budget
	// With an output bound a step can succeed and then be refused for its size, so
	// the stream must be restorable.
	snapped := writes > 1 || (writes == 1 && len(steps) > 1) || (writes > 0 && b.maxDoc > 0)
	work := s.newPatchWork(patchLen)
	if snapped {
		// A single edit already leaves the stream unchanged when it fails.
		snap = s.snapshot()
	}
	declined := 0
	for i := 0; i < len(steps); i++ {
		err := error(nil)
		if i > 0 {
			// Before each step, so the step that crosses the bound is the last to run.
			err = work.check(steps[i].path)
		}
		if err == nil {
			if i >= declined {
				n, end := s.removeRun(doc, steps[i:])
				if n > 0 {
					i += n - 1
					continue
				}
				// A run the pass declined is not offered again until it ends.
				declined = i + end
			}
			err = s.applyStep(doc, &steps[i], b)
		}
		if err != nil {
			if snapped {
				if rerr := s.rollback(snap); rerr != nil {
					// The text is back as it was, but the tree could not be: report
					// that, not the failure that led here.
					return &EditError{Op: steps[i].op, Pointer: steps[i].path, Patch: true, Index: i, Kind: rerr}
				}
			}
			var ee *EditError
			if !errors.As(err, &ee) {
				ee = &EditError{Op: steps[i].op, Pointer: steps[i].path, Kind: err}
			}
			ee.Patch, ee.Index = true, i
			if ee.Kind == ErrInvalidPointer {
				ee.Kind = errPatchPointer
			}
			return ee
		}
	}
	return nil
}

// removeRun removes a run of "remove" steps that take entries out of one mapping,
// in one pass. It returns how many steps it made, or 0 when the steps are not such
// a run or the pass does not apply; the first step is then made as usual. The
// second result is the length of the run it found (0 if there is none), which the
// caller skips over instead of asking again at every step of it. An entry that is
// not there fails the patch at its step, which the one-by-one path reports, so the
// pass only takes runs whose keys all exist.
func (s *docStream) removeRun(doc int, steps []patchStep) (made, run int) {
	if len(steps) < batchMin || steps[0].op != "remove" {
		return 0, 0
	}
	first, err := pointer.Parse(steps[0].path)
	if err != nil || len(first) == 0 {
		return 0, 0
	}
	parent := first[:len(first)-1]
	keys := []string{first[len(first)-1]}
	// The other steps are checked on their text: the same parent path and one more
	// token. A token with an escape is parsed, so that a bad escape ends the run.
	prefix := steps[0].path[:strings.LastIndexByte(steps[0].path, '/')+1]
	for _, st := range steps[1:] {
		if st.op != "remove" || !strings.HasPrefix(st.path, prefix) {
			break
		}
		tok := st.path[len(prefix):]
		if strings.IndexByte(tok, '/') >= 0 {
			break
		}
		if strings.IndexByte(tok, '~') >= 0 {
			toks, err := pointer.Parse(st.path)
			if err != nil || len(toks) != len(first) {
				break
			}
			tok = toks[len(toks)-1]
		}
		keys = append(keys, tok)
	}
	if len(keys) < batchMin {
		return 0, len(keys)
	}
	if !s.removeKeys(doc, parent, keys, true) {
		return 0, len(keys)
	}
	return len(keys), len(keys)
}

func (s *docStream) applyStep(doc int, st *patchStep, b *patchBudget) error {
	switch st.op {
	case "add":
		return b.afterWrite(s, st, s.edit(doc, opAdd, st.path, &st.val))
	case "replace":
		return b.afterWrite(s, st, s.edit(doc, opReplace, st.path, &st.val))
	case "remove":
		return s.edit(doc, opRemove, st.path, nil)
	case "test":
		return s.testStep(doc, st)
	case "copy", "move":
		return s.relocate(doc, st, b)
	}
	return &EditError{Op: st.op, Pointer: st.path, Kind: fmt.Errorf("%w: unknown operation %q", ErrInvalidPatch, st.op)}
}

// errPatchPointer is what a malformed pointer in a patch reports: the patch is
// invalid, and the pointer is the reason.
var errPatchPointer = fmt.Errorf("%w: %w", ErrInvalidPatch, ErrInvalidPointer)

func (s *docStream) fail(st *patchStep, kind error) error {
	return &EditError{Op: st.op, Pointer: st.path, From: st.from, Kind: kind}
}

func (s *docStream) ctx(doc int) *edit.Ctx {
	c := edit.NewCtx(&s.tree, s.src, doc)
	c.MaxAliasHops = s.cfg.limits.MaxAliasNodes
	s.attach(c)
	return c
}

// attach gives c the stream's cache, valid for the current version of the tree. It
// keeps the indexes that steps and nodes read the same version of the document
// share, and the key indexes of large mappings that a batch of edits works on.
func (s *docStream) attach(c *edit.Ctx) {
	ca := &s.es().cache
	ca.Gen = s.gen
	c.Cache = ca
}

func (s *docStream) testStep(doc int, st *patchStep) error {
	toks, err := pointer.Parse(st.path)
	if err != nil {
		return s.fail(st, ErrInvalidPointer)
	}
	if doc < 0 || doc >= len(s.tree.Docs) {
		return s.fail(st, ErrNotFound)
	}
	c := s.ctx(doc)
	loc, err := c.Resolve(toks, false)
	if err != nil {
		return s.fail(st, editKind(err))
	}
	if loc.Node == 0 {
		return s.fail(st, fmt.Errorf("%w: nothing at %s", ErrNotFound, st.path))
	}
	ok, err := patch.Equal(c, loc.Node, &st.val, s.aliasBudget())
	if err != nil {
		return s.fail(st, &LimitError{Limit: "alias expansion nodes", Max: s.aliasBudget(), Option: "Limits (MaxAliasNodes)", Path: st.path})
	}
	if !ok {
		return s.fail(st, ErrTestFailed)
	}
	return nil
}

// relocate does "copy" and "move".
func (s *docStream) relocate(doc int, st *patchStep, b *patchBudget) error {
	move := st.op == "move"
	fromToks, err := pointer.Parse(st.from)
	if err != nil {
		return s.fail(st, ErrInvalidPointer)
	}
	toToks, err := pointer.Parse(st.path)
	if err != nil {
		return s.fail(st, ErrInvalidPointer)
	}
	if doc < 0 || doc >= len(s.tree.Docs) {
		return s.fail(st, ErrNotFound)
	}
	if move && len(toToks) > len(fromToks) && equalToks(fromToks, toToks[:len(fromToks)]) {
		return s.fail(st, fmt.Errorf("%w: cannot move %s into itself", ErrInvalidPatch, st.from))
	}
	// A move reads below no alias, since it removes what it reads.
	c := s.ctx(doc)
	loc, err := c.Resolve(fromToks, move)
	if err != nil {
		return s.fail(st, editKind(err))
	}
	if loc.Node == 0 {
		return s.fail(st, fmt.Errorf("%w: nothing at %s", ErrNotFound, st.from))
	}
	if st.from == st.path && (move || loc.Parent == 0 || s.tree.Nodes[loc.Parent].Kind != cst.Sequence) {
		// Moving a value onto itself, or copying it onto its own mapping key, changes
		// nothing. A copy onto its own sequence index is an add (RFC 6902, section
		// 4.5): the sequence gets a second copy of the value there.
		return nil
	}
	if !move {
		// A copy adds the text of the value to the document; charge it before the
		// value is built, so a few copies of the whole document cannot double it
		// until memory is gone.
		nd := &s.tree.Nodes[loc.Node]
		if err := b.chargeCopy(s, st, int(nd.End-nd.Start)); err != nil {
			return err
		}
	}
	cp := s.copier(&s.cfg)
	cp.Old, cp.New = c, c
	if move {
		cp.Moved = loc.Node
	}
	// Where the value will start, to decide which aliases can stay.
	cp.Dest = len(s.src)
	addFirst := false
	if dl, err := c.Resolve(toToks, true); err == nil {
		switch {
		case dl.Node != 0:
			cp.Dest, cp.DestNode = int(s.tree.Nodes[dl.Node].Start), dl.Node
		case dl.Parent != 0:
			cp.Dest, cp.DestNode = int(s.tree.Nodes[dl.Parent].End), dl.Parent
		}
		// When both the source and the destination sit in mappings, writing the
		// destination first changes no index the source or the destination needs,
		// and it never empties a mapping in the middle of the move (which would turn
		// a block mapping into "{}").
		pk := s.tree.Nodes[dl.Parent].Kind
		fk := s.tree.Nodes[loc.Parent].Kind
		addFirst = move && dl.Parent != 0 && loc.Parent != 0 && pk == cst.Mapping && fk == cst.Mapping &&
			!(len(toToks) <= len(fromToks) && equalToks(toToks, fromToks[:len(toToks)]))
	}
	var snap docSnapshot
	switch {
	case move && addFirst:
		snap = s.snapshot()
	case move:
		snap = s.snapshotTree() // the old tree is read after the removal
	}
	fail := func(err error) error {
		if move {
			if rerr := s.rollback(snap); rerr != nil {
				return s.fail(st, rerr)
			}
		}
		var be *patch.BudgetError
		if errors.As(err, &be) && be.What == "written" {
			return s.fail(st, &LimitError{Limit: "patch copy bytes", Max: b.maxCopy, Option: "Limits (MaxAliasBytes)", Path: st.path})
		}
		if errors.Is(err, patch.ErrBudget) {
			return s.fail(st, aliasError(err, st.from))
		}
		return unwrapEdit(st, aliasError(err, st.from))
	}
	if move && !addFirst {
		// Remove first, as the RFC says. The old tree stays readable: edits build
		// new trees.
		cp.Old = edit.NewCtx(snap.tree, snap.src, doc)
		cp.Old.MaxAliasHops = s.cfg.limits.MaxAliasNodes
		if err := s.edit(doc, opRemove, st.from, nil); err != nil {
			return unwrapEdit(st, err)
		}
		cp.New = s.ctx(doc)
		cp.Dest = len(s.src)
		// The destination named a node of the old tree; none of those numbers is
		// good in the new one.
		cp.DestNode = 0
		if dl, err := cp.New.Resolve(toToks, true); err == nil {
			switch {
			case dl.Node != 0:
				cp.Dest, cp.DestNode = int(s.tree.Nodes[dl.Node].Start), dl.Node
			case dl.Parent != 0:
				cp.Dest, cp.DestNode = int(s.tree.Nodes[dl.Parent].End), dl.Parent
			}
		}
	}
	// What writing aliases out adds to the value counts against the same patch-wide
	// bound as the copied text, whose size the aliases themselves do not show.
	if b.maxCopy > 0 {
		cp.MaxWritten = max(b.maxCopy-b.copied, 1)
	}
	v, err := cp.Value(loc.Node)
	if err == nil {
		b.copied += cp.Written
	}
	if err == nil {
		err = s.edit(doc, opAdd, st.path, &v)
	}
	if err == nil && addFirst {
		err = s.edit(doc, opRemove, st.from, nil)
	}
	if err != nil {
		return fail(err)
	}
	return b.afterWrite(s, st, nil)
}

// patchBudget bounds what one patch may write. A patch of a few bytes can name the
// whole document as the source of every copy, which doubles it each time.
type patchBudget struct {
	maxDoc  int // the largest document a step may leave (the input limit), or 0
	maxCopy int // the most text copies may add up to, or 0
	copied  int
}

func (s *docStream) initBudget(b *patchBudget) {
	b.maxDoc = s.cfg.limits.MaxInputBytes
	// The same bound as for alias expansion: the document's own size times the
	// configured multiple, but never below the limit.
	_, b.maxCopy = s.cfg.aliasBounds(len(s.tree.Nodes), len(s.src))
}

// chargeCopy accounts for n bytes about to be copied.
func (b *patchBudget) chargeCopy(s *docStream, st *patchStep, n int) error {
	if b.maxCopy > 0 {
		b.copied += n
		if b.copied > b.maxCopy {
			return s.fail(st, &LimitError{Limit: "patch copy bytes", Max: b.maxCopy, Option: "Limits (MaxAliasBytes)", Path: st.path})
		}
	}
	if b.maxDoc > 0 && len(s.src)+n > b.maxDoc {
		return s.fail(st, &LimitError{Limit: "patch output size", Max: b.maxDoc, Option: "Limits (MaxInputBytes)", Path: st.path})
	}
	return nil
}

// afterWrite checks the size of the document a step left, and passes err on.
func (b *patchBudget) afterWrite(s *docStream, st *patchStep, err error) error {
	if err == nil && b.maxDoc > 0 && len(s.src) > b.maxDoc {
		return s.fail(st, &LimitError{Limit: "patch output size", Max: b.maxDoc, Option: "Limits (MaxInputBytes)", Path: st.path})
	}
	return err
}

// unwrapEdit gives an error from the single-edit path the fields of the patch step.
func unwrapEdit(st *patchStep, err error) error {
	var ee *EditError
	if errors.As(err, &ee) {
		return &EditError{Op: st.op, Pointer: st.path, From: st.from, Kind: ee.Kind}
	}
	return &EditError{Op: st.op, Pointer: st.path, From: st.from, Kind: err}
}

func equalToks(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
