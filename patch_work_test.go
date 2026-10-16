package oneyaml

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// checkStoppedAtBound shows that a refused patch stopped when the bound was
// reached: the work its edits did, counted as the bound counts it, is not much
// past the bound. A refusal that came late would be the quadratic behaviour the
// bound exists to prevent.
func checkStoppedAtBound(t *testing.T, d *Document, le *LimitError) {
	t.Helper()
	w := d.s.ed.work
	cost := w.bytes + 8*w.nodes + w.shifted + w.moved/8
	if cost > 2*le.Max {
		t.Errorf("refused after work %d, bound %d", cost, le.Max)
	}
}

// bigBlockMapping is a block mapping of n short entries.
func bigBlockMapping(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "k%06d: %d\n", i, i)
	}
	return sb.String()
}

// swapScalars is a document with three long block scalars and a patch that copies
// two of them onto the first one in turn. The first scalar and its neighbour are
// most of the stream, so each copy is written by parsing the stream again.
func swapScalars(ops int) (src string, patch []byte) {
	block := func(c string) string { return "|\n" + strings.Repeat("  "+strings.Repeat(c, 70)+"\n", 900) }
	src = "a: |\n  x\n  y\nb: " + block("y") + "d: " + block("z")
	var p []string
	for i := 0; i < ops; i++ {
		from := "/b"
		if i%2 == 1 {
			from = "/d"
		}
		p = append(p, `{"op":"copy","from":"`+from+`","path":"/a"}`)
	}
	return src, []byte("[" + strings.Join(p, ",") + "]")
}

// A patch of many operations that each move or re-read much of the document costs
// more than its size allows. The work is counted, a patch past the bound is
// refused and undone, and the work done before refusing stays within the bound.
func TestPatchWorkIsBounded(t *testing.T) {
	removeSrc := bigBlockMapping(30000)
	// Removes with a test between them are separate edits; consecutive removes from
	// one mapping are made in one pass and cost no more than one parse.
	var rm []string
	for i := 0; i < 3000; i++ {
		rm = append(rm, fmt.Sprintf(`{"op":"remove","path":"/k%06d"}`, i), `{"op":"test","path":"/k029999","value":29999}`)
	}
	swapSrc, swapPatch := swapScalars(400)
	cases := []struct {
		name  string
		src   string
		patch []byte
		opts  []Option
	}{
		{"remove-first-keys", removeSrc, []byte("[" + strings.Join(rm, ",") + "]"), []Option{Untrusted()}},
		{"copy-over-long-scalar/untrusted", swapSrc, swapPatch, []Option{Untrusted()}},
		{"copy-over-long-scalar/default", swapSrc, swapPatch, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := mustParse(t, tc.src, tc.opts...)
			err := d.ApplyPatch(tc.patch)
			var le *LimitError
			if !errors.As(err, &le) || le.Limit != "patch work" {
				t.Fatalf("got %v", err)
			}
			if le.Max <= 0 || le.Option == "" {
				t.Fatalf("limit error without bound or option: %+v", le)
			}
			if string(d.Bytes()) != tc.src {
				t.Fatal("document changed")
			}
			if err := sameAsFreshParse(d); err != nil {
				t.Fatal(err)
			}
			checkStoppedAtBound(t, d, le)
		})
	}

	// A patch of a modest size is not in the way, and the patch the bound refuses
	// runs when the bound is off.
	d := mustParse(t, removeSrc, Untrusted())
	if err := d.ApplyPatch([]byte("[" + strings.Join(rm[:200], ",") + "]")); err != nil {
		t.Fatalf("modest patch: %v", err)
	}
	src, patch := swapScalars(400)
	if err := mustParse(t, src, NoLimits()).ApplyPatch(patch); err != nil {
		t.Fatalf("NoLimits: %v", err)
	}
	// The bound is set by the limit: a low ratio refuses a patch of removes that
	// the defaults let through.
	rmPatch := []byte("[" + strings.Join(rm, ",") + "]")
	low := mustParse(t, removeSrc, Limits(LimitSet{MaxPatchWorkRatio: 1}))
	var le *LimitError
	if err := low.ApplyPatch(rmPatch); !errors.As(err, &le) || le.Limit != "patch work" {
		t.Fatalf("low ratio: got %v", err)
	}
	if string(low.Bytes()) != removeSrc {
		t.Fatal("document changed under the low ratio")
	}
}

