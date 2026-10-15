package oneyaml_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	oneyaml "oneyaml.dev/go"
)

type benchEnv struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value,omitempty"`
}

type benchItem struct {
	Name     string            `yaml:"name"`
	Image    string            `yaml:"image"`
	Replicas int               `yaml:"replicas"`
	Enabled  bool              `yaml:"enabled"`
	Ratio    float64           `yaml:"ratio"`
	Args     []string          `yaml:"args,omitempty"`
	Env      []benchEnv        `yaml:"env,omitempty"`
	Labels   map[string]string `yaml:"labels,omitempty"`
}

func benchDoc() map[string]any {
	items := make([]any, 0, 100)
	for i := 0; i < 100; i++ {
		items = append(items, map[string]any{
			"name":     fmt.Sprintf("service-%03d", i),
			"image":    fmt.Sprintf("registry.example.com/team/app-%d:v1.%d", i%7, i%13),
			"replicas": i%5 + 1,
			"enabled":  i%3 != 0,
			"ratio":    float64(i) / 7,
			"ports":    []any{80, 443},
			"env":      []any{map[string]any{"name": "MODE", "value": "yes"}},
			"owner":    nil,
		})
	}
	return map[string]any{"apiVersion": "v1", "kind": "List", "items": items}
}

func benchStructs() []benchItem {
	out := make([]benchItem, 100)
	for i := range out {
		out[i] = benchItem{
			Name: fmt.Sprintf("service-%03d", i), Image: "registry.example.com/team/app:v1", Replicas: i%5 + 1,
			Enabled: i%3 != 0, Ratio: float64(i) / 7, Args: []string{"--a", "--b=1"},
			Env: []benchEnv{{"MODE", "yes"}, {"PATH", "/usr/bin"}}, Labels: map[string]string{"app": "svc", "tier": "web"},
		}
	}
	return out
}

func BenchmarkMarshalAny(b *testing.B) {
	doc := benchDoc()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := oneyaml.Marshal(doc); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalStruct(b *testing.B) {
	doc := benchStructs()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := oneyaml.Marshal(doc); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalScalar(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := oneyaml.Marshal("registry.example.com/team/app:v1"); err != nil {
			b.Fatal(err)
		}
	}
}

// TestMarshalAllocs pins the allocation behaviour the encoder is designed for:
// writing generic data costs the output copy and the map key sort buffers only,
// not an allocation per value.
func TestMarshalAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts differ under the race detector")
	}
	doc := benchDoc()
	n := testing.AllocsPerRun(20, func() { _, _ = oneyaml.Marshal(doc) })
	// 100 items, 8 values each: a per-value allocation would give 800 or more.
	if n > 3 {
		t.Errorf("Marshal(any doc) allocates %v times per run", n)
	}
	if n := testing.AllocsPerRun(50, func() { _, _ = oneyaml.Marshal("hello") }); n > 1 {
		t.Errorf("Marshal(string) allocates %v times per run", n)
	}
}

// TestConcurrentMarshal runs under the race detector in CI; it exercises the plan
// cache and the encoder pool from many goroutines.
func TestConcurrentMarshal(t *testing.T) {
	structs := benchStructs()
	doc := benchDoc()
	want1, _ := oneyaml.Marshal(structs)
	want2, _ := oneyaml.Marshal(doc)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				a, err1 := oneyaml.Marshal(structs)
				b, err2 := oneyaml.Marshal(doc)
				if err1 != nil || err2 != nil || string(a) != string(want1) || string(b) != string(want2) {
					t.Error("concurrent Marshal differs")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func FuzzMarshalString(f *testing.F) {
	for _, s := range []string{"", "a", "yes", "a: b", "x\ny\n", "  lead\nz", "\xff", "\u2028", "- x", "1e3", "0x1F", "2001-12-14", "a\tb", "'q'", "---"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, v := range []any{s, []any{s}, map[string]any{s: s}, map[string]string{s: s}, []string{s}} {
			out, err := oneyaml.Marshal(v)
			if err != nil {
				t.Fatalf("Marshal(%q): %v", s, err)
			}
			if len(out) == 0 || out[len(out)-1] != '\n' {
				t.Fatalf("output lacks trailing newline: %q", out)
			}
			if !utf8.Valid(out) {
				t.Fatalf("output is not UTF-8: %q", out)
			}
			again, _ := oneyaml.Marshal(v)
			if string(again) != string(out) {
				t.Fatal("not deterministic")
			}
		}
		// Output never contains a raw carriage return or a control character other
		// than the tab and newline: those are escaped or the string is quoted.
		out, _ := oneyaml.Marshal(s)
		if strings.ContainsAny(string(out), "\r\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0e\x0f\x7f") {
			t.Fatalf("raw control character in %q", out)
		}
	})
}
