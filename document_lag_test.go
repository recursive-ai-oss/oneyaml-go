package oneyaml

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/cst"
)

// A run of Sets leaves the offsets far down the tree stale (see
// cst.ShiftAfterNode). These tests run every edit twice, once the lazy way and once
// shifting every offset, and compare what a reader can see after each step. With
// the poison on, a stale offset is stored far from the truth, so a read that did
// not settle first fails loudly.

func lagShapes() map[string]string {
	var wide, recs, anch, multi, flow, cmt strings.Builder
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&wide, "k%d: %d\n", i, i%7)
	}
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&recs, "- id: %d\n  name: n%d\n  tags: [a, b]\n  empty:\n  sub:\n    x: 'q'\n    y: \"z\"\n", i, i)
	}
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&anch, "a%d: &an%d 1\nb%d: !!str 2\nc%d: *an%d\nd%d: &bn%d {x: 1}\n", i, i, i, i, i, i, i)
	}
	multi.WriteString("a: 1\nb: 2\n---\nc: 3\nd: 4\n...\n---\n- 5\n- 6\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&flow, "r%d: {a: 1, b: [2, 3], c: x}\n", i)
	}
	for i := 0; i < 90; i++ {
		fmt.Fprintf(&cmt, "# head %d\nk%d: v # line\n\nm%d: |\n  text %d\n", i, i, i, i)
	}
	return map[string]string{"wide": wide.String(), "records": recs.String(), "anchors": anch.String(),
		"multi": multi.String(), "flow": flow.String(), "comments": cmt.String()}
}

// leafPointers lists the pointers of the scalars of document doc.
func leafPointers(d *Document) []string {
	var out []string
	var walk func(n Node)
	walk = func(n Node) {
		switch n.Kind() {
		case MappingNode:
			for k, v := range n.Entries() {
				_ = k
				walk(v)
			}
		case SequenceNode:
			for it := range n.Items() {
				walk(it)
			}
		default:
			if p, err := n.Pointer(); err == nil {
				out = append(out, p)
			}
		}
	}
	walk(d.Root())
	return out
}

// nodeAtPointer finds the node at ptr by walking the collections with Entries and
// Items, which read the keys and no other offsets, so that nothing is settled that
// the caller does not read.
func nodeAtPointer(d *Document, ptr string) Node {
	n := d.Root()
	if ptr == "" {
		return n
	}
	for _, tok := range strings.Split(ptr[1:], "/") {
		tok = strings.NewReplacer("~1", "/", "~0", "~").Replace(tok)
		var next Node
		switch n.Kind() {
		case MappingNode:
			for k, v := range n.Entries() {
				if k.Value() == tok {
					next = v
					break
				}
			}
		case SequenceNode:
			var i int
			fmt.Sscan(tok, &i)
			for it := range n.Items() {
				if i == 0 {
					next = it
					break
				}
				i--
			}
		}
		if !next.IsValid() {
			return Node{}
		}
		n = next
	}
	return n
}

// describe is everything a reader can ask a node.
func describe(n Node) string {
	if !n.IsValid() {
		return "invalid"
	}
	l, c := n.Position()
	s, e := n.Span()
	p, _ := n.Pointer()
	return fmt.Sprint(n.Kind(), n.Style(), n.Value(), n.Source(), l, c, s, e, p, n.Anchor(), n.Tag(), n.TagAsWritten(),
		n.LineComment(), "|", n.HeadComment(), n.Len())
}

var lagValues = []any{"x", "a longer value here", "1", "", "q: r", " lead", "trail ", "# not a comment", 12345, -1.5, true, nil,
	"multi\nline", "it's", `say "hi"`, "é ü", []any{1, 2}, map[string]any{"n": 1}, "*alias", "&anchor", "!tag"}

func lagRun(t *testing.T, name, src string, seed int64, steps int) {
	lagRunWith(t, name, src, seed, steps, nil, nil, false)
}

