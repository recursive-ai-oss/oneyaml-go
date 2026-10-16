package oneyaml

import (
	"errors"
	"strings"
	"testing"
)

// The text of a patch may use an eighth of MaxInputBytes: a patch is read into a
// list of operations that costs many times its text, so under Untrusted a patch
// as long as the largest document is refused before it is read.
func TestUntrustedPatchTextShare(t *testing.T) {
	limit := UntrustedLimits().MaxInputBytes
	share := patchTextMax(limit)
	pad := strings.Repeat(" ", share+1)
	for name, call := range map[string]func(d *Document) error{
		"patch":       func(d *Document) error { return d.ApplyPatch([]byte(`[{"op":"add","path":"/b","value":1}]` + pad)) },
		"merge patch": func(d *Document) error { return d.ApplyMergePatch([]byte(`{"b":1}` + pad)) },
		"yaml patch":  func(d *Document) error { return d.ApplyPatch([]byte("- {op: add, path: /b, value: 1}\n" + pad)) },
		"yaml merge":  func(d *Document) error { return d.ApplyMergePatch([]byte("b: 1\n" + pad)) },
	} {
		d := mustParse(t, "a: 1\n", Untrusted())
		err := call(d)
		var le *LimitError
		if !errors.As(err, &le) || le.Limit != "patch size" || le.Max != share {
			t.Errorf("%s: got %v", name, err)
		}
		if string(d.Bytes()) != "a: 1\n" {
			t.Errorf("%s changed the document: %q", name, d.Bytes())
		}
	}
}

// A patch within its share still applies under Untrusted, and under the default
// limits, which set no input bound, a patch of any size is not refused for it.
func TestPatchTextWithinShareApplies(t *testing.T) {
	pad := strings.Repeat(" ", patchTextMax(UntrustedLimits().MaxInputBytes)/2)
	d := mustParse(t, "a: 1\n", Untrusted())
	if err := d.ApplyPatch([]byte(`[{"op":"add","path":"/b","value":1}]` + pad)); err != nil {
		t.Fatal(err)
	}
	if err := d.ApplyMergePatch([]byte(`{"c":2}` + pad)); err != nil {
		t.Fatal(err)
	}
	if string(d.Bytes()) != "a: 1\nb: 1\nc: 2\n" {
		t.Fatalf("got %q", d.Bytes())
	}
	d = mustParse(t, "a: 1\n")
	big := strings.Repeat(" ", 10<<20)
	if err := d.ApplyPatch([]byte(`[{"op":"add","path":"/b","value":1}]` + big)); err != nil {
		t.Fatal(err)
	}
}

// A small input limit still leaves room for a patch of 64 KiB, and no more.
func TestPatchTextFloor(t *testing.T) {
	d := mustParse(t, "a: 1\n", Limits(LimitSet{MaxInputBytes: 200_000}))
	patch := func(n int) []byte { return []byte(`{"b":1}` + strings.Repeat(" ", n)) }
	if err := d.ApplyMergePatch(patch(60_000)); err != nil {
		t.Fatal(err)
	}
	var le *LimitError
	if err := d.ApplyMergePatch(patch(70_000)); !errors.As(err, &le) || le.Max != 64<<10 {
		t.Fatalf("got %v", err)
	}
	if got := patchTextMax(100); got != 100 {
		t.Fatalf("patchTextMax(100) = %d", got)
	}
}
