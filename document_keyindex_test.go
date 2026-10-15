package oneyaml

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func wideKeysDoc(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "k%d: %d\n", i, i)
	}
	return b.String()
}

// The work of an add into a large mapping does not grow with the mapping: the keys
// read per add are the same however many keys there are.
func TestAddsIntoALargeMappingReadAConstantNumberOfKeys(t *testing.T) {
	var per []float64
	for _, keys := range []int{500, 2000, 8000} {
		d := mustParse(t, wideKeysDoc(keys))
		add := func(from, to int) {
			var ops []string
			for i := from; i < to; i++ {
				ops = append(ops, fmt.Sprintf(`{"op":"add","path":"/n%d","value":%d}`, i, i))
			}
			if err := d.ApplyPatch([]byte("[" + strings.Join(ops, ",") + "]")); err != nil {
				t.Fatal(err)
			}
		}
		add(0, 10) // the index is built during the first adds
		c := d.s.ed.cache.Keys
		before := c.Compared
		add(10, 210)
		per = append(per, float64(c.Compared-before)/200)
		if err := sameAsFreshParse(d); err != nil {
			t.Fatal(err)
		}
		var got map[string]int
		if err := Unmarshal(d.Bytes(), &got); err != nil || len(got) != keys+210 {
			t.Fatalf("%d keys: %d read back, %v", keys, len(got), err)
		}
	}
	for i, p := range per {
		if p > 4 {
			t.Errorf("size %d: %.1f keys read per add", i, p)
		}
	}
}

// A key that occurs twice through two spellings is still refused once the index
// exists, and a key the index does not hold is still found absent.
func TestKeyIndexKeepsAmbiguousKeysRefused(t *testing.T) {
	src := wideKeysDoc(400) + "80: a\n\"80\": b\n"
	d := mustParse(t, src)
	for i := 0; i < 6; i++ { // enough lookups to build the index
		if err := d.Set(fmt.Sprintf("/n%d", i), i); err != nil {
			t.Fatal(err)
		}
	}
	if d.s.ed.cache.Keys.Built() == 0 {
		t.Fatal("no index was built")
	}
	for _, edit := range []func() error{
		func() error { return d.Set("/80", "x") },
		func() error { return d.Remove("/80") },
		func() error { return d.ApplyPatch([]byte(`[{"op":"replace","path":"/80","value":1}]`)) },
	} {
		before := string(d.Bytes())
		if err := edit(); !errors.Is(err, ErrAmbiguousKey) {
			t.Fatalf("got %v", err)
		}
		if string(d.Bytes()) != before {
			t.Fatal("document changed")
		}
	}
	var x any
	if err := d.Get("/80", &x); !errors.Is(err, ErrAmbiguousKey) {
		t.Fatalf("Get: %v", err)
	}
	if err := d.Remove("/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove of a missing key: %v", err)
	}
	// Setting a key that already exists replaces its value; it does not add a second one.
	if err := d.Set("/k5", 55); err != nil {
		t.Fatal(err)
	}
	var v int
	if err := d.Get("/k5", &v); err != nil || v != 55 {
		t.Fatalf("k5 = %d, %v", v, err)
	}
}

// Whatever the edits, a document that keeps its key indexes ends up byte for byte
// where one that drops them before every edit does, and both are what a fresh
// parse of the text gives.
func TestKeyIndexAgreesWithScanning(t *testing.T) {
	var nested strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&nested, "  m%d: %d\n", i, i)
	}
	src := wideKeysDoc(300) + "sub:\n" + nested.String() + "tail: end\n"
	for seed := int64(1); seed <= 6; seed++ {
		rng := rand.New(rand.NewSource(seed))
		a, b := mustParse(t, src), mustParse(t, src)
		steps := 400
		if testing.Short() {
			steps = 150
		}
		for step := 0; step < steps; step++ {
			var do func(d *Document) error
			switch rng.Intn(9) {
			case 0, 1, 2:
				p := fmt.Sprintf("/n%d", rng.Intn(60))
				do = func(d *Document) error { return d.Set(p, step) }
			case 3:
				p := fmt.Sprintf("/sub/s%d", rng.Intn(60))
				do = func(d *Document) error { return d.Set(p, "text") }
			case 4:
				p := fmt.Sprintf("/k%d", rng.Intn(300))
				do = func(d *Document) error { return d.Set(p, step) } // inline scalar replace
			case 5:
				p := fmt.Sprintf("/k%d", rng.Intn(300))
				do = func(d *Document) error { return d.Set(p, []int{step, 2}) } // collection value
			case 6:
				p := fmt.Sprintf("/n%d", rng.Intn(60))
				do = func(d *Document) error { return d.Remove(p) }
			case 7:
				p := fmt.Sprintf("/sub/m%d", rng.Intn(300))
				do = func(d *Document) error { return d.Remove(p) }
			default:
				from, to := fmt.Sprintf("/k%d", rng.Intn(300)), fmt.Sprintf("/mv%d", rng.Intn(20))
				do = func(d *Document) error {
					return d.ApplyPatch([]byte(`[{"op":"move","from":"` + from + `","path":"` + to + `"}]`))
				}
			}
			b.s.ed = nil // no index survives in b
			ea, eb := do(a), do(b)
			if (ea == nil) != (eb == nil) {
				t.Fatalf("seed %d step %d: indexed %v, scanning %v", seed, step, ea, eb)
			}
			if string(a.Bytes()) != string(b.Bytes()) {
				t.Fatalf("seed %d step %d: the documents differ", seed, step)
			}
		}
		if err := sameAsFreshParse(a); err != nil {
			t.Fatal(err)
		}
	}
}