// lagRunWith is lagRun with parse options and extra pointers that the leaves do
// not list (through an alias, below a merged key, missing keys), which it edits now
// and then. With sparseBytes the bytes are compared only now and then, so that runs
// of Sets are not cut short by a read.
func lagRunWith(t *testing.T, name, src string, seed int64, steps int, opts []Option, extra []string, sparseBytes bool) {
	r := rand.New(rand.NewSource(seed))
	ref, err := Parse([]byte(src), opts...)
	if err != nil {
		t.Fatal(err)
	}
	lazy, _ := Parse([]byte(src), opts...)
	ptrs := leafPointers(ref)
	if len(ptrs) == 0 {
		return
	}
	// Edits go in runs: ascending, descending or anywhere, as loops and scripts do.
	pos, mode := 0, 0
	for step := 0; step < steps; step++ {
		if len(ptrs) == 0 {
			break
		}
		if step%25 == 0 {
			mode = r.Intn(4)
			pos = r.Intn(len(ptrs))
		}
		pos %= len(ptrs)
		switch mode {
		case 0:
			pos = (pos + 1) % len(ptrs)
		case 1:
			pos = (pos + len(ptrs) - 1) % len(ptrs)
		case 2:
			pos = (pos + 1 + r.Intn(5)) % len(ptrs)
		default:
			pos = r.Intn(len(ptrs))
		}
		ptr := ptrs[pos]
		if len(extra) > 0 && r.Intn(4) == 0 {
			ptr = extra[r.Intn(len(extra))]
		}
		var errR, errL error
		what := ""
		switch k := r.Intn(20); {
		case k < 14:
			v := lagValues[r.Intn(len(lagValues))]
			what = fmt.Sprintf("set %s %v", ptr, v)
			noLag = true
			errR = ref.Set(ptr, v)
			noLag = false
			errL = lazy.Set(ptr, v)
		case k == 14:
			what = "remove " + ptr
			errR, errL = ref.Remove(ptr), lazy.Remove(ptr)
			ptrs = leafPointers(ref)
			pos = 0
		case k == 15:
			what = "rename " + ptr
			if i := strings.LastIndex(ptr, "/"); i >= 0 {
				errR, errL = ref.Rename(ptr, "renamed"), lazy.Rename(ptr, "renamed")
				ptrs = leafPointers(ref)
			}
		case k == 16:
			what = "comment " + ptr
			errR, errL = ref.SetLineComment(ptr, "noted"), lazy.SetLineComment(ptr, "noted")
		case k == 17:
			what = "add " + ptr + "x"
			parent := ptr[:max(strings.LastIndex(ptr, "/"), 0)]
			errR, errL = ref.Set(parent+"/addedkey", "v"), lazy.Set(parent+"/addedkey", "v")
			ptrs = leafPointers(ref)
		default:
			// Reads between edits.
			what = "read " + ptr
			if a, b := describe(nodeAtPointer(ref, ptr)), describe(nodeAtPointer(lazy, ptr)); a != b {
				t.Fatalf("%s seed %d step %d %s: node differs\n ref %s\nlazy %s", name, seed, step, what, a, b)
			}
			var x, y any
			ea, eb := ref.Get(ptr, &x), lazy.Get(ptr, &y)
			if fmt.Sprint(ea) != fmt.Sprint(eb) || !reflect.DeepEqual(x, y) {
				t.Fatalf("%s seed %d step %d %s: Get differs: %v %v / %v %v", name, seed, step, what, x, ea, y, eb)
			}
		}
		if (errR == nil) != (errL == nil) || fmt.Sprint(errR) != fmt.Sprint(errL) {
			t.Fatalf("%s seed %d step %d %s: errors differ: ref %v lazy %v", name, seed, step, what, errR, errL)
		}
		if sparseBytes && step%7 != 0 {
			continue
		}
		if a, b := string(ref.Bytes()), string(lazy.Bytes()); a != b {
			t.Fatalf("%s seed %d step %d %s: bytes differ\n ref %q\nlazy %q", name, seed, step, what, a, b)
		}
		if step%9 == 0 && len(ptrs) > 0 {
			// A node read in the middle of a run, without settling everything.
			p := ptrs[r.Intn(len(ptrs))]
			if a, b := describe(nodeAtPointer(ref, p)), describe(nodeAtPointer(lazy, p)); a != b {
				t.Fatalf("%s seed %d step %d: node %s differs after %s\n ref %s\nlazy %s", name, seed, step, p, what, a, b)
			}
		}
		if step%11 == 0 {
			lazy.s.tree.Settle()
			if err := sameAsFreshParse(lazy); err != nil {
				t.Fatalf("%s seed %d step %d %s: %v", name, seed, step, what, err)
			}
		}
	}
	lazy.s.tree.Settle()
	if err := sameAsFreshParse(lazy); err != nil {
		t.Fatalf("%s seed %d end: %v", name, seed, err)
	}
}

func TestLaggingOffsetsMatchEagerOnes(t *testing.T) {
	prev := cst.LagPoison
	cst.LagPoison = 1 << 20
	defer func() { cst.LagPoison = prev }()
	seeds, steps := 40, 300
	if testing.Short() {
		seeds, steps = 6, 150
	}
	for name, src := range lagShapes() {
		for seed := int64(0); seed < int64(seeds); seed++ {
			lagRun(t, name, src, seed, steps)
		}
	}
}