// Appending to a collection written in flow style is a region edit, so patches of
// thousands of appends run under every preset, and the work they are charged
// stays small.
func TestFlowAppendPatchesRun(t *testing.T) {
	val := strings.Repeat("y", 1024)
	build := func(n int, op func(i int) string) []byte {
		ops := []string{`{"op":"add","path":"/f","value":[]}`}
		for i := 0; i < n; i++ {
			ops = append(ops, op(i))
		}
		return []byte("[" + strings.Join(ops, ",") + "]")
	}
	patches := map[string][]byte{
		"add":  build(1500, func(int) string { return `{"op":"add","path":"/f/-","value":"` + val + `"}` }),
		"copy": build(1500, func(int) string { return `{"op":"copy","from":"/d","path":"/f/-"}` }),
	}
	for name, p := range patches {
		for preset, opts := range map[string][]Option{"untrusted": {Untrusted()}, "default": nil} {
			t.Run(name+"/"+preset, func(t *testing.T) {
				d := mustParse(t, "d: "+val+"\n", opts...)
				if err := d.ApplyPatch(p); err != nil {
					t.Fatalf("got %v", err)
				}
				w := d.s.ed.work
				if w.wholes > 4 || w.regions < 1400 {
					t.Errorf("whole parses %d, region edits %d", w.wholes, w.regions)
				}
				if w.bytes > 8*len(d.Bytes()) {
					t.Errorf("parsed %d bytes to write %d", w.bytes, len(d.Bytes()))
				}
				if err := sameAsFreshParse(d); err != nil {
					t.Fatal(err)
				}
				var got struct{ F []string }
				if err := Unmarshal(d.Bytes(), &got); err != nil || len(got.F) != 1500 {
					t.Fatalf("%d items, %v", len(got.F), err)
				}
			})
		}
	}
	// Four thousand copies of a 240-byte string into a sequence the patch creates.
	item := strings.Repeat("x", 240)
	big := build(4000, func(int) string { return `{"op":"copy","from":"/v","path":"/f/-"}` })
	for preset, opts := range map[string][]Option{"untrusted": {Untrusted()}, "default": nil} {
		d := mustParse(t, "a: 1\nv: "+item+"\n", opts...)
		if err := d.ApplyPatch(big); err != nil {
			t.Fatalf("4000 copies, %s: %v", preset, err)
		}
		t.Logf("4000 copies, %s: work %+v", preset, d.s.ed.work)
	}
}

// The bound holds for a merge patch too: one that deletes a member from each of
// many nested mappings is made of separate edits, is refused and undone; one that
// adds many members to a flow mapping runs.
func TestMergePatchWorkIsBounded(t *testing.T) {
	var src strings.Builder
	for i := 0; i < 30000; i++ {
		fmt.Fprintf(&src, "m%d:\n  x: 1\n  y: 2\n", i)
	}
	var sb strings.Builder
	sb.WriteString("{")
	for i := 0; i < 3000; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"m%d":{"x":null}`, i)
	}
	sb.WriteString("}")
	d := mustParse(t, src.String(), Untrusted())
	err := d.ApplyMergePatch([]byte(sb.String()))
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != "patch work" {
		t.Fatalf("got %v", err)
	}
	if string(d.Bytes()) != src.String() {
		t.Fatal("document changed")
	}
	if err := sameAsFreshParse(d); err != nil {
		t.Fatal(err)
	}
	checkStoppedAtBound(t, d, le)

	// Many members added to a flow mapping are cheap and succeed.
	sb.Reset()
	sb.WriteString(`{"f":{`)
	for i := 0; i < 1500; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"k%d":"%s"`, i, strings.Repeat("y", 1024))
	}
	sb.WriteString(`}}`)
	flow := mustParse(t, "f: {}\n", Untrusted())
	if err := flow.ApplyMergePatch([]byte(sb.String())); err != nil {
		t.Fatalf("flow members: %v", err)
	}
	var got struct{ F map[string]string }
	if err := Unmarshal(flow.Bytes(), &got); err != nil || len(got.F) != 1500 {
		t.Fatalf("%d members, %v", len(got.F), err)
	}
	if err := sameAsFreshParse(flow); err != nil {
		t.Fatal(err)
	}
}

