package bench

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"

	oneyaml "oneyaml.dev/go"
)

// Many adds into one wide mapping: the cost of an add should not grow with the size
// of the mapping, so the time for the same 1000 adds into 2000 and 20000 keys should
// differ by little. Parsing is outside the timer for oneyaml (the Document is held),
// and the competitor works on its decoded document.

func wideMappingDocs(keys int) (yamlDoc, jsonDoc string) {
	var y, j strings.Builder
	j.WriteString("{")
	for i := 0; i < keys; i++ {
		fmt.Fprintf(&y, "k%d: %d\n", i, i)
		if i > 0 {
			j.WriteByte(',')
		}
		fmt.Fprintf(&j, `"k%d":%d`, i, i)
	}
	j.WriteString("}")
	return y.String(), j.String()
}

func BenchmarkPatchManyAdds(b *testing.B) {
	const adds = 1000
	var ops, merge []string
	for i := 0; i < adds; i++ {
		ops = append(ops, `{"op":"add","path":"/n`+strconv.Itoa(i)+`","value":1}`)
		merge = append(merge, `"n`+strconv.Itoa(i)+`":1`)
	}
	patch6902 := []byte("[" + strings.Join(ops, ",") + "]")
	patch7396 := []byte("{" + strings.Join(merge, ",") + "}")
	for _, keys := range []int{2000, 20000} {
		yamlDoc, jsonDoc := wideMappingDocs(keys)
		name := "keys=" + strconv.Itoa(keys)
		b.Run("6902/"+name+"/oneyaml-doc", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				d, err := oneyaml.Parse([]byte(yamlDoc))
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				if err := d.ApplyPatch(patch6902); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("7396/"+name+"/oneyaml-doc", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				d, err := oneyaml.Parse([]byte(yamlDoc))
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				if err := d.ApplyMergePatch(patch7396); err != nil {
					b.Fatal(err)
				}
			}
		})
		jp, err := jsonpatch.DecodePatch(patch6902)
		if err != nil {
			b.Fatal(err)
		}
		b.Run("6902/"+name+"/json-patch", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := jp.Apply([]byte(jsonDoc)); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("7396/"+name+"/json-patch", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := jsonpatch.MergePatch([]byte(jsonDoc), patch7396); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
