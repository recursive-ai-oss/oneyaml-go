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
