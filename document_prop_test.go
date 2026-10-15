package oneyaml

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/pointer"
)

// pointers lists a pointer to every node of v, the root included.
func pointers(v *edit.Value, prefix string, out *[]string) {
	*out = append(*out, prefix)
	switch v.Kind {
	case edit.Map:
		for i := 0; i+1 < len(v.Items); i += 2 {
			k := v.Items[i]
			if k.Kind == edit.Map || k.Kind == edit.Seq {
				continue
			}
			pointers(&v.Items[i+1], prefix+"/"+pointer.Escape(k.Text), out)
		}
	case edit.Seq:
		for i := range v.Items {
			pointers(&v.Items[i], prefix+"/"+strconv.Itoa(i), out)
		}
	}
}

var awkwardStrings = []string{
	"", "x", "hello world", "yes", "true", "null", "~", "12", "1.5", "0x1F", "a: b", "a #b", "# c", " lead", "trail ",
	"it's", `say "hi"`, "back\\slash", "line\nbreak", "two\n\nblank\n", "tail\n", "tail\n\n", "\nlead", "  indented\nx\n",
	"tab\there", "- dash", "? q", ": colon", "[a]", "{a}", "a,b", "*star", "&amp", "!bang", "|pipe", ">gt", "%pct", "@at", "`tick`",
	"日本語", "émoji 😀", "\x01ctl", "\r\n", "---", "...", "<<", "a\n b\n", "x\n\n\n", "  ", "\n", "a\tb\n c",
}

func randScalar(r *rand.Rand) edit.Value {
	switch r.Intn(9) {
	case 7:
		// A []byte, which Marshal writes as !!binary.
		return edit.Value{Kind: edit.String, Tag: "!!binary", Text: []string{"aGk=", "aGVsbG8gd29ybGQ=", ""}[r.Intn(3)]}
	case 8:
		return edit.Value{Kind: edit.String, Tag: []string{"!foo", "!!timestamp", "!!binary"}[r.Intn(3)], Text: []string{"2001-12-14", "x", "7"}[r.Intn(3)]}
	case 0:
		return edit.Value{Kind: edit.Null, Text: "null"}
	case 1:
		return edit.Value{Kind: edit.Bool, Text: strconv.FormatBool(r.Intn(2) == 0)}
	case 2:
		return edit.Value{Kind: edit.Int, Text: strconv.Itoa(r.Intn(2000) - 1000)}
	case 3:
		return edit.Value{Kind: edit.Float, Text: strconv.Itoa(r.Intn(1000)) + ".5"}
	}
	return edit.Str(awkwardStrings[r.Intn(len(awkwardStrings))])
}

func randValue(r *rand.Rand, depth int) edit.Value {
	if depth <= 0 || r.Intn(3) > 0 {
		return randScalar(r)
	}
	n := r.Intn(4)
	if r.Intn(2) == 0 {
		v := edit.Value{Kind: edit.Seq}
		for i := 0; i < n; i++ {
			v.Items = append(v.Items, randValue(r, depth-1))
		}
		return v
	}
	v := edit.Value{Kind: edit.Map}
	for i := 0; i < n; i++ {
		v.Items = append(v.Items, edit.Str(fmt.Sprintf("k%d%s", i, []string{"", " x", "-", "\"q"}[r.Intn(4)])), randValue(r, depth-1))
	}
	return v
}

var editStats struct{ ok, refused, unsupported int }

var editSentinels = []error{ErrNotFound, ErrAliasWrite, ErrAmbiguousKey, ErrAnchorInUse, ErrUnsupportedEdit, ErrInvalidPointer}

