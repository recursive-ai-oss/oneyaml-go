package oneyaml

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/resolve"
)

// A copy of an alias whose anchor follows the destination is written out in
// full. The few bytes of the alias must not hide that: the text actually written
// counts against the patch-wide bound, so forty small operations cannot add up to
// a document far past it.
func TestPatchCopyOfAliasChargesWhatItWrites(t *testing.T) {
	big := strings.Repeat("v", 300<<10)
	src := "s: [0]\nd: &a " + big + "\nx: *a\n"
	var ops []string
	for i := 0; i < 40; i++ {
		ops = append(ops, `{"op":"copy","from":"/x","path":"/s/0"}`)
	}
	patch := "[" + strings.Join(ops, ",") + "]"
	for _, name := range []string{"default", "untrusted"} {
		t.Run(name, func(t *testing.T) {
			var opts []Option
			if name == "untrusted" {
				// Without the bound on the work, which would stop this patch first.
				opts = append(opts, noPatchWork(UntrustedLimits()))
			} else {
				// A small bound keeps the test fast; the rule is the same as at 64 MiB.
				opts = append(opts, Limits(LimitSet{MaxDepth: 10000, MaxAliasNodes: 1 << 20, MaxAliasBytes: 1 << 20}))
			}
			d := mustParse(t, src, opts...)
			err := d.ApplyPatch([]byte(patch))
			var le *LimitError
			if !errors.As(err, &le) || (le.Limit != LimitMaxAliasBytes && le.Limit != LimitMaxInputBytes) {
				t.Fatalf("got %v", err)
			}
			if string(d.Bytes()) != src {
				t.Fatal("document changed")
			}
			if err := sameAsFreshParse(d); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := Unmarshal(d.Bytes(), &got); err != nil || got["x"] != big {
				t.Fatalf("read back: %v", err)
			}
		})
	}
	// A single copy inside the bound still works.
	d := mustParse(t, src)
	if err := d.ApplyPatch([]byte(`[{"op":"copy","from":"/x","path":"/s/0"}]`)); err != nil {
		t.Fatal(err)
	}
	var got struct{ S []string }
	if err := Unmarshal(d.Bytes(), &got); err != nil || len(got.S) != 2 || got.S[0] != big {
		t.Fatalf("single copy: %v", err)
	}
}

// A move writes its aliases out once, and that counts too: twenty moves of an
// alias to a place before its anchor add up past the bound.
func TestPatchMoveOfAliasChargesWhatItWrites(t *testing.T) {
	big := strings.Repeat("v", 100<<10)
	var sb strings.Builder
	const n = 20
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "t%d: 0\n", i)
	}
	sb.WriteString("d: &a " + big + "\n")
	var ops []string
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "x%d: *a\n", i)
		ops = append(ops, fmt.Sprintf(`{"op":"move","from":"/x%d","path":"/t%d"}`, i, i))
	}
	src := sb.String()
	d := mustParse(t, src, Limits(LimitSet{MaxAliasBytes: 1 << 20, MaxAliasNodes: 1 << 20, MaxDepth: 10000}))
	err := d.ApplyPatch([]byte("[" + strings.Join(ops, ",") + "]"))
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != LimitMaxAliasBytes {
		t.Fatalf("got %v", err)
	}
	if string(d.Bytes()) != src {
		t.Fatal("document changed")
	}
	if err := sameAsFreshParse(d); err != nil {
		t.Fatal(err)
	}
	// Half as many moves fit.
	d = mustParse(t, src, Limits(LimitSet{MaxAliasBytes: 1 << 20, MaxAliasNodes: 1 << 20, MaxDepth: 10000}))
	if err := d.ApplyPatch([]byte("[" + strings.Join(ops[:8], ",") + "]")); err != nil {
		t.Fatal(err)
	}
}

// A merge patch is held to the input limit like a 6902 patch.
func TestMergePatchOutputIsBounded(t *testing.T) {
	src := "a: " + strings.Repeat("x", 30) + "\n"
	d := mustParse(t, src, Limits(LimitSet{MaxInputBytes: 120}))
	err := d.ApplyMergePatch([]byte(`{"b":"` + strings.Repeat("y", 100) + `"}`))
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != LimitMaxInputBytes {
		t.Fatalf("got %v", err)
	}
	if string(d.Bytes()) != src {
		t.Fatal("document changed")
	}
}

// Equality of two long integers must not convert them to big integers, which
// costs quadratic work in the digits.
func TestPatchTestComparesHugeIntegersByDigits(t *testing.T) {
	digits := strings.Repeat("7", 2_000_000)
	d := mustParse(t, "n: "+digits+"\n", NoLimits())
	ok := `[{"op":"test","path":"/n","value":` + digits + `}]`
	bad := `[{"op":"test","path":"/n","value":` + digits[:len(digits)-1] + `8}]`
	before := resolve.BigConversions()
	defer func() {
		// Comparing the digit strings converts nothing.
		if n := resolve.BigConversions() - before; n != 0 {
			t.Errorf("%d big-integer conversions", n)
		}
	}()
	if err := d.ApplyPatch([]byte(ok)); err != nil {
		t.Fatal(err)
	}
	if err := d.ApplyPatch([]byte(bad)); !errors.Is(err, ErrTestFailed) {
		t.Fatalf("got %v", err)
	}
}
