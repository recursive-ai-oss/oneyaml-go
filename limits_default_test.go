package oneyaml

import (
	"errors"
	"strings"
	"testing"
)

func deepFlow(n int) []byte {
	return []byte(strings.Repeat("[", n) + strings.Repeat("]", n))
}

func isLimit(err error, which Limit) bool {
	var le *LimitError
	return errors.As(err, &le) && le.Limit == which
}

// Setting one bound adds it; the bounds left at zero keep their defaults.
func TestLimitSetZeroFieldsKeepDefaults(t *testing.T) {
	deep := deepFlow(20000)
	var v any
	if err := Unmarshal(deep, &v, Limits(LimitSet{MaxInputBytes: 1 << 20})); !isLimit(err, LimitMaxDepth) {
		t.Fatalf("input bound only: got %v", err)
	}
	if err := Unmarshal(deep, &v, Limits(LimitSet{MaxAliasNodes: 20})); !isLimit(err, LimitMaxDepth) {
		t.Fatalf("alias bound only: got %v", err)
	}
	if _, err := Parse(deep, Limits(LimitSet{MaxAliasNodes: 20})); !isLimit(err, LimitMaxDepth) {
		t.Fatalf("Parse: got %v", err)
	}
	if err := Unmarshal(deep, &v, Limits(LimitSet{})); !isLimit(err, LimitMaxDepth) {
		t.Fatalf("empty set: got %v", err)
	}
	// The number-digit default holds too.
	digits := "a: " + strings.Repeat("9", 200000) + "\n"
	var n any
	if err := Unmarshal([]byte(digits), &n, Limits(LimitSet{MaxDepth: 50})); !isLimit(err, LimitMaxNumberDigits) {
		t.Fatalf("digits: got %v", err)
	}
}

// NoBound lifts one field and nothing else; NoLimits lifts all.
func TestNoBoundLiftsOneField(t *testing.T) {
	deep := deepFlow(20000)
	var v any
	if err := Unmarshal(deep, &v, Limits(LimitSet{MaxDepth: NoBound})); err != nil {
		t.Fatalf("NoBound depth: %v", err)
	}
	digits := "a: " + strings.Repeat("9", 200000) + "\n"
	if err := Unmarshal([]byte(digits), &v, Limits(LimitSet{MaxDepth: NoBound})); !isLimit(err, LimitMaxNumberDigits) {
		t.Fatalf("other bound lost: %v", err)
	}
	if err := Unmarshal(deep, &v, Limits(LimitSet{MaxNumberDigits: NoBound})); !isLimit(err, LimitMaxDepth) {
		t.Fatalf("depth lost: %v", err)
	}
	if err := Unmarshal(deep, &v, NoLimits()); err != nil {
		t.Fatalf("NoLimits: %v", err)
	}
	if _, err := Parse(deep, Limits(LimitSet{MaxDepth: NoBound})); err != nil {
		t.Fatalf("Parse NoBound: %v", err)
	}
}

// A defaulted alias bound still grows with the document; one you set is exact.
func TestAliasBoundDefaultedOrExact(t *testing.T) {
	var b strings.Builder
	b.WriteString("a: &a [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 0; i < 30; i++ {
		b.WriteString("k" + string(rune('A'+i%26)) + string(rune('a'+i/26)) + ": *a\n")
	}
	var v any
	// 330 alias nodes: under the default, and under a depth-only set, which must
	// keep the default alias bounds.
	if err := Unmarshal([]byte(b.String()), &v, Limits(LimitSet{MaxDepth: 50})); err != nil {
		t.Fatal(err)
	}
	if err := Unmarshal([]byte(b.String()), &v, Limits(LimitSet{MaxAliasNodes: 20})); !isLimit(err, LimitMaxAliasNodes) {
		t.Fatalf("exact bound: got %v", err)
	}
}

func TestDefaultLimitsUnchanged(t *testing.T) {
	want := LimitSet{MaxDepth: 10000, MaxAliasNodes: 1_000_000, MaxAliasBytes: 64 << 20, MaxNumberDigits: 100_000, MaxPatchWorkRatio: 256}
	if got := DefaultLimits(); got != want {
		t.Fatalf("got %+v", got)
	}
}

