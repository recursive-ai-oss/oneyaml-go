package oneyaml_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
)

func TestMergeWalkSelfMergeEnds(t *testing.T) {
	var b strings.Builder
	b.WriteString("a: &a\n")
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&b, "  k%d: %d\n", i, i)
	}
	b.WriteString("  <<: *a\n")
	doc, err := oneyaml.Parse([]byte(b.String()), oneyaml.MergeKeys())
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := doc.Get("/a/missing", &v); !errors.Is(err, oneyaml.ErrNotFound) {
		t.Fatalf("got %v, want not found", err)
	}
	if err := doc.Get("/a/k1999", &v); err != nil || v != any(1999) {
		t.Fatalf("own key: %v %v", v, err)
	}
}

func TestMergeWalkChainFindsDeepSource(t *testing.T) {
	var b strings.Builder
	for l := 0; l < 20; l++ {
		fmt.Fprintf(&b, "l%d: &l%d\n", l, l)
		for i := 0; i < 100; i++ {
			fmt.Fprintf(&b, "  k%d_%d: %d\n", l, i, l)
		}
		if l > 0 {
			fmt.Fprintf(&b, "  <<: [*l%d, *l%d]\n", l-1, l-1)
		}
	}
	doc, err := oneyaml.Parse([]byte(b.String()), oneyaml.MergeKeys())
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := doc.Get("/l19/k0_5", &v); err != nil || v != any(0) {
		t.Fatalf("deep source: %v %v", v, err)
	}
	if err := doc.Get("/l19/nope", &v); !errors.Is(err, oneyaml.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestMergeWalkBudgetIsLimitError(t *testing.T) {
	var b strings.Builder
	b.WriteString("a: &a\n")
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "  k%d: %d\n", i, i)
	}
	b.WriteString("b:\n  <<: *a\n")
	lim := oneyaml.DefaultLimits()
	lim.MaxAliasNodes = 100
	doc, err := oneyaml.Parse([]byte(b.String()), oneyaml.MergeKeys(), oneyaml.Limits(lim))
	if err != nil {
		t.Fatal(err)
	}
	var v any
	err = doc.Get("/b/missing", &v)
	var le *oneyaml.LimitError
	if !errors.As(err, &le) {
		t.Fatalf("got %v, want a LimitError", err)
	}
}

func TestPatchMoveOfMergedOnlyKeyIsAliasWrite(t *testing.T) {
	doc := mergePtrParse(t, oneyaml.MergeKeys())
	err := doc.ApplyPatch([]byte(`[{"op":"move","from":"/use/w","path":"/use/moved"}]`))
	if !errors.Is(err, oneyaml.ErrAliasWrite) {
		t.Fatalf("got %v, want ErrAliasWrite", err)
	}
	err = doc.ApplyPatch([]byte(`[{"op":"move","from":"/use/nothing","path":"/use/moved"}]`))
	if !errors.Is(err, oneyaml.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestPatchCopyFromMergedOnlyKey(t *testing.T) {
	doc := mergePtrParse(t, oneyaml.MergeKeys())
	before := string(doc.Bytes())
	// w is only in the second source; n is a nested mapping; y is the mapping's own.
	for _, c := range []struct{ from, to, line string }{
		{"/use/w", "/use/copied", "  copied: 5\n"},
		{"/use/x", "/use/cx", "  cx: 1\n"},
		{"/use/y", "/use/cy", "  cy: 20\n"},
	} {
		d := mergePtrParse(t, oneyaml.MergeKeys())
		if err := d.ApplyPatch([]byte(`[{"op":"copy","from":"` + c.from + `","path":"` + c.to + `"}]`)); err != nil {
			t.Fatalf("%s: %v", c.from, err)
		}
		// Everything but the added line is byte-identical: the merge source is unchanged.
		want := strings.Replace(before, "  z: 3\n", "  z: 3\n"+c.line, 1)
		if got := string(d.Bytes()); got != want {
			t.Errorf("%s: got\n%s\nwant\n%s", c.from, got, want)
		}
	}

	// A nested merged mapping is copied whole.
	d := mergePtrParse(t, oneyaml.MergeKeys())
	if err := d.ApplyPatch([]byte(`[{"op":"copy","from":"/use/n","path":"/use/m"}]`)); err != nil {
		t.Fatal(err)
	}
	var k string
	if err := d.Get("/use/m/k", &k); err != nil || k != "a" {
		t.Fatalf("copied mapping: %q %v", k, err)
	}
	if err := d.Get("/a/n/k", &k); err != nil || k != "a" {
		t.Fatalf("source: %q %v", k, err)
	}

	// Without MergeKeys the key is not there.
	plain := mergePtrParse(t)
	err := plain.ApplyPatch([]byte(`[{"op":"copy","from":"/use/w","path":"/use/c"}]`))
	if !errors.Is(err, oneyaml.ErrNotFound) {
		t.Fatalf("without MergeKeys: %v", err)
	}
	if string(plain.Bytes()) != before {
		t.Error("failed patch changed the document")
	}
}

func TestPatchCopyFromMergeBudgetIsLimitError(t *testing.T) {
	var b strings.Builder
	b.WriteString("a: &a\n")
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "  k%d: %d\n", i, i)
	}
	b.WriteString("b:\n  <<: *a\n")
	lim := oneyaml.DefaultLimits()
	lim.MaxAliasNodes = 100
	doc, err := oneyaml.Parse([]byte(b.String()), oneyaml.MergeKeys(), oneyaml.Limits(lim))
	if err != nil {
		t.Fatal(err)
	}
	err = doc.ApplyPatch([]byte(`[{"op":"copy","from":"/b/missing","path":"/c"}]`))
	var le *oneyaml.LimitError
	if !errors.As(err, &le) {
		t.Fatalf("got %v, want a LimitError", err)
	}
}
