package oneyaml_test

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"oneyaml.dev/go"
)

type nest map[string]nest

// deepBadLeaves is a flow mapping nested depth levels deep whose innermost
// mapping holds leaves values that do not fit.
func deepBadLeaves(depth, keyLen, leaves int) []byte {
	key := strings.Repeat("k", keyLen)
	var sb strings.Builder
	for i := 0; i < depth; i++ {
		sb.WriteString("{" + key + ": ")
	}
	sb.WriteString("{")
	for i := 0; i < leaves; i++ {
		fmt.Fprintf(&sb, "e%d: x, ", i)
	}
	sb.WriteString("}")
	sb.WriteString(strings.Repeat("}", depth))
	return []byte(sb.String())
}

// Many mismatches at the bottom of a deep document must cost time and memory in
// proportion to what is reported, not to the depth times the count.
func TestDeepAggregateIsLinear(t *testing.T) {
	for _, c := range []struct{ depth, leaves int }{{990, 99}, {990, 1}, {9990, 99}, {9990, 1}} {
		if testing.Short() && c.depth > 990 {
			continue
		}
		in := deepBadLeaves(c.depth, 40, c.leaves)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		var v nest
		err := oneyaml.Unmarshal(in, &v)
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		var te *oneyaml.TypeError
		if !errors.As(err, &te) {
			t.Fatalf("depth %d: got %v", c.depth, err != nil)
		}
		// The paths of the errors after the first share a budget of the larger of
		// the input size and 1 MiB; whatever fits is reported, with whole paths.
		perPath := c.depth * 42
		wantN := min(c.leaves, 1+max(len(in), 1<<20)/perPath)
		if n := len(te.All()); n < wantN-1 || n > wantN+1 || n < 1 {
			t.Fatalf("depth %d: got %d errors, want about %d", c.depth, n, wantN)
		}
		for _, e := range te.All() {
			if len(e.Path) < c.depth*41 {
				t.Fatalf("depth %d: path of %d bytes is not whole", c.depth, len(e.Path))
			}
		}
		// Every error carries its full path (about 40 bytes a level), twice with its
		// message; allow a generous constant over that.
		paths := len(te.All()) * c.depth * 45
		if alloc := int(after.TotalAlloc - before.TotalAlloc); alloc > 12*paths+8*len(in)+(4<<20) {
			t.Errorf("depth %d, %d errors: allocated %d MB for %d MB of paths", c.depth, c.leaves, alloc>>20, paths>>20)
		}
		if n := len(err.Error()); n > 200<<10 {
			t.Errorf("depth %d, %d errors: message of %d KB", c.depth, c.leaves, n>>10)
		}
		if elapsed > 5*time.Second {
			t.Errorf("depth %d, %d errors: %v", c.depth, c.leaves, elapsed)
		}
	}
}

func TestDeepPathIsComplete(t *testing.T) {
	var v nest
	err := oneyaml.Unmarshal([]byte("{a: {b: {c: x, d: y}}}"), &v)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) || len(te.All()) != 2 || te.All()[0].Path != "/a/b/c" || te.All()[1].Path != "/a/b/d" {
		t.Fatalf("got %v", err)
	}
}

func TestMismatchLeavesNilPointersNil(t *testing.T) {
	var a struct {
		P *int
		N string
	}
	err := oneyaml.Unmarshal([]byte("p: x\nn: a\n"), &a)
	if err == nil || a.P != nil || a.N != "a" {
		t.Errorf("pointer field: %v %+v", err, a)
	}

	var b []*int
	err = oneyaml.Unmarshal([]byte("[x, 2]"), &b)
	if err == nil || len(b) != 2 || b[0] != nil || b[1] == nil || *b[1] != 2 {
		t.Errorf("slice of pointers: %v %v", err, b)
	}

	var c struct {
		S *[]int
		M *map[string]int
		T *struct{ A int }
		N string
	}
	err = oneyaml.Unmarshal([]byte("s: {a: 1}\nm: [1]\nt: [1]\nn: a\n"), &c)
	if err == nil || c.S != nil || c.M != nil || c.T != nil || c.N != "a" {
		t.Errorf("pointers to collections: %v %+v", err, c)
	}

	// A pointer whose target was partly filled keeps what was read.
	var d struct{ T *struct{ A, B int } }
	err = oneyaml.Unmarshal([]byte("t: {a: x, b: 2}"), &d)
	if err == nil || d.T == nil || d.T.B != 2 {
		t.Errorf("partly filled: %v %+v", err, d.T)
	}
}

