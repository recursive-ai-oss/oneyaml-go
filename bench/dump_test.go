package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	goccy "github.com/goccy/go-yaml"
	yamlv3 "go.yaml.in/yaml/v3"
	yamlv2 "gopkg.in/yaml.v2"
	sigs "sigs.k8s.io/yaml"

	oneyaml "oneyaml.dev/go"
)

type dumper struct {
	name string
	fn   func(any) ([]byte, error)
}

var dumpers = []dumper{
	{"oneyaml", func(v any) ([]byte, error) { return oneyaml.Marshal(v) }},
	{"yaml.v2", func(v any) ([]byte, error) { return yamlv2.Marshal(v) }},
	{"yaml.v3", func(v any) ([]byte, error) { return yamlv3.Marshal(v) }},
	{"sigs", func(v any) ([]byte, error) { return sigs.Marshal(v) }},
	{"goccy", func(v any) ([]byte, error) { return goccy.Marshal(v) }},
}

type workload struct {
	name  string
	value any
}

// syntheticWorkload builds a deterministic document of a few thousand nodes that
// mixes the shapes real configuration has: nested maps, lists of maps, strings
// that need quoting, numbers, booleans, nulls and a few multi-line strings.
func syntheticWorkload() any {
	items := make([]any, 0, 300)
	for i := 0; i < 300; i++ {
		item := map[string]any{
			"name":     fmt.Sprintf("service-%03d", i),
			"image":    fmt.Sprintf("registry.example.com/team/app-%d:v1.%d.%d", i%7, i%13, i%5),
			"replicas": i%5 + 1,
			"enabled":  i%3 != 0,
			"ratio":    float64(i) / 7,
			"labels":   map[string]any{"app": "svc", "tier": []any{"web", "backend"}[i%2], "version": fmt.Sprintf("%d", i)},
			"ports":    []any{80, 443, 8000 + i},
			"env":      []any{map[string]any{"name": "MODE", "value": "yes"}, map[string]any{"name": "PATH", "value": "/usr/bin:/bin"}},
			"owner":    nil,
		}
		if i%10 == 0 {
			item["script"] = "set -e\nmake build\nmake test\n"
		}
		if i%17 == 0 {
			item["note"] = "contains: a colon and a # hash"
		}
		items = append(items, item)
	}
	return map[string]any{"apiVersion": "v1", "kind": "List", "items": items}
}

func loadWorkloads(b *testing.B) []workload {
	ws := []workload{{"synthetic", syntheticWorkload()}}
	kit := os.Getenv("ONEYAML_KIT")
	if kit == "" {
		return ws
	}
	files, _ := filepath.Glob(filepath.Join(kit, "bench/workloads/*/*/input.yaml"))
	sort.Strings(files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			b.Fatal(err)
		}
		// Decode once, outside the timed loops, with yaml.v3: all libraries then
		// dump the same value.
		var v any
		if err := yamlv3.Unmarshal(raw, &v); err != nil {
			b.Fatalf("%s: %v", f, err)
		}
		ws = append(ws, workload{filepath.Base(filepath.Dir(f)), v})
	}
	return ws
}

// BenchmarkDump measures Marshal of a decoded document for each library. The
// sub-benchmark name is Dump/<workload>/library=<library>; compare libraries
// with benchstat -col /library.
func BenchmarkDump(b *testing.B) {
	for _, w := range loadWorkloads(b) {
		for _, d := range dumpers {
			out, err := d.fn(w.value)
			if err != nil {
				b.Fatalf("%s/%s: %v", w.name, d.name, err)
			}
			b.Run(fmt.Sprintf("%s/library=%s", w.name, strings.ReplaceAll(d.name, ".", "_")), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(out)))
				for b.Loop() {
					if _, err := d.fn(w.value); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
