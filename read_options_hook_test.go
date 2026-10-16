package oneyaml

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/encoding"
)

type optInner struct{ A int }

// optFuncHook reads through the function form of UnmarshalYAML.
type optFuncHook struct{ in optInner }

func (h *optFuncHook) UnmarshalYAML(unmarshal func(any) error) error { return unmarshal(&h.in) }

// optNodeHook reads through the Node it is given.
type optNodeHook struct{ in optInner }

func (h *optNodeHook) UnmarshalYAML(n Node) error { return n.Decode(&h.in) }

type optAnyHook struct{ v any }

func (h *optAnyHook) UnmarshalYAML(unmarshal func(any) error) error { return unmarshal(&h.v) }

func TestPerReadOptionsReachAHooksNestedDecode(t *testing.T) {
	src := []byte("x: {a: 1, extra: 2}\n")
	run := func(t *testing.T, mk func() any, leaf func() any) {
		t.Helper()
		d, err := Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Decode(mk()); err != nil {
			t.Fatalf("without the option: %v", err)
		}
		if err := d.Decode(mk(), KnownFields()); !errors.Is(err, ErrUnknownField) {
			t.Errorf("Decode(KnownFields) = %v, want an unknown field", err)
		}
		if err := d.Get("/x", leaf(), KnownFields()); !errors.Is(err, ErrUnknownField) {
			t.Errorf("Get(KnownFields) = %v, want an unknown field", err)
		}
		var n Node
		if err := d.Get("", &n); err != nil {
			t.Fatal(err)
		}
		if err := n.Decode(mk(), KnownFields()); !errors.Is(err, ErrUnknownField) {
			t.Errorf("Node.Decode(KnownFields) = %v, want an unknown field", err)
		}
		// And as the Document was parsed.
		ds, _ := Parse(src, KnownFields())
		if err := ds.Decode(mk()); !errors.Is(err, ErrUnknownField) {
			t.Errorf("Parse(KnownFields).Decode = %v, want an unknown field", err)
		}
		// The option is for that call only.
		if err := d.Decode(mk()); err != nil {
			t.Errorf("a later read without the option: %v", err)
		}
	}
	t.Run("func hook", func(t *testing.T) {
		run(t, func() any { return &struct{ X optFuncHook }{} }, func() any { return new(optFuncHook) })
	})
	t.Run("node hook", func(t *testing.T) {
		run(t, func() any { return &struct{ X optNodeHook }{} }, func() any { return new(optNodeHook) })
	})
}

func TestPerReadLimitsReachAHooksNestedDecode(t *testing.T) {
	var b strings.Builder
	b.WriteString("n0: &n0 [a, a, a, a, a, a, a, a]\n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&b, "n%d: &n%d [*n%d, *n%d, *n%d, *n%d, *n%d, *n%d, *n%d, *n%d]\n", i, i, i-1, i-1, i-1, i-1, i-1, i-1, i-1, i-1)
	}
	b.WriteString("x: *n5\n")
	bomb := []byte(b.String())
	d, err := Parse(bomb)
	if err != nil {
		t.Fatal(err)
	}
	var plain struct{ X any }
	var le *LimitError
	if err := d.Decode(&plain, Untrusted()); !errors.As(err, &le) {
		t.Fatalf("without a hook: %v, want a limit error", err)
	}
	var viaHook struct{ X optAnyHook }
	le = nil
	if err := d.Decode(&viaHook, Untrusted()); !errors.As(err, &le) {
		t.Errorf("through a hook: %v, want the same limit error", err)
	}
	// The option does not outlive the call.
	if err := d.Decode(&viaHook); err != nil {
		t.Errorf("a later read with the Document's own bounds: %v", err)
	}
}

func utf32le(s string) []byte { return encoding.FromUTF8(nil, []byte(s), encoding.UTF32LE, true) }
func utf16le(s string) []byte { return encoding.FromUTF8(nil, []byte(s), encoding.UTF16LE, true) }

func TestEditSizeIsMeasuredInTheWrittenEncoding(t *testing.T) {
	// 24 bytes as UTF-32 with a mark; 50 more characters would be 200 more.
	src := utf32le("a: 1\n")
	if len(src) != 24 {
		t.Fatal(len(src))
	}
	d, err := Parse(src, Limits(LimitSet{MaxInputBytes: 64}))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Set("/a", strings.Repeat("z", 50)); !errors.Is(err, ErrMaxInputBytes) {
		t.Fatalf("Set of 50 characters into a 64 byte limit = %v (document now %d bytes)", err, len(d.Bytes()))
	}
	if got := d.Bytes(); string(got) != string(src) {
		t.Errorf("a refused edit changed the document")
	}
	if err := d.Set("/a", "zz"); err != nil {
		t.Errorf("a small edit: %v", err)
	}
	if n := len(d.Bytes()); n > 64 {
		t.Errorf("document is %d bytes after the edit", n)
	}

	// UTF-16, three-byte characters: 3 bytes in UTF-8, 2 written.
	cjk := strings.Repeat("日", 20)
	src16 := utf16le("a: " + cjk + "\nb: 1\n") // 2 + 2*(3+20+1+5) = 60 bytes
	if len(src16) != 60 {
		t.Fatal(len(src16))
	}
	d16, err := Parse(src16, Limits(LimitSet{MaxInputBytes: 66}))
	if err != nil {
		t.Fatal(err)
	}
	if err := d16.Set("/b", "xy"); err != nil {
		t.Errorf("an edit that writes 62 bytes into a 66 byte limit: %v", err)
	}
	if err := d16.Set("/c", "abcdefgh"); !errors.Is(err, ErrMaxInputBytes) {
		t.Errorf("an edit that writes past 66 bytes = %v", err)
	}
}

