package oneyaml_test

import (
	"fmt"
	"strings"
	"testing"

	"oneyaml.dev/go"
)

// ignoredDoc is a list of records of which the target reads two keys and ignores
// the rest, some of them nested: the shape of a manifest read into a small struct.
func ignoredDoc(records int) []byte {
	var sb strings.Builder
	for i := 0; i < records; i++ {
		fmt.Fprintf(&sb, "- id: %d\n  name: n%d\n  extra1: a\n  extra2: b\n  labels:\n    app: x\n    tier: y\n    env: z\n  ports: [80, 443]\n  meta: {owner: o, team: t}\n", i, i)
	}
	return []byte(sb.String())
}

// BenchmarkLoadTypedIgnoredKeys reads records into a struct that ignores most of
// their keys: every ignored key is checked for a repeat.
func BenchmarkLoadTypedIgnoredKeys(b *testing.B) {
	data := ignoredDoc(400)
	type rec struct {
		ID   int
		Name string
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		var out []rec
		if err := oneyaml.Unmarshal(data, &out); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoadTypedIgnoredWideMapping has one mapping of many keys that the
// target ignores, which moves the repeat check to its hashed form.
func BenchmarkLoadTypedIgnoredWideMapping(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&sb, "key%d: %d\n", i, i)
	}
	data := []byte(sb.String())
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		var out struct{ Key1 int }
		if err := oneyaml.Unmarshal(data, &out); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoadTypedNoIgnoredKeys is the control: every key has a field.
func BenchmarkLoadTypedNoIgnoredKeys(b *testing.B) {
	data := ignoredDoc(400)
	type rec struct {
		ID     int
		Name   string
		Extra1 string
		Extra2 string
		Labels map[string]string
		Ports  []int
		Meta   map[string]string
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		var out []rec
		if err := oneyaml.Unmarshal(data, &out); err != nil {
			b.Fatal(err)
		}
	}
}