func TestDuplicateAfterFailedEntryIsReported(t *testing.T) {
	for _, c := range []struct {
		name, in string
		target   any
	}{
		{"map", "a: x\na: 1\n", new(map[string]int)},
		{"map of int keys", "1: x\n0x1: 1\n", new(map[int]int)},
		{"struct", "a: x\na: 1\n", new(struct{ A int })},
		{"inline map", "a: x\na: 1\n", new(struct {
			N    int
			Rest map[string]int `yaml:",inline"`
		})},
		{"nested", "o:\n  a: x\n  b: 1\n  a: 1\n", new(map[string]map[string]int)},
	} {
		err := oneyaml.Unmarshal([]byte(c.in), c.target)
		var se *oneyaml.SyntaxError
		if !errors.As(err, &se) || se.Category != oneyaml.CategoryDuplicateKey {
			t.Errorf("%s: got %v", c.name, err)
		}
	}
}

func TestCollectedErrorsStopAtTheBound(t *testing.T) {
	row := "[" + strings.Repeat("x, ", 99) + "]"
	for name, in := range map[string]string{
		"two rows":         "- " + row + "\n- " + row + "\n",
		"row then scalars": "- " + row + "\n- [x]\n- [x]\n",
		"flat":             "[" + strings.Repeat("x, ", 500) + "]",
	} {
		var target any = new([][]int)
		if name == "flat" {
			target = new([]int)
		}
		for _, decode := range []func([]byte) error{
			func(b []byte) error { return oneyaml.Unmarshal(b, target) },
			func(b []byte) error {
				d, err := oneyaml.Parse(b)
				if err != nil {
					return err
				}
				return d.Decode(target)
			},
		} {
			var te *oneyaml.TypeError
			err := decode([]byte(in))
			if !errors.As(err, &te) || len(te.All()) != 100 {
				t.Errorf("%s: %d errors, want 100 (%v)", name, len(te.All()), err != nil)
			}
		}
	}
}

func TestFlowOpenerIsTheInnermostOpen(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a: [[x], y\nz", "flow sequence opened at line 1, column 4"},
		{"a: {b: [1], c: 2\nz", "flow mapping opened at line 1, column 4"},
		{"a: [{b: 1}, c\nz", "flow sequence opened at line 1, column 4"},
		{"a: [[x]\nz", "flow sequence opened at line 1, column 4"},
	} {
		var v any
		err := oneyaml.Unmarshal([]byte(c.in), &v)
		var se *oneyaml.SyntaxError
		if !errors.As(err, &se) || !strings.Contains(se.Msg, c.want) {
			t.Errorf("%q: got %v, want %q", c.in, err, c.want)
		}
	}
}

func TestFirstDuplicateIsInTheSameMapping(t *testing.T) {
	in := "x: {a: 1}\ny:\n  b: 1\n  a: 2\n  a: 3\n"
	var v any
	err := oneyaml.Unmarshal([]byte(in), &v)
	var se *oneyaml.SyntaxError
	if !errors.As(err, &se) || se.Line != 5 || se.FirstLine != 4 || se.FirstColumn != 3 {
		t.Fatalf("got %v", err)
	}
}

func TestFloatKeyComparisonIsExact(t *testing.T) {
	dup := func(in string, target any) {
		t.Helper()
		err := oneyaml.Unmarshal([]byte(in), target)
		var se *oneyaml.SyntaxError
		if !errors.As(err, &se) || se.Category != oneyaml.CategoryDuplicateKey {
			t.Errorf("%q into %T: got %v, want a duplicate", in, target, err)
		}
	}
	round := func(in string, target any) {
		t.Helper()
		err := oneyaml.Unmarshal([]byte(in), target)
		var te *oneyaml.TypeError
		var se *oneyaml.SyntaxError
		if errors.As(err, &se) || !errors.As(err, &te) || !strings.Contains(te.Msg, "round to the same float64") {
			t.Errorf("%q into %T: got %v, want the float64 message", in, target, err)
		}
	}
	for _, target := range []func() any{func() any { return new(any) }, func() any { return new(map[float64]string) }} {
		round("1e-400: a\n2e-400: b\n", target())
		round("1e400: a\n.inf: b\n", target())
		round("1e99999999999999999999: a\n2e99999999999999999999: b\n", target())
		dup("1e400: a\n10e399: b\n", target())
		dup("1e99999999999999999999: a\n10e99999999999999999998: b\n", target())
		dup(".inf: a\n.Inf: b\n", target())
	}
	msg := func(in string) string {
		var v map[float64]string
		return fmt.Sprint(oneyaml.Unmarshal([]byte(in), &v))
	}
	var a any
	mAny := fmt.Sprint(oneyaml.Unmarshal([]byte("1e-400: a\n2e-400: b\n"), &a))
	if msg("1e-400: a\n2e-400: b\n") != mAny {
		t.Errorf("typed and generic messages differ:\n%s\n%s", msg("1e-400: a\n2e-400: b\n"), mAny)
	}
}