// A snapshot that cannot be put back leaves the bytes of the snapshot and a
// document that refuses further edits, and the caller reports the failure.
func TestRollbackOfUnparseableSnapshot(t *testing.T) {
	d := mustParse(t, "a: 1\n")
	bad := []byte("a: \x8e\n")
	if err := d.s.rollback(docSnapshot{src: bad}); err == nil {
		t.Fatal("rollback to text that does not parse reported success")
	}
	if string(d.Bytes()) != string(bad) {
		t.Fatalf("bytes after rollback: %q", d.Bytes())
	}
	if err := d.Set("/b", 2); err == nil {
		t.Fatal("a lost document accepted an edit")
	}
}

// Under a MaxInputBytes limit the text of the patch is held to it as well.
func TestPatchTextIsBoundedByTheInputLimit(t *testing.T) {
	src := "a: 1\n"
	d := mustParse(t, src, Limits(LimitSet{MaxInputBytes: 100}))
	long := strings.Repeat(" ", 200)
	for name, call := range map[string]func() error{
		"patch": func() error { return d.ApplyPatch([]byte(`[{"op":"test","path":"/a","value":1}]` + long)) },
		"merge": func() error { return d.ApplyMergePatch([]byte(`{"b":2}` + long)) },
	} {
		var le *LimitError
		if err := call(); !errors.As(err, &le) || le.Limit != "patch size" || le.Max != 100 {
			t.Fatalf("%s: got %v", name, err)
		}
	}
	if string(d.Bytes()) != src {
		t.Fatal("document changed")
	}
}

// Removing the first key again and again keeps the index of the key table: each
// remove looks the key up without reading the others.
func TestRemovingFirstKeysKeepsTheKeyIndex(t *testing.T) {
	var sb strings.Builder
	const n = 3000
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "k%d: v%d\n", i, i)
	}
	d := mustParse(t, sb.String())
	remove := func(i int) {
		if err := d.Remove(fmt.Sprintf("/k%d", i)); err != nil {
			t.Fatalf("remove k%d: %v", i, err)
		}
	}
	// Warm up: the index is made when a mapping is looked in more than once.
	for i := 0; i < 5; i++ {
		remove(i)
	}
	ki := d.s.es().cache.Keys
	if ki == nil {
		t.Fatal("no key index after repeated edits")
	}
	before := ki.Compared
	for i := 5; i < 205; i++ {
		remove(i)
	}
	if got := d.s.es().cache.Keys.Compared - before; got > 200*40 {
		t.Errorf("200 removes read %d keys; the index was built again", got)
	}
	// The index still answers as the tree does.
	var x string
	if err := d.Get("/k500", &x); err != nil || x != "v500" {
		t.Fatalf("k500: %q %v", x, err)
	}
	if err := d.Get("/k100", &x); !errors.Is(err, ErrNotFound) {
		t.Fatalf("k100: %q %v", x, err)
	}
	if err := d.Remove("/k100"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove of a removed key: %v", err)
	}
	if err := d.Set("/k100", "again"); err != nil {
		t.Fatal(err)
	}
	if err := d.Get("/k100", &x); err != nil || x != "again" {
		t.Fatalf("k100 after Set: %q %v", x, err)
	}
	if err := sameAsFreshParse(d); err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := Unmarshal(d.Bytes(), &m); err != nil || len(m) != n-205+1 {
		t.Fatalf("%d keys, %v", len(m), err)
	}
}