func TestEditErrorText(t *testing.T) {
	d := mustParse(t, "spec:\n  a: 1\n")
	for _, c := range []struct {
		got  error
		want string
	}{
		{d.Remove("/nope"), `oneyaml: remove "/nope": path not found`},
		{d.ApplyPatch([]byte(`[{"op":"remove","path":"/nope"}]`)), `oneyaml: patch operation 0 (remove "/nope"): path not found`},
		{d.ApplyPatch([]byte(`[{"op":"remove","path":"replicas"}]`)), `oneyaml: patch operation 0 (remove "replicas"): invalid patch: invalid JSON pointer`},
		{d.ApplyPatch([]byte(`--- x`)), `oneyaml: invalid patch: JSON at offset 0: unexpected character '-'`},
		{&EditError{Op: "decode", Kind: ErrNotFound}, `oneyaml: decode: path not found`},
	} {
		if c.got == nil || c.got.Error() != c.want {
			t.Errorf("got  %v\nwant %s", c.got, c.want)
		}
	}
	if err := d.Remove("/nope"); !errors.Is(err, ErrNotFound) {
		t.Error("errors.Is lost")
	}
	if ErrNotFound.Error() != "oneyaml: path not found" {
		t.Error("sentinel text changed")
	}
}

// A later Limits option adds to the options before it instead of replacing them.
func TestLimitsLayerOnPresets(t *testing.T) {
	digits := []byte("a: " + strings.Repeat("9", 5000) + "\n") // over the untrusted 4,300, under the default
	var v any

	if err := Unmarshal(deepFlow(60), &v, Untrusted(), Limits(LimitSet{MaxDepth: 50})); !isLimit(err, LimitMaxDepth) {
		t.Fatalf("depth tightened: %v", err)
	}
	if err := Unmarshal(digits, &v, Untrusted(), Limits(LimitSet{MaxDepth: 50})); !isLimit(err, LimitMaxNumberDigits) {
		t.Fatalf("untrusted digits lost: %v", err)
	}
	if err := Unmarshal(deepFlow(2000), &v, Untrusted(), Limits(LimitSet{MaxDepth: NoBound})); err != nil {
		t.Fatalf("NoBound lifts one field: %v", err)
	}
	if err := Unmarshal(digits, &v, Untrusted(), Limits(LimitSet{MaxDepth: NoBound})); !isLimit(err, LimitMaxNumberDigits) {
		t.Fatalf("NoBound lifted another field: %v", err)
	}
	if err := Unmarshal(deepFlow(1500), &v, Untrusted(), Limits(LimitSet{})); !isLimit(err, LimitMaxDepth) {
		t.Fatalf("empty Limits changed the preset: %v", err)
	}

	// After NoLimits, Limits adds only what it names.
	if err := Unmarshal(deepFlow(60), &v, NoLimits(), Limits(LimitSet{MaxDepth: 50})); !isLimit(err, LimitMaxDepth) {
		t.Fatalf("added depth: %v", err)
	}
	if err := Unmarshal(deepFlow(20000), &v, NoLimits(), Limits(LimitSet{MaxInputBytes: 1 << 20})); err != nil {
		t.Fatalf("NoLimits then input bound refused depth: %v", err)
	}
	big := []byte("a: " + strings.Repeat("9", 200000) + "\n")
	if err := Unmarshal(big, &v, NoLimits(), Limits(LimitSet{MaxDepth: 50})); err != nil {
		t.Fatalf("NoLimits then depth bound refused digits: %v", err)
	}
	if err := Unmarshal(big, &v, NoLimits(), Limits(LimitSet{MaxInputBytes: 100})); err == nil {
		t.Fatal("input bound not applied")
	}
}

// An alias bound inherited from a preset keeps the preset's ratio; one named is exact.
func TestLimitsLayerAliasRatio(t *testing.T) {
	c := applyOptions([]Option{Untrusted(), Limits(LimitSet{MaxDepth: 50})})
	if c.aliasRatio != 2 || c.exactNodes || c.exactBytes || c.limits.MaxAliasNodes != 100_000 {
		t.Fatalf("depth-only layer: %+v", c)
	}
	c = applyOptions([]Option{Untrusted(), Limits(LimitSet{MaxAliasNodes: 20})})
	if c.aliasRatio != 2 || !c.exactNodes || c.exactBytes || c.limits.MaxAliasBytes != 8<<20 {
		t.Fatalf("alias-nodes layer: %+v", c)
	}
	c = applyOptions([]Option{Limits(LimitSet{MaxDepth: 5})})
	if c.aliasRatio != 10 || c.exactNodes || c.limits.MaxAliasNodes != 1_000_000 {
		t.Fatalf("default layer: %+v", c)
	}
}
