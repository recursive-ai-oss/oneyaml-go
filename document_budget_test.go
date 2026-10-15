package oneyaml

import (
	"math"
	"testing"
)

// The bound on a patch's work never wraps: an overflowing bound is no bound.
func TestWorkBoundDoesNotWrap(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		ratio, src, patch     int
		wantZero, wantAtLeast bool
	}{
		{"off", 0, 1 << 20, 100, true, false},
		{"default", 256, 880, 100, false, true},
		{"huge ratio", math.MaxInt, 880, 100, false, false},
		{"huge ratio, empty", math.MaxInt, 0, 0, false, true},
		{"large document", 256, 10 << 20, 100, false, true}, // 2.6 GB: wraps in 32 bits
		{"huge document", 32, math.MaxInt / 2, math.MaxInt / 2, false, false},
	} {
		got := workBound(tc.ratio, tc.src, tc.patch)
		if got < 0 {
			t.Errorf("%s: bound %d is negative", tc.name, got)
		}
		if tc.wantZero && got != 0 {
			t.Errorf("%s: bound %d, want none", tc.name, got)
		}
		if !tc.wantZero && got == 0 {
			t.Errorf("%s: no bound where one is set", tc.name)
		}
		if tc.wantAtLeast && got < 16<<20 {
			t.Errorf("%s: bound %d is below the floor", tc.name, got)
		}
	}
	if got := workBound(256, 880, 100); got != 256*980+16<<20 {
		t.Errorf("bound %d", got)
	}
}

func TestSaturatingArithmetic(t *testing.T) {
	if addSat(math.MaxInt, 1) != math.MaxInt || addSat(3, 4) != 7 {
		t.Error("addSat")
	}
	if mulSat(math.MaxInt/2, 8) != math.MaxInt || mulSat(3, 8) != 24 || mulSat(0, math.MaxInt) != 0 {
		t.Error("mulSat")
	}
}

// A huge ratio gives a bound that a small patch never reaches.
func TestHugePatchWorkRatioRefusesNothing(t *testing.T) {
	d := mustParse(t, "a: 1\nb: 2\n", Limits(LimitSet{MaxPatchWorkRatio: math.MaxInt}))
	if err := d.ApplyPatch([]byte(`[{"op":"replace","path":"/a","value":3},{"op":"replace","path":"/b","value":4}]`)); err != nil {
		t.Fatalf("patch: %v", err)
	}
	if err := d.ApplyMergePatch([]byte(`{"a":5,"b":6}`)); err != nil {
		t.Fatalf("merge patch: %v", err)
	}
}

// The cost stops at the largest int where plain arithmetic would wrap. These
// counters wrap without saturation: the sum of the bytes and eight times the
// nodes is a small negative number then, which no bound would catch.
func TestPatchWorkCostSaturates(t *testing.T) {
	d := mustParse(t, "a: 1\nb: 2\n")
	w := d.s.newPatchWork(100)
	wk := &w.s.es().work
	wk.bytes = math.MaxInt - 5
	wk.nodes = math.MaxInt/8 + 1
	if c := w.cost(); c != math.MaxInt {
		t.Fatalf("cost %d, want %d", c, math.MaxInt)
	}
	wk.bytes, wk.nodes, wk.shifted = 0, 0, math.MaxInt
	wk.nodes = math.MaxInt / 4 // times eight: wraps
	if c := w.cost(); c != math.MaxInt {
		t.Fatalf("cost %d, want %d", c, math.MaxInt)
	}
}

// With the bound held at the largest int, a cost that saturates still reaches it.
func TestSaturatedCostReachesAClampedBound(t *testing.T) {
	d := mustParse(t, "a: 1\n", Limits(LimitSet{MaxPatchWorkRatio: math.MaxInt}))
	w := d.s.newPatchWork(10)
	if w.max != math.MaxInt {
		t.Fatalf("bound %d, want %d", w.max, math.MaxInt)
	}
	w.s.es().work.bytes = math.MaxInt
	if err := w.check("/a"); err == nil {
		t.Fatal("a saturated cost passed a clamped bound")
	}
}

// The arithmetic of a 32-bit int, on any host: a 10 MiB document at the default
// ratio has a bound of 2.6 GB, which does not fit and is held at the largest int,
// not wrapped negative and not dropped.
func TestWorkBoundIn32Bits(t *testing.T) {
	const max32 = math.MaxInt32
	for _, tc := range []struct {
		ratio, src, patch int
		want              uint64
	}{
		{256, 10 << 20, 100, math.MaxInt32},
		{32, 10 << 20, 100, 32*(10<<20+100) + 16<<20},
		{256, 1 << 20, 100, 256*(1<<20+100) + 16<<20},
		{1 << 30, 880, 100, math.MaxInt32},
		{math.MaxInt32, 880, 100, math.MaxInt32},
		{0, 880, 100, 0},
		{-1, 880, 100, 0},
		{256, 880, 100, 256*980 + 16<<20},
	} {
		if got := workBoundIn(max32, tc.ratio, tc.src, tc.patch); got != tc.want {
			t.Errorf("ratio %d, %d+%d bytes: bound %d, want %d", tc.ratio, tc.src, tc.patch, got, tc.want)
		}
	}
}
