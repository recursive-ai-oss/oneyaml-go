package oneyaml_test

import (
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
)

// BenchmarkNodeMethodsLongLine reads nodes of a document with no line breaks: the
// cost of one call must not depend on the length of the document.
func BenchmarkNodeMethodsLongLine(b *testing.B) {
	src := "[" + strings.TrimSuffix(strings.Repeat("&a 0, *a, ", 5000), ", ") + "]"
	d, err := oneyaml.Parse([]byte(src))
	if err != nil {
		b.Fatal(err)
	}
	root := d.Root()
	b.ReportAllocs()
	for b.Loop() {
		for it := range root.Items() {
			it.Target()
			it.LineComment()
		}
	}
}

// BenchmarkPatchRemoveFirstKeys removes the first keys of a large block mapping.
func BenchmarkPatchRemoveFirstKeys(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 20000; i++ {
		sb.WriteString("key" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + "_" + itoa(i) + ": value\n")
	}
	src := sb.String()
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		d, err := oneyaml.Parse([]byte(src))
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		for i := 0; i < 100; i++ {
			k := "/key" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + "_" + itoa(i)
			if err := d.Remove(k); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for ; i > 0; i /= 10 {
		p--
		buf[p] = byte('0' + i%10)
	}
	return string(buf[p:])
}
