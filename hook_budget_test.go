package oneyaml_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	oneyaml "oneyaml.dev/go"
)

type budgetItem struct{}

var budgetCalls int

func (*budgetItem) UnmarshalYAML(n oneyaml.Node) error {
	budgetCalls++
	if n.Kind() == oneyaml.SequenceNode {
		var xs []budgetItem
		return n.Decode(&xs)
	}
	return nil
}

// A nested Node.Decode inside an Unmarshaler must draw on the alias budget of
// the call that started it; a fresh budget per level would let a few hundred
// bytes expand without bound.
func budgetBomb() []byte {
	var b strings.Builder
	b.WriteString("- &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i < 8; i++ {
		fmt.Fprintf(&b, "- &a%d [", i)
		for j := 0; j < 10; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "*a%d", i-1)
		}
		b.WriteString("]\n")
	}
	return []byte(b.String())
}

func TestNestedDecodeSharesAliasBudget(t *testing.T) {
	in := budgetBomb()
	parse := func(opts ...oneyaml.Option) func() error {
		return func() error {
			d, err := oneyaml.Parse(in, opts...)
			if err != nil {
				return err
			}
			var xs []budgetItem
			return d.Decode(&xs)
		}
	}
	for name, run := range map[string]func() error{
		"unmarshal":          func() error { var xs []budgetItem; return oneyaml.Unmarshal(in, &xs) },
		"document":           parse(),
		"document untrusted": parse(oneyaml.Untrusted()),
	} {
		budgetCalls = 0
		err := run()
		var le *oneyaml.LimitError
		if !errors.As(err, &le) {
			t.Errorf("%s: want a LimitError, got %v after %d hook calls", name, err, budgetCalls)
		}
		if budgetCalls > 2_000_000 {
			t.Errorf("%s: %d hook calls before the limit", name, budgetCalls)
		}
	}
}

type keptItem struct{ n oneyaml.Node }

func (k *keptItem) UnmarshalYAML(n oneyaml.Node) error { k.n = n; return nil }

// A Node kept past UnmarshalYAML and decoded again and again starts a budget of
// its own each time; it must not add up to the limit or touch other calls.
func TestKeptNodeDecodesWithFreshBudget(t *testing.T) {
	doc := []byte("a: &a [" + strings.TrimSuffix(strings.Repeat("x, ", 100), ", ") + "]\nb: *a\n")
	d, err := oneyaml.Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	var k keptItem
	if err := d.Decode(&k); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	var kerr error
	go func() {
		defer wg.Done()
		for i := 0; i < 12000 && kerr == nil; i++ {
			var v any
			kerr = k.n.Decode(&v)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			var m map[string]any
			_ = oneyaml.Unmarshal(doc, &m)
			var o keptItem
			_ = oneyaml.Unmarshal(doc, &o)
		}
	}()
	wg.Wait()
	if kerr != nil {
		t.Fatalf("decode of the kept node: %v", kerr)
	}
}
