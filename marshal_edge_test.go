package oneyaml_test

import (
	"testing"
	"time"

	oneyaml "oneyaml.dev/go"
)

type hiddenBase struct {
	Shown int
	When  time.Time
	Tags  []string
}

type viaHidden struct {
	hiddenBase
	Top int
}

type viaHiddenPtr struct {
	*hiddenBase
	Top int
}

// Exported fields promoted through an unexported embedded struct are written, as
// encoding/json does, and reading them must not panic.
func TestUnexportedEmbedded(t *testing.T) {
	base := hiddenBase{Shown: 1, When: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), Tags: []string{"a"}}
	want := "shown: 1\nwhen: 2024-01-02T03:04:05Z\ntags:\n  - a\ntop: 2\n"
	if got := mustMarshal(t, viaHidden{base, 2}); got != want {
		t.Errorf("value embed: %q", got)
	}
	if got := mustMarshal(t, viaHiddenPtr{&base, 2}); got != want {
		t.Errorf("pointer embed: %q", got)
	}
	if got := mustMarshal(t, &viaHidden{base, 2}); got != want {
		t.Errorf("addressable embed: %q", got)
	}
	if got := mustMarshal(t, viaHiddenPtr{nil, 2}); got != "top: 2\n" {
		t.Errorf("nil pointer embed: %q", got)
	}
}

type unsupportedField struct {
	A int
	C chan int `yaml:"c,omitempty"`
	F func()   `yaml:"-"`
}

func TestUnsupportedFieldHandling(t *testing.T) {
	// A nil channel with omitempty is skipped; a skipped field is never looked at.
	if got := mustMarshal(t, unsupportedField{A: 1}); got != "a: 1\n" {
		t.Errorf("got %q", got)
	}
	if _, err := oneyaml.Marshal(unsupportedField{A: 1, C: make(chan int)}); err == nil {
		t.Error("a live channel must be an error")
	}
}

func TestNeverPanics(t *testing.T) {
	var np *int
	var ni any
	var nm map[string]any
	var ns []any
	var ne error
	values := []any{np, ni, nm, ns, ne, struct{}{}, &struct{}{}, [0]int{}, new(any), &ni, &np, map[any]any{nil: nil}, []any{nil}, map[string]any{"": nil}, ""}
	for _, v := range values {
		if _, err := oneyaml.Marshal(v); err != nil {
			t.Errorf("Marshal(%#v): %v", v, err)
		}
	}
	for _, v := range []any{make(chan int), func() {}, complex64(1), uintptrZero()} {
		_, _ = oneyaml.Marshal(v) // must return, error or not
	}
}

func uintptrZero() uintptr { return 0 }
