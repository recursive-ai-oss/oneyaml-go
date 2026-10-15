package oneyaml

import (
	"reflect"
	"strings"
	"testing"
)

// A new key too long to be an implicit key (1024 characters) is written as an
// explicit key, in block and in flow mappings, and reads back exactly.
func TestLongKeysAreWrittenAsExplicitKeys(t *testing.T) {
	long := strings.Repeat("k", 1100)
	quoted := strings.Repeat("q ", 600) // needs no escapes but is not plain-safe at the end
	cases := []struct {
		name, src string
		set       func(d *Document) error
		want      any
	}{
		{"block add", "a: 1\n", func(d *Document) error { return d.Set("/"+long, 2) }, map[string]any{"a": 1, long: 2}},
		{"block add nested", "a:\n  b: 1\n", func(d *Document) error { return d.Set("/a/"+long, []int{1, 2}) },
			map[string]any{"a": map[string]any{"b": 1, long: []any{1, 2}}}},
		{"block add with text needing quotes", "a: 1\n", func(d *Document) error { return d.Set("/"+quoted, "v") }, map[string]any{"a": 1, quoted: "v"}},
		{"flow add", "m: {a: 1}\n", func(d *Document) error { return d.Set("/m/"+long, 2) },
			map[string]any{"m": map[string]any{"a": 1, long: 2}}},
		{"flow add to empty", "m: {}\n", func(d *Document) error { return d.Set("/m/"+long, 2) },
			map[string]any{"m": map[string]any{long: 2}}},
		{"value with a long key", "a: 1\n", func(d *Document) error { return d.Set("/b", map[string]any{long: 3}) },
			map[string]any{"a": 1, "b": map[string]any{long: 3}}},
		{"patch add", "a: 1\n", func(d *Document) error {
			return d.ApplyPatch([]byte(`[{"op":"add","path":"/` + long + `","value":2}]`))
		}, map[string]any{"a": 1, long: 2}},
		{"merge patch", "a: 1\n", func(d *Document) error {
			return d.ApplyMergePatch([]byte(`{"` + long + `":2}`))
		}, map[string]any{"a": 1, long: 2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := mustParse(t, c.src)
			if err := c.set(d); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := Unmarshal(d.Bytes(), &got); err != nil {
				t.Fatalf("does not decode: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("reads back differently\n%.200s", d.Bytes())
			}
			if err := sameAsFreshParse(d); err != nil {
				t.Fatal(err)
			}
			// The new key can be read, changed and removed again.
			key := ""
			for k := range got {
				if len(k) > 1000 {
					key = k
				}
			}
			if key != "" {
				var v any
				if err := d.Get("/"+key, &v); err != nil {
					t.Fatalf("Get of the long key: %v", err)
				}
				if err := d.Remove("/" + key); err != nil {
					t.Fatalf("Remove of the long key: %v", err)
				}
			}
		})
	}
	// The first case is exactly the explicit layout, and nothing else changed.
	d := mustParse(t, "a: 1\n")
	if err := d.Set("/"+long, 2); err != nil {
		t.Fatal(err)
	}
	if want := "a: 1\n? " + long + "\n: 2\n"; string(d.Bytes()) != want {
		t.Fatalf("got %.80q", d.Bytes())
	}
}