// A strict run treats errEdit as a failure; the fuzzer only checks that the
// document is untouched, because arbitrary input reaches layouts (empty explicit
// keys, nodes with properties on separate lines) that the engine refuses.
func expectedEditError(err error) bool {
	for _, s := range editSentinels {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

// randomEdits applies n random edits to d, checking the read-back oracle after
// each. It returns a description of the first problem.
func randomEdits(d *Document, r *rand.Rand, n int, strict bool) error {
	for step := 0; step < n; step++ {
		before, err := streamValues(d)
		if err != nil {
			return err
		}
		if len(before) == 0 {
			return nil
		}
		doc := r.Intn(len(before))
		var ptrs []string
		pointers(&before[doc], "", &ptrs)
		ptr := ptrs[r.Intn(len(ptrs))]
		switch r.Intn(6) {
		case 0:
			ptr += "/new" + strconv.Itoa(r.Intn(3))
		case 1:
			ptr += "/-"
		}
		var op editKindOp
		switch r.Intn(5) {
		case 0:
			op = opRemove
		case 1:
			op = opAdd
		case 2:
			op = opReplace
		default:
			op = opSet
		}
		var v *edit.Value
		if op != opRemove {
			val := randValue(r, 3)
			v = &val
		}
		snap := d.s.snapshot()
		hadAlias := hasAliases(d)
		in := append([]byte(nil), d.Bytes()...)
		// The window is in offsets of the UTF-8 text the editor works on, which is
		// not the text Bytes returns for a UTF-16 or UTF-32 document.
		inSrc := append([]byte(nil), d.s.src...)
		keepHead, keepTail := replaceWindow(d, doc, op, ptr)
		err = d.s.edit(doc, op, ptr, v)
		if err != nil {
			// Outside strict mode (the fuzzer), an edit the engine could not write
			// so that it reads back exactly is a safe refusal, not a failure: the
			// document is checked to be unchanged below.
			if !expectedEditError(err) && (strict || !errors.Is(err, errEdit)) {
				return fmt.Errorf("step %d: %s %q on %q value %+v: unexpected error %v\n%s", step, op.name(), ptr, in, v, err, explainEdit(d, doc, op, ptr, v))
			}
			editStats.refused++
			if errors.Is(err, ErrUnsupportedEdit) {
				editStats.unsupported++
				if os.Getenv("ONEYAML_SHOW_UNSUPPORTED") != "" {
					fmt.Printf("unsupported: %s %q on %q: %v\n", op.name(), ptr, in, err)
				}
			}
			if !bytes.Equal(d.Bytes(), in) {
				return fmt.Errorf("step %d: failed edit changed the document", step)
			}
			if err := sameAsFreshParse(d); err != nil {
				return fmt.Errorf("step %d: after a failed %s %q: %v", step, op.name(), ptr, err)
			}
			continue
		}
		editStats.ok++
		var fresh cst.Tree
		if err := cst.Build(&fresh, d.s.src, -1); err != nil {
			return fmt.Errorf("step %d: output does not parse: %v", step, err)
		}
		if !reflect.DeepEqual(fresh.Nodes, d.s.tree.Nodes) || !reflect.DeepEqual(fresh.Props, d.s.tree.Props) || !reflect.DeepEqual(fresh.Docs, d.s.tree.Docs) {
			return fmt.Errorf("step %d: %s %q: the tree kept after the edit differs from a fresh parse of %q", step, op.name(), ptr, d.s.src)
		}
		if out := d.s.src; keepHead > 0 || keepTail > 0 {
			if len(out) < keepHead+keepTail || !bytes.Equal(out[:keepHead], inSrc[:keepHead]) ||
				!bytes.Equal(out[len(out)-keepTail:], inSrc[len(inSrc)-keepTail:]) {
				d.s.restore(snap)
				return fmt.Errorf("step %d: %s %q rewrote text outside the edited lines\n in: %q\nout: %q", step, op.name(), ptr, in, out)
			}
		}
		if !hadAlias {
			if err := checkDecoded(in, d.Bytes(), d, doc, op, ptr, v); err != nil {
				d.s.restore(snap)
				return fmt.Errorf("step %d: %s %q on %q value %+v: %v\nout: %q", step, op.name(), ptr, in, v, err, d.Bytes())
			}
		}
		if err := checkReadBack(before, d, doc, op, ptr, v); err != nil {
			d.s.restore(snap)
			return fmt.Errorf("step %d: %s %q on %q: %w\nout: %q", step, op.name(), ptr, in, err, d.Bytes())
		}
	}
	return nil
}

var propSeeds = []string{
	"a: 1\nb: [1, 2, {c: d}]\nc:\n  - x\n  - y: 1\n    z: |\n      text\n\n  - 'q'\n",
	"# top\nk: &a\n  x: 1 # one\n  y: [p, q]\nr: *a\ns: !!str 5\n# end\n",
	"- a\n- - b\n  - c\n- d: 1\n  e: >\n    folded\n    text\n- \"q\"\n- ~\n",
	"---\na: 1\n...\n---\n- x\n- y\n",
	"{a: 1, b: [x, y], c: {d: e}}\n",
	"? complex\n: value\nk:\n- a\n- b\nm:\n    n: 1\n    o:\n        - p\n",
	"a: |+\n  keep\n\nb: >-\n  strip\nc: |2\n    two\n",
	"a:\r\n  b: 1\r\n  c: [1,\r\n    2]\r\n",
	"x: 'single'\ny: \"double\"\nz: plain\n",
	"top: {}\nl: []\nn:\nm: \n",
	"a: !!binary aGVsbG8gd29ybGQ=\nb: !foo longoldvaluexxxxxxx\nc: ! 12\nd: !!str 12\ne: !!timestamp 2001-12-14\nf: !!int '7'\ng: [!foo a, ! b, !!binary Zg==]\n",
	"- !!binary aGk=\n- ! 5\n- !custom {a: 1}\n- !!set {x, y}\n- k: ! v\n  j: !foo 3\n",
	"a: [1, 2, 3, {x: 1, y: [4, 5]}, k: v, ? q : r]\nb: {p: 1, q: [1, 2], r: {s: t}, u}\n",
	"[1, [2, 3], {a: b}, # c\n  4, 5]\n",
	"k: [a, b,\n  c, # note\n  d]\nm: {a: 1,\n  b: 2, c: [x, y,\n    z]}\nn: [{a: 1}, {b: 2}, {c: [3]}]\n",
}

func TestRandomEdits(t *testing.T) {
	iters := 300
	if testing.Short() {
		iters = 40
	}
	for si, seed := range propSeeds {
		for i := 0; i < iters; i++ {
			r := rand.New(rand.NewSource(int64(si*100000 + i)))
			d, err := Parse([]byte(seed))
			if err != nil {
				t.Fatalf("seed %d: %v", si, err)
			}
			if err := randomEdits(d, r, 12, true); err != nil {
				t.Fatalf("seed %d run %d: %v", si, i, err)
			}
		}
	}
}

// kitSeeds returns the YAML inputs of the kit's edit and round trip cases.
func kitSeeds(t testing.TB) [][]byte {
	root := os.Getenv("ONEYAML_KIT")
	if root == "" {
		return nil
	}
	var out [][]byte
	for _, cat := range []string{"roundtrip", "edit"} {
		m, _ := filepath.Glob(filepath.Join(root, "suites", "own", "cases", cat, "*", "input*"))
		for _, f := range m {
			if b, err := os.ReadFile(f); err == nil {
				out = append(out, b)
			}
		}
	}
	return out
}

// TestRandomEditsOnKitInputs runs random edits over every kit input.
func TestRandomEditsOnKitInputs(t *testing.T) {
	seeds := kitSeeds(t)
	if len(seeds) == 0 {
		t.Skip("ONEYAML_KIT is not set")
	}
	iters := 40
	if testing.Short() {
		iters = 2
	}
	skipped := 0
	defer func() {
		t.Logf("edits applied %d, refused %d (unsupported layouts %d), inputs not parsed %d", editStats.ok, editStats.refused, editStats.unsupported, skipped)
	}()
	for si, seed := range seeds {
		for i := 0; i < iters; i++ {
			r := rand.New(rand.NewSource(int64(si*1000 + i)))
			d, err := Parse(seed)
			if err != nil {
				skipped++
				break
			}
			if err := randomEdits(d, r, 8, true); err != nil {
				t.Fatalf("input %q run %d: %v", strings.TrimSpace(string(seed)), i, err)
			}
		}
	}
}

func FuzzDocument(f *testing.F) {
	for _, s := range propSeeds {
		f.Add([]byte(s), int64(1))
	}
	for _, s := range kitSeeds(f) {
		f.Add(s, int64(7))
	}
	f.Fuzz(func(t *testing.T, data []byte, seed int64) {
		d, err := Parse(data)
		if err != nil {
			return
		}
		if !bytes.Equal(d.Bytes(), data) {
			t.Fatalf("Bytes differs from the input")
		}
		// Reading must not panic, whatever the shape.
		var x any
		_ = d.Get("", &x)
		_ = d.Root().Tag()
		if err := randomEdits(d, rand.New(rand.NewSource(seed)), 6, false); err != nil {
			t.Fatal(err)
		}
	})
}

// replaceWindow returns how many leading and trailing bytes of the stream a
// replacement of the value at ptr must leave alone: everything before the first
// line of the value and after the line that follows its last.
func replaceWindow(d *Document, doc int, op editKindOp, ptr string) (head, tail int) {
	if op != opSet && op != opReplace {
		return 0, 0
	}
	toks, err := pointer.Parse(ptr)
	if err != nil {
		return 0, 0
	}
	c := edit.NewCtx(&d.s.tree, d.s.src, doc)
	loc, err := c.Resolve(toks, true)
	if err != nil || loc.Node == 0 {
		return 0, 0
	}
	src := d.s.src
	nd := &d.s.tree.Nodes[loc.Node]
	s := int(d.s.tree.FullStart(loc.Node))
	if p := d.s.tree.Nodes[nd.Parent]; nd.Parent != 0 && p.Kind == cst.Mapping && !p.Flow && !d.s.tree.IsKey(loc.Node) {
		s = int(d.s.tree.Nodes[loc.Key].Start)
	}
	e := int(nd.End)
	// Widen to whole lines. A nested value that becomes a scalar moves up to its
	// indicator's line, past any blank or comment-only lines in between, and a
	// block value moves to the line after.
	lineStart := func(i int) int {
		for i > 0 && src[i-1] != '\n' && src[i-1] != '\r' {
			i--
		}
		return i
	}
	s = lineStart(s)
	for s > 0 {
		p := lineStart(s - 1)
		if s-1 > 0 && src[s-1] == '\n' && src[s-2] == '\r' {
			p = lineStart(s - 2)
		}
		line := strings.TrimSpace(string(src[p:s]))
		s = p
		if line != "" && !strings.HasPrefix(line, "#") {
			break
		}
	}
	for i := 0; i < 2 && e < len(src); i++ {
		for e < len(src) && src[e] != '\n' && src[e] != '\r' {
			e++
		}
		for e < len(src) && (src[e] == '\n' || src[e] == '\r') {
			e++
		}
	}
	return s, len(src) - e
}
