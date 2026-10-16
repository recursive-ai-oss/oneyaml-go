package oneyaml_test

import (
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
)

func accessDoc(b *testing.B, opts ...oneyaml.Option) *oneyaml.Document {
	var sb strings.Builder
	sb.WriteString("base: &b {shared: 1}\nitems:\n  <<: *b\n")
	for i := 0; i < 200; i++ {
		sb.WriteString("  k")
		sb.WriteString(strings.Repeat("x", i%7))
		sb.WriteString(string(rune('a'+i%26)) + string(rune('a'+i/26)))
		sb.WriteString(": value\n")
	}
	doc, err := oneyaml.Parse([]byte(sb.String()), opts...)
	if err != nil {
		b.Fatal(err)
	}
	return doc
}

func BenchmarkGetPointer(b *testing.B) {
	for _, mk := range []bool{false, true} {
		name := "plain"
		var opts []oneyaml.Option
		if mk {
			name = "mergekeys"
			opts = append(opts, oneyaml.MergeKeys())
		}
		b.Run(name, func(b *testing.B) {
			doc := accessDoc(b, opts...)
			var s string
			b.ReportAllocs()
			for b.Loop() {
				_ = doc.Get("/items/kxxxda", &s)
			}
		})
	}
}

func BenchmarkNodeAccessors(b *testing.B) {
	doc := accessDoc(b)
	var items oneyaml.Node
	if err := doc.Get("/items", &items); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		n, _ := items.Lookup("kxxxda")
		_ = n.Kind()
		_ = n.Len()
		_ = n.Err()
		_ = items.Len()
		_ = items.IsValid()
	}
}
