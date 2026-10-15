package oneyaml

import (
	"fmt"
	"strings"
	"testing"
)

func largeMapping(n int) []byte {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "k%d: %d\n", i, i%10)
	}
	return []byte(sb.String())
}

// BenchmarkMergePatchDeletions is a merge patch of 3,000 deletions out of a
// mapping of 500,000 keys, under the default limits.
func BenchmarkMergePatchDeletions(b *testing.B) {
	noSpliceCheck(b)
	src := largeMapping(500000)
	var m []string
	for i := 0; i < 3000; i++ {
		m = append(m, fmt.Sprintf(`"k%d":null`, i*100))
	}
	patch := []byte("{" + strings.Join(m, ",") + "}")
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		d, err := Parse(src)
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := d.ApplyMergePatch(patch); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRemoveFirstKeys is 200 consecutive removes of the first keys of a
// mapping of 500,000 keys, with no limits so that the patch runs at every version.
func BenchmarkRemoveFirstKeys(b *testing.B) {
	noSpliceCheck(b)
	src := largeMapping(500000)
	var r []string
	for i := 0; i < 200; i++ {
		r = append(r, fmt.Sprintf(`{"op":"remove","path":"/k%d"}`, i))
	}
	patch := []byte("[" + strings.Join(r, ",") + "]")
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		d, err := Parse(src, NoLimits())
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := d.ApplyPatch(patch); err != nil {
			b.Fatal(err)
		}
	}
}
