package bench

import (
	"fmt"
	"strings"
	"testing"

	goccyast "github.com/goccy/go-yaml/ast"
	goccyparser "github.com/goccy/go-yaml/parser"
	yamlv3 "go.yaml.in/yaml/v3"
	oneyaml "oneyaml.dev/go"
)

// wideMap writes one flat mapping of mixed scalars of about size bytes, the shape
// of the kit's wide-map workload.
func wideMap(size int) []byte {
	var b strings.Builder
	for i := 0; b.Len() < size; i++ {
		switch i % 6 {
		case 0:
			fmt.Fprintf(&b, "key%d: %d\n", i, i)
		case 1:
			fmt.Fprintf(&b, "key%d: %v\n", i, float64(i)/8)
		case 2:
			fmt.Fprintf(&b, "key%d: %v\n", i, i%12 == 2)
		case 3:
			fmt.Fprintf(&b, "key%d: plain text %d\n", i, i)
		case 4:
			fmt.Fprintf(&b, "key%d: \"quoted: %d\"\n", i, i)
		default:
			fmt.Fprintf(&b, "key%d: null\n", i)
		}
	}
	return []byte(b.String())
}

// BenchmarkEditWideMap is the kit's edit script on its wide-map workload: replace
// the first value, remove the fourth entry, write the result. The clock covers the
// edits and the write, as in the kit's harness; the parse that makes the document
// is outside it.
func BenchmarkEditWideMap(b *testing.B) {
	for _, size := range []int{64 << 10, 1 << 20} {
		data := wideMap(size)
		name := fmt.Sprintf("wide-map-%dk", size>>10)
		b.Run(name+"/oneyaml", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				doc, err := oneyaml.Parse(data)
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				if err := doc.Set("/key0", 42); err != nil {
					b.Fatal(err)
				}
				if err := doc.Remove("/key3"); err != nil {
					b.Fatal(err)
				}
				sink = doc.Bytes()
			}
		})
		b.Run(name+"/yaml.v3", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				var n yamlv3.Node
				if err := yamlv3.Unmarshal(data, &n); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				m := n.Content[0]
				m.Content[1].Value = "42"
				m.Content = append(m.Content[:6], m.Content[8:]...)
				out, err := yamlv3.Marshal(&n)
				if err != nil {
					b.Fatal(err)
				}
				sink = out
			}
		})
		b.Run(name+"/goccy", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				f, err := goccyparser.ParseBytes(data, goccyparser.ParseComments)
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				m := f.Docs[0].Body.(*goccyast.MappingNode)
				first, _ := m.Values[0].Value.(*goccyast.IntegerNode)
				first.Value = 42
				m.Values = append(m.Values[:3], m.Values[4:]...)
				sink = []byte(f.String())
			}
		})
	}
}

// BenchmarkEditScale applies edits that are not a single inline scalar (a value
// that needs a new style, an added entry, a removed entry) to one large document and
// reports the cost per edit. The text does not change in size over a run.
func BenchmarkEditScale(b *testing.B) {
	for _, size := range []int{64 << 10, 1 << 20} {
		data := wideMap(size)
		b.Run(fmt.Sprintf("wide-map-%dk", size>>10), func(b *testing.B) {
			doc, err := oneyaml.Parse(data)
			if err != nil {
				b.Fatal(err)
			}
			entries := doc.Root().Len()
			const edits = 100
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				for k := 0; k < edits/4; k++ {
					i++
					at := fmt.Sprintf("/key%d", (i*37)%entries)
					if err := doc.Set(at, "two\nlines"); err != nil {
						b.Fatal(err)
					}
					if err := doc.Set(at, i); err != nil {
						b.Fatal(err)
					}
					if err := doc.Set("/zz-added", i); err != nil {
						b.Fatal(err)
					}
					if err := doc.Remove("/zz-added"); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*edits), "ns/edit")
			sink = doc.Bytes()
		})
	}
}
