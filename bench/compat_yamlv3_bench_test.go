package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	yv3 "go.yaml.in/yaml/v3"
	compat "oneyaml.dev/go/compat/yamlv3"
)

// The rows below compare the shim with the real yaml.v3 on the same inputs:
// generic load, typed load, Node load, and dump of the decoded value.

type v3Lib struct {
	name      string
	unmarshal func([]byte, any) error
	marshal   func(any) ([]byte, error)
	newNode   func() any
}

var v3Libs = []v3Lib{
	{"yaml.v3", yv3.Unmarshal, yv3.Marshal, func() any { return new(yv3.Node) }},
	{"compat", compat.Unmarshal, compat.Marshal, func() any { return new(compat.Node) }},
}

func compatBenchInputs(b *testing.B) []loadInput {
	in := loadInputs(b)
	// A single document is enough for the stream-free calls.
	return in
}

// BenchmarkCompatV3LoadGeneric: Unmarshal into interface{}.
func BenchmarkCompatV3LoadGeneric(b *testing.B) {
	for _, w := range compatBenchInputs(b) {
		if w.docs != 1 {
			continue
		}
		for _, l := range v3Libs {
			if err := l.unmarshal(w.data, new(any)); err != nil {
				b.Logf("%s/%s: %v (skipped)", w.name, l.name, err)
				continue
			}
			b.Run(fmt.Sprintf("%s/library=%s", w.name, l.name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(w.data)))
				for b.Loop() {
					var v any
					if err := l.unmarshal(w.data, &v); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkCompatV3LoadTyped: Unmarshal into structs.
func BenchmarkCompatV3LoadTyped(b *testing.B) {
	type model struct {
		name string
		data []byte
		new  func() any
	}
	models := []model{{"records-40k", recordsDoc(40 << 10), func() any { return new([]Record) }}}
	if kit := os.Getenv("ONEYAML_KIT"); kit != "" {
		raw, err := os.ReadFile(filepath.Join(kit, "bench/workloads/real/gha-workflow/input.yaml"))
		if err != nil {
			b.Fatal(err)
		}
		models = append(models, model{"gha-workflow", raw, func() any { return new(Workflow) }})
	}
	for _, m := range models {
		for _, l := range v3Libs {
			if err := l.unmarshal(m.data, m.new()); err != nil {
				b.Logf("%s/%s: %v (skipped)", m.name, l.name, err)
				continue
			}
			b.Run(fmt.Sprintf("%s/library=%s", m.name, l.name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(m.data)))
				for b.Loop() {
					if err := l.unmarshal(m.data, m.new()); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkCompatV3LoadNode: Unmarshal into a Node tree.
func BenchmarkCompatV3LoadNode(b *testing.B) {
	for _, w := range compatBenchInputs(b) {
		if w.docs != 1 {
			continue
		}
		for _, l := range v3Libs {
			if err := l.unmarshal(w.data, l.newNode()); err != nil {
				b.Logf("%s/%s: %v (skipped)", w.name, l.name, err)
				continue
			}
			b.Run(fmt.Sprintf("%s/library=%s", w.name, l.name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(w.data)))
				for b.Loop() {
					if err := l.unmarshal(w.data, l.newNode()); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkCompatV3Dump: Marshal of a decoded generic value and of typed records.
func BenchmarkCompatV3Dump(b *testing.B) {
	type model struct {
		name  string
		value any
	}
	var models []model
	var recs []Record
	if err := yv3.Unmarshal(recordsDoc(40<<10), &recs); err != nil {
		b.Fatal(err)
	}
	models = append(models, model{"records-40k-typed", recs})
	for _, w := range compatBenchInputs(b) {
		if w.docs != 1 {
			continue
		}
		var v any
		if err := yv3.Unmarshal(w.data, &v); err != nil {
			continue
		}
		models = append(models, model{w.name + "-generic", v})
	}
	for _, m := range models {
		for _, l := range v3Libs {
			if _, err := l.marshal(m.value); err != nil {
				b.Logf("%s/%s: %v (skipped)", m.name, l.name, err)
				continue
			}
			b.Run(fmt.Sprintf("%s/library=%s", m.name, l.name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := l.marshal(m.value); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
