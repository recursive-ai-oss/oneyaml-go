package oneyaml

import (
	"strings"
	"testing"
)

type benchHook struct{ v string }

func (h *benchHook) UnmarshalYAML(n Node) error { return n.Decode(&h.v) }

type benchHookDoc struct {
	Items []benchHook
}

func benchHookInput() []byte {
	var sb strings.Builder
	sb.WriteString("items:\n")
	for range 200 {
		sb.WriteString("  - some value\n")
	}
	return []byte(sb.String())
}

// BenchmarkUnmarshalHook reads 200 values through an Unmarshaler that decodes
// its own Node, the route every hook call takes.
func BenchmarkUnmarshalHook(b *testing.B) {
	src := benchHookInput()
	b.ReportAllocs()
	for b.Loop() {
		var v benchHookDoc
		if err := Unmarshal(src, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDocumentDecodeHook is the same read from a parsed Document.
func BenchmarkDocumentDecodeHook(b *testing.B) {
	d, err := Parse(benchHookInput())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		var v benchHookDoc
		if err := d.Decode(&v); err != nil {
			b.Fatal(err)
		}
	}
}