// The work of a Set must not grow with the size of the mapping: the nodes shifted
// per edit are the same for a small and a large one.
func TestSetRunShiftsABoundedWindow(t *testing.T) {
	var shifted int
	cst.ShiftHook = func(n int) { shifted += n }
	defer func() { cst.ShiftHook = nil }()
	per := func(n int) float64 {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&sb, "k%d: %d\n", i, i%10)
		}
		d, err := Parse([]byte(sb.String()))
		if err != nil {
			t.Fatal(err)
		}
		shifted = 0
		for i := 0; i < n; i++ {
			if err := d.Set(fmt.Sprintf("/k%d", i), "changed value"); err != nil {
				t.Fatal(err)
			}
		}
		if got := string(d.Bytes()); got != strings.Repeat("", 0)+expectedSets(n) {
			t.Fatalf("n=%d: wrong text", n)
		}
		return float64(shifted) / float64(n)
	}
	small, large := per(2000), per(16000)
	if large > small*1.5+200 {
		t.Fatalf("nodes shifted per Set: %.0f for 2000 entries, %.0f for 16000", small, large)
	}
	if large > 400 {
		t.Fatalf("nodes shifted per Set = %.0f, want a small window", large)
	}
}

func expectedSets(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "k%d: changed value\n", i)
	}
	return sb.String()
}

// mergeAliasShapes are documents whose resolve walks merges and aliases while
// offsets lag: wide mappings (so that a key index exists) with long values that
// shrink and grow, merge keys, and aliases that an edit must refuse to write through.
func mergeAliasShapes() (map[string]string, []string) {
	var wide, merged strings.Builder
	fill := func(b *strings.Builder) {
		for i := 0; i < 300; i++ {
			fmt.Fprintf(b, "k%03d: %s\n", i, strings.Repeat("x", 200))
		}
	}
	wide.WriteString("a: &a {x: 1, y: 2}\n")
	fill(&wide)
	wide.WriteString("zz: *a\nzy: 1\n")
	merged.WriteString("base: &b {x: 1, y: 2}\nm: {<<: *b, own: 3}\n")
	fill(&merged)
	merged.WriteString("<<: *b\nzz: *b\nlast: 1\n")
	extra := []string{"/zz/x", "/zz", "/a/x", "/m/x", "/m/y", "/m/own", "/m/new", "/x", "/y", "/missing", "/zy", "/zz/q/r"}
	return map[string]string{"alias-wide": wide.String(), "merge-wide": merged.String()}, extra
}

func TestLaggingOffsetsWithMergesAndAliases(t *testing.T) {
	prev := cst.LagPoison
	cst.LagPoison = 1 << 20
	defer func() { cst.LagPoison = prev }()
	seeds, steps := 30, 200
	if testing.Short() {
		seeds, steps = 8, 120
	}
	shapes, extra := mergeAliasShapes()
	for name, src := range shapes {
		for _, merge := range []bool{false, true} {
			for _, sparse := range []bool{false, true} {
				var opts []Option
				if merge {
					opts = append(opts, MergeKeys())
				}
				for seed := int64(0); seed < int64(seeds); seed++ {
					lagRunWith(t, fmt.Sprintf("%s merge=%v sparse=%v", name, merge, sparse), src, seed, steps, opts, extra, sparse)
				}
			}
		}
	}
}

// Writing through an alias is refused with the alias error however many Sets came
// before and left the alias node's offsets stale.
func TestAliasWriteErrorAfterLazySets(t *testing.T) {
	prev := cst.LagPoison
	cst.LagPoison = 1 << 20
	defer func() { cst.LagPoison = prev }()
	var b strings.Builder
	b.WriteString("a: &a {x: 1}\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "k%03d: %s\n", i, strings.Repeat("x", 200))
	}
	b.WriteString("zz: *a\n")
	d, err := Parse([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		_ = d.Bytes()
		if err := d.Set(fmt.Sprintf("/k%03d", i), "y"); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Set("/zz/x", "v"); !errors.Is(err, ErrAliasWrite) {
		t.Fatalf("want the alias error, got %v", err)
	}
}

// With merge keys a Set on a wide mapping after lazy Sets does not read stale
// offsets of the keys it walks.
func TestMergeKeysSetAfterLazySets(t *testing.T) {
	prev := cst.LagPoison
	cst.LagPoison = 1 << 20
	defer func() { cst.LagPoison = prev }()
	var b strings.Builder
	b.WriteString("base: &b {x: 1}\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "k%03d: %s\n", i, strings.Repeat("x", 20))
	}
	b.WriteString("<<: *b\nzz: 1\n")
	d, err := Parse([]byte(b.String()), MergeKeys())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := d.Set(fmt.Sprintf("/k%03d", i), "y"); err != nil {
			t.Fatal(err)
		}
	}
	// Below a key that only the merge brings in: refused, and no panic.
	if err := d.Set("/x/q", "v"); err == nil {
		t.Fatal("want a refusal below a merged key")
	}
	for _, p := range []string{"/x", "/missing", "/zz"} {
		if err := d.Set(p, "v"); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	var m map[string]any
	if err := d.Get("", &m); err != nil || m["missing"] != "v" || m["x"] != "v" {
		t.Fatal(err, m["missing"], m["x"])
	}
}
