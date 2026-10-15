package oneyaml

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkSmallRemoveRun removes m keys from a mapping of n keys, spread over it or
// taken from its front, with an RFC 6902 patch, under the default limits: the sizes around the point where one
// pass over the text starts to pay.
func BenchmarkSmallRemoveRun(b *testing.B) {
	noSpliceCheck(b)
	for _, c := range []struct {
		n, m  int
		front bool
	}{
		{500000, 8, false}, {500000, 12, false}, {5000, 8, false}, {5000, 12, false}, {500000, 40, false},
		{500000, 9, true}, {500000, 12, true}, {5000, 9, true}, {5000, 16, true},
	} {
		shape, step := "spread", c.n/c.m
		if c.front {
			shape, step = "front", 1
		}
		var sb strings.Builder
		for i := 0; i < c.n; i++ {
			fmt.Fprintf(&sb, "k%07d: %d\n", i, i)
		}
		src := []byte(sb.String())
		var ops []string
		for i := 0; i < c.m; i++ {
			ops = append(ops, fmt.Sprintf(`{"op":"remove","path":"/k%07d"}`, i*step))
		}
		patch := []byte("[" + strings.Join(ops, ",") + "]")
		b.Run(fmt.Sprintf("n=%d/m=%d/%s", c.n, c.m, shape), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				d, err := Parse(src)
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				if err := d.ApplyPatch(patch); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
