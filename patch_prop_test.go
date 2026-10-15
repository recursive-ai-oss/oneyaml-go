package oneyaml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/pointer"
)

// The patch property test applies random multi-operation patches and compares the
// outcome with a model: the same operations applied to the fully expanded value
// of every document (aliases replaced by what they refer to). On success the
// output, parsed again from its bytes, must expand to exactly the model. On
// failure the bytes must be identical to the input, and the error must be the
// one the model predicts, unless the engine refused the layout.
//
// The model has no notion of sharing, so a document that holds aliases is only
// checked for atomicity and for staying readable.

// expandedStream returns the alias-free value of every document of d.
func expandedStream(d *Document) ([]edit.Value, error) {
	var out []edit.Value
	for i := 0; i < d.Len(); i++ {
		c := edit.NewCtx(&d.s.tree, d.s.src, i)
		budget := 20000
		v, err := expandNode(c, d.s.tree.Docs[i].Root, &budget, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func expandNode(c *edit.Ctx, n int32, budget *int, depth int) (edit.Value, error) {
	*budget--
	if *budget < 0 || depth > 300 {
		return edit.Value{}, errors.New("expansion too large")
	}
	for hops := 0; c.T.Nodes[n].Kind == cst.Alias; hops++ {
		a := c.AnchorOf(n)
		if a == 0 || hops > 100 {
			return edit.Value{}, errors.New("undefined alias")
		}
		n = a
	}
	nd := &c.T.Nodes[n]
	switch nd.Kind {
	case cst.Mapping, cst.Sequence:
		v := edit.Value{Kind: edit.Seq}
		if nd.Kind == cst.Mapping {
			v.Kind = edit.Map
		}
		for ch := c.T.First(n); ch != 0; ch = c.T.Nodes[ch].Next {
			x, err := expandNode(c, ch, budget, depth+1)
			if err != nil {
				return v, err
			}
			v.Items = append(v.Items, x)
		}
		return v, nil
	}
	return edit.FromTree(c.T, c.Src, n)
}

func hasAliases(d *Document) bool {
	for i := range d.s.tree.Nodes {
		if d.s.tree.Nodes[i].Kind == cst.Alias {
			return true
		}
	}
	return false
}

var (
	errModelNotFound = ErrNotFound
	errModelInvalid  = ErrInvalidPatch
	errModelTest     = ErrTestFailed
)

func modelGet(root *edit.Value, toks []string) (*edit.Value, error) {
	cur := root
	for _, tok := range toks {
		next, err := modelStep(cur, tok)
		if err != nil {
			return nil, errModelNotFound
		}
		cur = next
	}
	return cur, nil
}

func modelEqual(a, b edit.Value) bool {
	a, b = canon(a), canon(b)
	var eq func(a, b edit.Value) bool
	eq = func(a, b edit.Value) bool {
		if a.Kind != b.Kind {
			return false
		}
		switch a.Kind {
		case edit.Seq:
			if len(a.Items) != len(b.Items) {
				return false
			}
			for i := range a.Items {
				if !eq(a.Items[i], b.Items[i]) {
					return false
				}
			}
			return true
		case edit.Map:
			if len(a.Items) != len(b.Items) {
				return false
			}
			used := make([]bool, len(b.Items)/2)
			for i := 0; i < len(a.Items); i += 2 {
				found := false
				for j := 0; j < len(b.Items); j += 2 {
					if !used[j/2] && a.Items[i].Kind == edit.String && b.Items[j].Kind == edit.String && a.Items[i].Text == b.Items[j].Text {
						if !eq(a.Items[i+1], b.Items[j+1]) {
							return false
						}
						used[j/2], found = true, true
						break
					}
				}
				if !found {
					return false
				}
			}
			return true
		}
		return a.Text == b.Text
	}
	return eq(a, b)
}

type genOp struct {
	Op, Path, From string
	Value          *edit.Value
}

func (o genOp) json() []byte {
	m := map[string]json.RawMessage{}
	s := func(x string) json.RawMessage { b, _ := json.Marshal(x); return b }
	m["op"], m["path"] = s(o.Op), s(o.Path)
	var b bytes.Buffer
	b.WriteString(`{"op":`)
	b.Write(m["op"])
	b.WriteString(`,"path":`)
	b.Write(m["path"])
	if o.Op == "move" || o.Op == "copy" {
		b.WriteString(`,"from":`)
		b.Write(s(o.From))
	}
	if o.Value != nil {
		b.WriteString(`,"value":`)
		writeJSON(&b, *o.Value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func writeJSON(b *bytes.Buffer, v edit.Value) {
	switch v.Kind {
	case edit.Map:
		b.WriteByte('{')
		for i := 0; i < len(v.Items); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			k, _ := json.Marshal(v.Items[i].Text)
			b.Write(k)
			b.WriteByte(':')
			writeJSON(b, v.Items[i+1])
		}
		b.WriteByte('}')
	case edit.Seq:
		b.WriteByte('[')
		for i := range v.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, v.Items[i])
		}
		b.WriteByte(']')
	case edit.String:
		s, _ := json.Marshal(v.Text)
		b.Write(s)
	default:
		v = canon(v)
		text := v.Text
		if v.Kind == edit.Float && !strings.ContainsAny(text, ".eE") {
			text += ".0"
		}
		b.WriteString(text)
	}
}

// jsonSafe reports whether v can be written as JSON with the same meaning.
func jsonSafe(v edit.Value) bool {
	// A scalar that an explicit tag left empty (an empty !!int) is no number in
	// the data model, so there is no JSON text for it.
	if (v.Kind == edit.Int || v.Kind == edit.Float || v.Kind == edit.Bool) && v.Text == "" {
		return false
	}
	if v.Kind == edit.Float {
		text := canon(v).Text
		if strings.ContainsAny(text, "NI") {
			return false
		}
	}
	// Invalid UTF-8 has no JSON text: the encoder writes U+FFFD, which is a
	// different string and can collide with another name.
	if v.Kind == edit.String && !utf8.ValidString(v.Text) {
		return false
	}
	if v.Kind == edit.Map {
		seen := map[string]bool{}
		for i := 0; i < len(v.Items); i += 2 {
			if v.Items[i].Kind != edit.String || !utf8.ValidString(v.Items[i].Text) || seen[v.Items[i].Text] {
				return false
			}
			seen[v.Items[i].Text] = true
		}
	}
	for _, c := range v.Items {
		if !jsonSafe(c) {
			return false
		}
	}
	return true
}

// modelPatch applies ops to the model, returning the index and error of the first
// operation that fails.
func modelPatch(model []edit.Value, doc int, ops []genOp) (int, error) {
	if doc >= len(model) {
		return 0, errModelNotFound
	}
	for i, o := range ops {
		root := model[doc]
		toks, err := pointer.Parse(o.Path)
		if err != nil {
			return i, errModelInvalid
		}
		apply := func(kind editKindOp, v *edit.Value) error {
			if kind != opRemove && len(toks) == 0 {
				root = cloneValue(*v)
				return nil
			}
			m, err := modelApply(root, kind, o.Path, v)
			if err != nil {
				return errModelNotFound
			}
			root = m
			return nil
		}
		root = cloneValue(root)
		switch o.Op {
		case "add":
			err = apply(opAdd, o.Value)
		case "replace":
			err = apply(opReplace, o.Value)
		case "remove":
			if len(toks) == 0 {
				return i, errModelNotFound
			}
			err = apply(opRemove, nil)
		case "test":
			var got *edit.Value
			if got, err = modelGet(&root, toks); err == nil && !modelEqual(*got, *o.Value) {
				err = errModelTest
			}
		case "copy", "move":
			ft, perr := pointer.Parse(o.From)
			if perr != nil {
				return i, errModelInvalid
			}
			if o.Op == "move" && len(toks) > len(ft) && slices.Equal(ft, toks[:len(ft)]) {
				return i, errModelInvalid
			}
			var got *edit.Value
			if got, err = modelGet(&root, ft); err != nil {
				return i, err
			}
			v := nullKeysWritten(cloneValue(*got))
			if o.From == o.Path {
				// Moving onto itself, or copying onto the same mapping key, changes
				// nothing; a copy onto its own sequence index adds a second copy there
				// (RFC 6902, section 4.5).
				inSeq := false
				if len(ft) > 0 {
					if par, perr := modelGet(&root, ft[:len(ft)-1]); perr == nil && par.Kind == edit.Seq {
						inSeq = true
					}
				}
				if o.Op == "move" || !inSeq {
					break
				}
			}
			if o.Op == "move" {
				if len(ft) == 0 {
					return i, errModelNotFound
				}
				if err = apply2(&root, opRemove, o.From, nil); err != nil {
					return i, err
				}
			}
			err = apply2(&root, opAdd, o.Path, &v)
		}
		if err != nil {
			return i, err
		}
		model[doc] = root
	}
	return -1, nil
}

// nullKeysWritten spells an empty key "null", as the writer does when it writes a
// value out (a copy of a mapping with an empty key addresses that key as "null").
func nullKeysWritten(v edit.Value) edit.Value {
	for i := range v.Items {
		v.Items[i] = nullKeysWritten(v.Items[i])
		if v.Kind == edit.Map && i%2 == 0 && v.Items[i].Kind == edit.Null && v.Items[i].Text == "" {
			v.Items[i].Text = "null"
		}
	}
	return v
}

func apply2(root *edit.Value, kind editKindOp, ptr string, v *edit.Value) error {
	if ptr == "" && kind != opRemove {
		*root = cloneValue(*v)
		return nil
	}
	m, err := modelApply(*root, kind, ptr, v)
	if err != nil {
		return errModelNotFound
	}
	*root = m
	return nil
}

// genPatch builds 1 to 4 random operations over the model, advancing a copy of
// the model as it goes so later operations address things that exist.
func genPatch(r *rand.Rand, model []edit.Value, doc int) []genOp {
	work := make([]edit.Value, len(model))
	for i := range model {
		work[i] = cloneValue(model[i])
	}
	var ops []genOp
	n := 1 + r.Intn(4)
	for len(ops) < n {
		var ptrs []string
		pointers(&work[doc], "", &ptrs)
		pick := func() string { return ptrs[r.Intn(len(ptrs))] }
		path := pick()
		switch r.Intn(6) {
		case 0:
			path += "/new" + fmt.Sprint(r.Intn(3))
		case 1:
			path += "/-"
		}
		var o genOp
		switch r.Intn(8) {
		case 0:
			o = genOp{Op: "remove", Path: pick()}
		case 1:
			v := randValue(r, 2)
			o = genOp{Op: "add", Path: path, Value: &v}
		case 2:
			v := randValue(r, 2)
			o = genOp{Op: "replace", Path: pick(), Value: &v}
		case 3:
			o = genOp{Op: "copy", From: pick(), Path: path}
		case 4:
			o = genOp{Op: "move", From: pick(), Path: path}
		case 5, 6:
			p := pick()
			var v edit.Value
			if got, err := modelGet(&work[doc], mustToks(p)); err == nil && jsonSafe(*got) && r.Intn(3) > 0 {
				v = reverseMaps(cloneValue(*got))
			} else {
				v = randValue(r, 1)
			}
			o = genOp{Op: "test", Path: p, Value: &v}
		default:
			v := randValue(r, 2)
			o = genOp{Op: "add", Path: path, Value: &v}
		}
		if o.Value != nil && !jsonSafe(*o.Value) {
			x := edit.Str("x")
			o.Value = &x
		}
		// A pointer into a key that is not valid UTF-8 cannot be written as JSON.
		if !utf8.ValidString(o.Path) || !utf8.ValidString(o.From) {
			continue
		}
		ops = append(ops, o)
		// Advance the working model; stop the patch where it stops being valid
		// half the time, so failing patches occur too.
		if _, err := modelPatch(work, doc, []genOp{o}); err != nil {
			if r.Intn(2) == 0 {
				break
			}
		}
	}
	return ops
}

func mustToks(p string) []string { t, _ := pointer.Parse(p); return t }

func reverseMaps(v edit.Value) edit.Value {
	for i := range v.Items {
		v.Items[i] = reverseMaps(v.Items[i])
	}
	if v.Kind == edit.Map {
		for i, j := 0, len(v.Items)-2; i < j; i, j = i+2, j-2 {
			v.Items[i], v.Items[j] = v.Items[j], v.Items[i]
			v.Items[i+1], v.Items[j+1] = v.Items[j+1], v.Items[i+1]
		}
	}
	return v
}

func isRefusal(err error) bool {
	return errors.Is(err, errEdit) || errors.Is(err, ErrUnsupportedEdit) || errors.Is(err, ErrAmbiguousKey) ||
		errors.Is(err, ErrAliasWrite) || errors.Is(err, ErrAnchorInUse)
}

var patchStats struct{ ok, failed, refused, relaxed int }

// randomPatches applies n random patches to d. With checked set they are checked
// against the model; without it (arbitrary fuzzed documents) only atomicity, a
// readable result and the absence of panics are checked.
func randomPatches(d *Document, r *rand.Rand, n int, checked bool) error {
	for step := 0; step < n; step++ {
		if d.Len() == 0 || len(d.Bytes()) > 1<<14 {
			// Copying a node into itself doubles the document; stop before it is huge.
			return nil
		}
		doc := r.Intn(d.Len())
		model, err := expandedStream(d)
		if err != nil {
			return nil // an alias bomb or undefined alias: not this test's subject
		}
		strict := checked && !hasAliases(d)
		ops := genPatch(r, model, doc)
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, o := range ops {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(o.json())
		}
		buf.WriteByte(']')
		in := append([]byte(nil), d.Bytes()...)
		wantModel := make([]edit.Value, len(model))
		for i := range model {
			wantModel[i] = cloneValue(model[i])
		}
		failAt, modelErr := modelPatch(wantModel, doc, ops)
		err = d.Doc(doc).ApplyPatch(buf.Bytes())
		if err != nil {
			var ee *EditError
			if !errors.As(err, &ee) || !ee.Patch {
				return fmt.Errorf("step %d: error is not a patch *EditError: %v\npatch %s", step, err, buf.Bytes())
			}
			if !bytes.Equal(d.Bytes(), in) {
				return fmt.Errorf("step %d: failed patch changed the document\npatch %s\n in: %q\nout: %q (%v)", step, buf.Bytes(), in, d.Bytes(), err)
			}
			if fresh, perr := Parse(in); perr != nil || !reflect.DeepEqual(fresh.s.tree.Nodes, d.s.tree.Nodes) ||
				!reflect.DeepEqual(fresh.s.tree.Docs, d.s.tree.Docs) || !reflect.DeepEqual(fresh.s.tree.Props, d.s.tree.Props) {
				return fmt.Errorf("step %d: tree after a failed patch differs from a fresh parse of the input\npatch %s", step, buf.Bytes())
			}
			switch {
			case isRefusal(err) || !strict:
				patchStats.refused++
			case modelErr == nil:
				return fmt.Errorf("step %d: unexpected error %v\npatch %s\non %q", step, err, buf.Bytes(), in)
			case ee.Index != failAt || !errors.Is(err, modelErr):
				return fmt.Errorf("step %d: error %v at op %d, model says %v at op %d\npatch %s\non %q", step, err, ee.Index, modelErr, failAt, buf.Bytes(), in)
			default:
				patchStats.failed++
			}
			continue
		}
		patchStats.ok++
		if modelErr != nil && strict {
			return fmt.Errorf("step %d: patch succeeded but the model fails at op %d with %v\npatch %s\non %q", step, failAt, modelErr, buf.Bytes(), in)
		}
		var fresh cst.Tree
		if err := cst.Build(&fresh, d.s.src, -1); err != nil {
			return fmt.Errorf("step %d: output does not parse: %v\npatch %s", step, err, buf.Bytes())
		}
		if !reflect.DeepEqual(fresh.Nodes, d.s.tree.Nodes) || !reflect.DeepEqual(fresh.Docs, d.s.tree.Docs) || !reflect.DeepEqual(fresh.Props, d.s.tree.Props) {
			return fmt.Errorf("step %d: kept tree differs from a fresh parse\npatch %s", step, buf.Bytes())
		}
		reparsed, err := Parse(d.Bytes())
		if err != nil {
			return err
		}
		got, err := expandedStream(reparsed)
		if err != nil {
			return fmt.Errorf("step %d: output no longer expands: %v\npatch %s\nout %q", step, err, buf.Bytes(), d.Bytes())
		}
		if !strict {
			patchStats.relaxed++
			continue
		}
		if !reflect.DeepEqual(canonAll(got), canonAll(wantModel)) {
			return fmt.Errorf("step %d: read-back differs from the model\npatch %s\non  %q\nout %q\n got %+v\nwant %+v", step, buf.Bytes(), in, d.Bytes(), canonAll(got), canonAll(wantModel))
		}
		// A lone replace leaves every line outside the value alone.
		if len(ops) == 1 && ops[0].Op == "replace" {
			// replaceWindow reads the tree of the input, which is gone; re-parse it.
			prev, _ := Parse(in)
			h, tl := replaceWindow(prev, doc, opReplace, ops[0].Path)
			out := d.Bytes()
			if len(out) < h+tl || !bytes.Equal(out[:h], in[:h]) || !bytes.Equal(out[len(out)-tl:], in[len(in)-tl:]) {
				return fmt.Errorf("step %d: replace rewrote text outside the value\n in: %q\nout: %q", step, in, out)
			}
		}
	}
	return nil
}

func TestRandomPatches(t *testing.T) {
	iters := 150
	if testing.Short() {
		iters = 25
	}
	patchStats = struct{ ok, failed, refused, relaxed int }{}
	for si, seed := range propSeeds {
		for i := 0; i < iters; i++ {
			r := rand.New(rand.NewSource(int64(si*100000 + i)))
			d, err := Parse([]byte(seed))
			if err != nil {
				t.Fatalf("seed %d: %v", si, err)
			}
			if err := randomPatches(d, r, 6, true); err != nil {
				t.Fatalf("seed %d run %d: %v", si, i, err)
			}
		}
	}
	t.Logf("patches: %d applied, %d failed as predicted, %d refused or unchecked, %d compared without sharing", patchStats.ok, patchStats.failed, patchStats.refused, patchStats.relaxed)
}

func TestRandomPatchesOnKitInputs(t *testing.T) {
	seeds := kitSeeds(t)
	if len(seeds) == 0 {
		t.Skip("ONEYAML_KIT is not set")
	}
	iters := 12
	if testing.Short() {
		iters = 2
	}
	patchStats = struct{ ok, failed, refused, relaxed int }{}
	for si, seed := range seeds {
		for i := 0; i < iters; i++ {
			r := rand.New(rand.NewSource(int64(si*1000 + i)))
			d, err := Parse(seed)
			if err != nil {
				break
			}
			if err := randomPatches(d, r, 5, true); err != nil {
				t.Fatalf("input %q run %d: %v", strings.TrimSpace(string(seed)), i, err)
			}
		}
	}
	t.Logf("patches: %d applied, %d failed as predicted, %d refused or unchecked, %d compared without sharing", patchStats.ok, patchStats.failed, patchStats.refused, patchStats.relaxed)
}

// FuzzPatch applies a random patch sequence to fuzzed documents: no panic, and a
// failed patch changes nothing.
func FuzzPatch(f *testing.F) {
	for _, s := range propSeeds {
		f.Add([]byte(s), int64(3), []byte(nil))
	}
	f.Add([]byte("a: 1\n"), int64(1), []byte(`[{"op":"move","from":"/a","path":"/b"},{"op":"test","path":"/b","value":1}]`))
	f.Add([]byte("a: &x 1\nb: *x\n"), int64(1), []byte(`{"a":null,"c":{"d":1}}`))
	for _, s := range kitSeeds(f) {
		f.Add(s, int64(7), []byte(nil))
	}
	f.Fuzz(func(t *testing.T, data []byte, seed int64, raw []byte) {
		d, err := Parse(data)
		if err != nil {
			return
		}
		// Arbitrary patch text: errors are fine, a changed document after an
		// error is not.
		in := append([]byte(nil), d.Bytes()...)
		err = d.ApplyPatch(raw)
		if err != nil && !bytes.Equal(d.Bytes(), in) {
			t.Fatalf("ApplyPatch failed (%v) and changed the document", err)
		}
		if !utf8.Valid(raw) && !errors.Is(err, ErrInvalidPatch) {
			t.Fatalf("ApplyPatch accepted or misreported patch text that is not UTF-8: %v", err)
		}
		in = append(in[:0], d.Bytes()...)
		err = d.ApplyMergePatch(raw)
		if err != nil && !bytes.Equal(d.Bytes(), in) {
			t.Fatalf("ApplyMergePatch failed (%v) and changed the document", err)
		}
		if !utf8.Valid(raw) && !errors.Is(err, ErrInvalidPatch) {
			t.Fatalf("ApplyMergePatch accepted or misreported patch text that is not UTF-8: %v", err)
		}
		if err := randomPatches(d, rand.New(rand.NewSource(seed)), 4, false); err != nil {
			t.Fatal(err)
		}
	})
}