func TestPatchSizeIsMeasuredInTheWrittenEncoding(t *testing.T) {
	src := utf32le("a: 1\n")
	parse := func() *Document {
		d, err := Parse(src, Limits(LimitSet{MaxInputBytes: 64}))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	big := strings.Repeat("z", 25) // UTF-8: 34 bytes; UTF-32: 116
	if err := parse().ApplyPatch([]byte(`[{"op":"add","path":"/b","value":"` + big + `"}]`)); !errors.Is(err, ErrMaxInputBytes) {
		t.Errorf("ApplyPatch past the limit in UTF-32 = %v", err)
	}
	if err := parse().ApplyMergePatch([]byte(`{"b":"` + big + `"}`)); !errors.Is(err, ErrMaxInputBytes) {
		t.Errorf("ApplyMergePatch past the limit in UTF-32 = %v", err)
	}
	d := parse()
	if err := d.ApplyPatch([]byte(`[{"op":"add","path":"/b","value":"z"}]`)); err != nil {
		t.Errorf("a patch inside the limit: %v", err)
	}
}

func TestEditSizeHasAFixedCeiling(t *testing.T) {
	// Node offsets are 32-bit, so the ceiling holds even with no input limit.
	if err := fixedSizeErr(math.MaxInt32-10, 20); !errors.Is(err, ErrMaxInputBytes) {
		t.Errorf("growing past the ceiling = %v", err)
	}
	var le *LimitError
	if err := fixedSizeErr(math.MaxInt32-10, 20); !errors.As(err, &le) || le.Option != "" {
		t.Errorf("the ceiling advises an option: %+v", err)
	}
	if err := fixedSizeErr(math.MaxInt32-10, 9); err != nil {
		t.Errorf("growing to the ceiling = %v", err)
	}
	if err := fixedSizeErr(math.MaxInt32-1, -5); err != nil {
		t.Errorf("shrinking = %v", err)
	}
}

// deepHook wraps a value read through Node.Decode, one level in each hook.
type deepHookM map[string]deepHookV
type deepHookV struct {
	S deepHookM
	L []int
}

func (h *deepHookV) UnmarshalYAML(n Node) error {
	var tmp struct {
		S deepHookM `yaml:"s"`
		L []int     `yaml:"l"`
	}
	err := n.Decode(&tmp)
	h.S, h.L = tmp.S, tmp.L
	return err
}

func TestDeepHookChainsReportEveryMismatch(t *testing.T) {
	const depth, klen, nbad = 50, 10, 100
	key := strings.Repeat("k", klen)
	var b strings.Builder
	for i := 0; i < depth; i++ {
		b.WriteString("{" + key + strconv.Itoa(i) + ": {s: ")
	}
	b.WriteString("{z: {l: [" + strings.TrimSuffix(strings.Repeat("x, ", nbad), ", ") + "]}}")
	for i := 0; i < depth; i++ {
		b.WriteString("}}")
	}
	src := []byte(b.String() + "\n")
	var h deepHookM
	err := Unmarshal(src, &h)
	var te *TypeError
	if !errors.As(err, &te) {
		t.Fatalf("got %T %v", err, err)
	}
	if got := len(te.All()); got != nbad {
		t.Errorf("%d mismatches reported, want %d", got, nbad)
	}
}

func TestKeptNodeErrorCarriesNoHookMark(t *testing.T) {
	var kept Node
	hookKeep := &nodeKeeper{out: &kept}
	var v struct{ X *nodeKeeper }
	v.X = hookKeep
	d, err := Parse([]byte("x: {a: b}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	var bad struct{ A int }
	err = kept.Decode(&bad)
	var te *TypeError
	if !errors.As(err, &te) {
		t.Fatalf("got %v", err)
	}
	if te.sc != nil || te.from != 0 {
		t.Errorf("an error from a kept Node carries the closed hook scope")
	}
}

type nodeKeeper struct{ out *Node }

func (k *nodeKeeper) UnmarshalYAML(n Node) error { *k.out = n; return nil }

func TestLimitErrorMessageShortensItsPath(t *testing.T) {
	e := limitErr(LimitMaxDepth, "depth", 10, "/"+strings.Repeat("k", 200_000))
	if n := len(e.Error()); n > 1200 {
		t.Errorf("message is %d bytes", n)
	}
	if len(e.Path) != 200_001 {
		t.Errorf("Path was shortened")
	}
}