// Node methods work on a document with no line breaks. That they do not read the
// whole line on every call is measured by BenchmarkNodeMethodsLongLine.
func TestNodeMethodsOnOneLongLine(t *testing.T) {
	const n = 40000
	src := "[" + strings.TrimSuffix(strings.Repeat("&a 0, *a, ", n), ", ") + "]"
	d := mustParse(t, src)
	count := 0
	for it := range d.Root().Items() {
		if v := it.Target().Value(); v != "0" {
			t.Fatalf("item %d: target %q", count, v)
		}
		if c := it.LineComment(); c != "" {
			t.Fatalf("item %d: comment %q", count, c)
		}
		count++
	}
	if count != 2*n {
		t.Fatalf("%d items", count)
	}
}

// A value that nests past the depth cap is refused with a *LimitError that names
// the cap, with or without a depth limit.
func TestDepthErrorsAreLimitErrors(t *testing.T) {
	deep := strings.Repeat("[", 20000) + strings.Repeat("]", 20000)
	d := mustParse(t, "a: "+deep+"\nb: 1\n", NoLimits())
	var n Node
	if err := d.Get("/a", &n); err != nil {
		t.Fatal(err)
	}
	check := func(what string, err error) {
		t.Helper()
		var le *LimitError
		if !errors.As(err, &le) || le.Limit != "depth" || le.Max <= 0 {
			t.Errorf("%s: got %T %v", what, err, err)
		}
	}
	check("Set with a deep Node", d.Set("/b", n))
	check("ApplyPatch", d.ApplyPatch([]byte(`[{"op":"add","path":"/c","value":`+deep+`}]`)))
	check("ApplyMergePatch", d.ApplyMergePatch([]byte(`{"c":`+deep+`}`)))
	if got := string(d.Bytes()); !strings.HasSuffix(got, "b: 1\n") {
		t.Errorf("document changed: %.60q", got)
	}
}

// A random patch run over a document with an empty typed scalar writes valid JSON.
func TestRandomPatchesOnEmptyTypedScalar(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		d := mustParse(t, "a: !!int\nb: [1, !!int , 3]\nc: 5\n")
		if err := randomPatches(d, rand.New(rand.NewSource(seed)), 6, false); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
}

// Lookups through a key index that survives removals agree with a plain map
// after a long run of removals, additions and replacements.
func TestKeyIndexAgreesWithModelAfterRemovals(t *testing.T) {
	var sb strings.Builder
	model := map[string]string{}
	for i := 0; i < 700; i++ {
		k := fmt.Sprintf("k%d", i)
		model[k] = fmt.Sprintf("v%d", i)
		switch i % 7 {
		case 0:
			fmt.Fprintf(&sb, "# note %d\n%s: v%d\n", i, k, i)
		case 3:
			fmt.Fprintf(&sb, "%s: v%d # tail\n", k, i)
		default:
			fmt.Fprintf(&sb, "%s: v%d\n", k, i)
		}
	}
	d := mustParse(t, sb.String())
	r := rand.New(rand.NewSource(7))
	for step := 0; step < 3000; step++ {
		k := fmt.Sprintf("k%d", r.Intn(900))
		switch r.Intn(4) {
		case 0, 1:
			// Mostly the first keys, the case the index is kept for.
			if r.Intn(2) == 0 {
				k = fmt.Sprintf("k%d", step%700)
			}
			err := d.Remove("/" + k)
			if _, ok := model[k]; ok != (err == nil) {
				t.Fatalf("step %d: remove %s: %v (in model: %v)", step, k, err, ok)
			}
			delete(model, k)
		case 2:
			v := fmt.Sprintf("n%d", step)
			if err := d.Set("/"+k, v); err != nil {
				t.Fatalf("step %d: set %s: %v", step, k, err)
			}
			model[k] = v
		default:
			var got string
			err := d.Get("/"+k, &got)
			want, ok := model[k]
			if ok != (err == nil) || got != want {
				t.Fatalf("step %d: get %s = %q, %v; model %q, %v", step, k, got, err, want, ok)
			}
		}
	}
	var got map[string]string
	if err := Unmarshal(d.Bytes(), &got); err != nil || len(got) != len(model) {
		t.Fatalf("%d keys, want %d: %v", len(got), len(model), err)
	}
	for k, v := range model {
		if got[k] != v {
			t.Fatalf("%s = %q, want %q", k, got[k], v)
		}
	}
	if err := sameAsFreshParse(d); err != nil {
		t.Fatal(err)
	}
}