// aliasKeyDoc nests depth flow mappings whose keys are one anchored key of keyLen
// bytes used through an alias, with 100 bad values at the bottom. The input is
// small and the path of every error is depth*keyLen bytes.
func aliasKeyDoc(depth, keyLen int) []byte {
	var sb strings.Builder
	sb.WriteString("{&k " + strings.Repeat("/", keyLen) + ": ")
	for i := 1; i < depth; i++ {
		sb.WriteString("{*k : ")
	}
	sb.WriteString("{")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&sb, "e%d: x, ", i)
	}
	sb.WriteString("}" + strings.Repeat("}", depth))
	return []byte(sb.String())
}

// The paths of the errors of one read are bounded by the size of the input, but
// the first error is always whole.
func TestCollectedPathsStayWithinABudget(t *testing.T) {
	for _, c := range []struct {
		name       string
		in         []byte
		opts       []oneyaml.Option
		maxAllocMB uint64
	}{
		{"alias key, untrusted", aliasKeyDoc(999, 8000), []oneyaml.Option{oneyaml.Untrusted()}, 120},
		{"alias key, defaults", aliasKeyDoc(2000, 1600), nil, 120},
	} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		var v nest
		err := oneyaml.Unmarshal(c.in, &v, c.opts...)
		runtime.ReadMemStats(&after)
		var te *oneyaml.TypeError
		if !errors.As(err, &te) {
			t.Fatalf("%s: got %v", c.name, err)
		}
		all := te.All()
		if len(all) == 0 || !strings.HasSuffix(all[0].Path, "/e0") {
			t.Fatalf("%s: the first error is not whole: %d errors", c.name, len(all))
		}
		total := 0
		for _, e := range all[1:] {
			total += len(e.Path)
		}
		if budget := max(len(c.in), 1<<20); total > budget {
			t.Errorf("%s: later errors hold %d bytes of path, budget %d", c.name, total, budget)
		}
		if mb := (after.TotalAlloc - before.TotalAlloc) >> 20; mb > c.maxAllocMB {
			t.Errorf("%s: allocated %d MB, want at most %d", c.name, mb, c.maxAllocMB)
		}
	}
}

func TestLongPathWithoutAliasesIsBounded(t *testing.T) {
	key := strings.Repeat("k", 1_000_000)
	var sb strings.Builder
	sb.WriteString("{" + key + ": {")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&sb, "e%d: x, ", i)
	}
	sb.WriteString("}}")
	var v nest
	err := oneyaml.Unmarshal([]byte(sb.String()), &v)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) {
		t.Fatal(err)
	}
	total := 0
	for _, e := range te.All() {
		total += len(e.Path)
	}
	if total > sb.Len()+(1<<20) {
		t.Errorf("%d bytes of path for %d bytes of input", total, sb.Len())
	}
}

func TestRepeatOfAnEarlierGoodKeyIsADuplicate(t *testing.T) {
	var m map[string]int
	err := oneyaml.Unmarshal([]byte("a: 1\na: x\n"), &m)
	var se *oneyaml.SyntaxError
	if !errors.As(err, &se) || se.Category != oneyaml.CategoryDuplicateKey || se.Line != 2 {
		t.Errorf("got %v", err)
	}
}

func TestExponentLeadingZerosDoNotCount(t *testing.T) {
	var v any
	in := "1e5: a\n? 1e" + strings.Repeat("0", 5000) + "5\n: b\n"
	err := oneyaml.Unmarshal([]byte(in), &v)
	var se *oneyaml.SyntaxError
	if !errors.As(err, &se) || se.Category != oneyaml.CategoryDuplicateKey {
		t.Errorf("got %v", err)
	}
}
