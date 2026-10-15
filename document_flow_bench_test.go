package oneyaml

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkFlowAppends appends n items one by one to a flow sequence and to a flow
// mapping that sit in a block mapping. The cost per append must not grow with n.
func BenchmarkFlowAppends(b *testing.B) {
	noSpliceCheck(b)
	for _, n := range []int{1000, 8000} {
		for _, kind := range []string{"sequence", "mapping"} {
			head := "a: 1\nflow: [x]\nb: 2\n"
			if kind == "mapping" {
				head = "a: 1\nflow: {z: 0}\nb: 2\n"
			}
			b.Run(fmt.Sprintf("%s/n=%d", kind, n), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					d, err := Parse([]byte(head))
					if err != nil {
						b.Fatal(err)
					}
					for i := 0; i < n; i++ {
						ptr := "/flow/-"
						if kind == "mapping" {
							ptr = fmt.Sprintf("/flow/k%d", i)
						}
						if err := d.Set(ptr, i); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}

// BenchmarkEditFlowMappingManyKeys is the shape of a flow mapping with 140,000
// keys, one per line: 50 replacements and 50 removals.
func BenchmarkEditFlowMappingManyKeys(b *testing.B) {
	noSpliceCheck(b)
	var sb strings.Builder
	sb.WriteString("{")
	for i := 0; i < 140000; i++ {
		fmt.Fprintf(&sb, "k%d: %d,\n", i, i)
	}
	sb.WriteString("}\n")
	src := []byte(sb.String())
	b.ReportAllocs()
	for b.Loop() {
		d, err := Parse(src)
		if err != nil {
			b.Fatal(err)
		}
		for i := 0; i < 50; i++ {
			if err := d.Set(fmt.Sprintf("/k%d", 70000+i), 7); err != nil {
				b.Fatal(err)
			}
			if err := d.Remove(fmt.Sprintf("/k%d", 100000+i)); err != nil {
				b.Fatal(err)
			}
		}
	}
}
