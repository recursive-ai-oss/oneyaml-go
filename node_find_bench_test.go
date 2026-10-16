package oneyaml

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkNodeFind reads keys of mappings of several widths through Find and
// Lookup: the cost of a lookup must not grow because it checks for a repeat.
func BenchmarkNodeFind(b *testing.B) {
	for _, n := range []int{8, 64, 4096} {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&sb, "key%d: %d\n", i, i)
		}
		doc, err := Parse([]byte(sb.String()))
		if err != nil {
			b.Fatal(err)
		}
		root := doc.Root()
		key := fmt.Sprintf("key%d", n/4)
		b.Run(fmt.Sprintf("Find/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := root.Find(key); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("Lookup/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, ok := root.Lookup(key); !ok {
					b.Fatal("missing")
				}
			}
		})
	}
}
