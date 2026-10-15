package bench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	goccy "github.com/goccy/go-yaml"
	yamlv3 "go.yaml.in/yaml/v3"
	yamlv2 "gopkg.in/yaml.v2"
	sigs "sigs.k8s.io/yaml"

	oneyaml "oneyaml.dev/go"
)

// Load benchmarks: generic (into any) and typed (into structs), for each library,
// on the kit's real-world documents plus a generated list of records.
// Compare libraries with: benchstat -col /library

type loader struct {
	name string
	fn   func(in []byte, out any) error
	// stream, when set, reads a multi-document stream one document per call.
	stream func(in []byte) func(out any) error
}

var loaders = []loader{
	{"oneyaml", func(in []byte, out any) error { return oneyaml.Unmarshal(in, out) },
		func(in []byte) func(any) error { return oneyaml.NewDecoder(bytes.NewReader(in)).Decode }},
	{"yaml_v2", func(in []byte, out any) error { return yamlv2.Unmarshal(in, out) },
		func(in []byte) func(any) error { return yamlv2.NewDecoder(bytes.NewReader(in)).Decode }},
	{"yaml_v3", func(in []byte, out any) error { return yamlv3.Unmarshal(in, out) },
		func(in []byte) func(any) error { return yamlv3.NewDecoder(bytes.NewReader(in)).Decode }},
	// sigs.k8s.io/yaml has no stream API: it reads the first document only.
	{"sigs", func(in []byte, out any) error { return sigs.Unmarshal(in, out) }, nil},
	{"goccy", func(in []byte, out any) error { return goccy.Unmarshal(in, out) },
		func(in []byte) func(any) error { return goccy.NewDecoder(bytes.NewReader(in)).Decode }},
}

type loadInput struct {
	name string
	data []byte
	docs int // documents in the stream
}

// loadAll decodes every document of a stream with library l.
func (l loader) loadAll(w loadInput) error {
	if w.docs <= 1 || l.stream == nil {
		var v any
		return l.fn(w.data, &v)
	}
	next := l.stream(w.data)
	for {
		var v any
		if err := next(&v); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

var recordWords = strings.Fields("alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega")

// recordsDoc generates a list of records of the shape the kit's records workload
// has, from a fixed random sequence, until the text reaches size bytes.
func recordsDoc(size int) []byte {
	x := uint64(1)
	below := func(n int) int {
		x = x*6364136223846793005 + 1442695040888963407
		return int((x >> 33) % uint64(n))
	}
	word := func() string { return recordWords[below(len(recordWords))] }
	cap1 := func(s string) string { return strings.ToUpper(s[:1]) + s[1:] }
	flt := func(f float64) string {
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	var sb strings.Builder
	for i := 0; sb.Len() < size || i == 0; i++ {
		w1, w2, w3 := word(), word(), word()
		active := below(2) == 0
		score := float64(below(8000))/8 + 0.125
		tags := make([]string, 1+below(3))
		for j := range tags {
			tags[j] = word()
		}
		zip := below(100000)
		lat, lng := float64(below(1440))/8-90, float64(below(2880))/8-180
		noteNone := below(3) != 0
		fmt.Fprintf(&sb, "- id: %d\n  name: %s %s %d\n  email: %s.%s%d@example.com\n  active: %v\n  score: %s\n  tags:\n",
			i, cap1(w1), cap1(w2), i, w1, w2, i, active, flt(score))
		for _, tg := range tags {
			fmt.Fprintf(&sb, "    - %s\n", tg)
		}
		fmt.Fprintf(&sb, "  address:\n    city: %sville\n    zip: '%05d'\n    geo:\n      lat: %s\n      lng: %s\n", cap1(w3), zip, flt(lat), flt(lng))
		if noteNone {
			sb.WriteString("  note: ~\n")
		} else {
			fmt.Fprintf(&sb, "  note: \"seen %s twice\"\n", w3)
		}
	}
	return []byte(sb.String())
}

// workloadDocs reads the document count from the workload.json beside input.
func workloadDocs(input string) int {
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(input), "workload.json"))
	if err != nil {
		return 1
	}
	var w struct {
		Documents int `json:"documents"`
	}
	if json.Unmarshal(raw, &w) != nil || w.Documents < 1 {
		return 1
	}
	return w.Documents
}

func loadInputs(b *testing.B) []loadInput {
	in := []loadInput{{"records-40k", recordsDoc(40 << 10), 1}}
	kit := os.Getenv("ONEYAML_KIT")
	if kit == "" {
		return in
	}
	files, _ := filepath.Glob(filepath.Join(kit, "bench/workloads/real/*/input.yaml"))
	sort.Strings(files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			b.Fatal(err)
		}
		in = append(in, loadInput{filepath.Base(filepath.Dir(f)), raw, workloadDocs(f)})
	}
	return in
}

// BenchmarkLoadGeneric measures decoding into any. Names are
// LoadGeneric/<workload>/library=<library>.
func BenchmarkLoadGeneric(b *testing.B) {
	for _, w := range loadInputs(b) {
		for _, l := range loaders {
			if err := l.loadAll(w); err != nil {
				b.Logf("%s/%s: %v (skipped)", w.name, l.name, err)
				continue
			}
			b.Run(fmt.Sprintf("%s/library=%s", w.name, l.name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(w.data)))
				for b.Loop() {
					if err := l.loadAll(w); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// Typed models, the same as the kit's typed-load task uses.

type Record struct {
	ID      uint64   `yaml:"id" json:"id"`
	Name    string   `yaml:"name" json:"name"`
	Email   string   `yaml:"email" json:"email"`
	Active  bool     `yaml:"active" json:"active"`
	Score   float64  `yaml:"score" json:"score"`
	Tags    []string `yaml:"tags" json:"tags"`
	Address Address  `yaml:"address" json:"address"`
	Note    *string  `yaml:"note" json:"note"`
}

type Address struct {
	City string `yaml:"city" json:"city"`
	Zip  string `yaml:"zip" json:"zip"`
	Geo  *Geo   `yaml:"geo" json:"geo"`
}

type Geo struct {
	Lat float64 `yaml:"lat" json:"lat"`
	Lng float64 `yaml:"lng" json:"lng"`
}

type Workflow struct {
	Name string         `yaml:"name" json:"name"`
	On   On             `yaml:"on" json:"on"`
	Jobs map[string]Job `yaml:"jobs" json:"jobs"`
}

type On struct {
	PullRequest *string `yaml:"pull_request" json:"pull_request"`
	Push        Push    `yaml:"push" json:"push"`
}

type Push struct {
	Branches []string `yaml:"branches" json:"branches"`
}

type Job struct {
	RunsOn    string             `yaml:"runs-on" json:"runs-on"`
	Strategy  *Strategy          `yaml:"strategy" json:"strategy"`
	Container any                `yaml:"container" json:"container"`
	Services  map[string]Service `yaml:"services" json:"services"`
	Env       map[string]string  `yaml:"env" json:"env"`
	Steps     []Step             `yaml:"steps" json:"steps"`
}

type Strategy struct {
	Matrix Matrix `yaml:"matrix" json:"matrix"`
}

type Matrix struct {
	RunsOn []string `yaml:"runs-on" json:"runs-on"`
}

type Service struct {
	Image string   `yaml:"image" json:"image"`
	Ports []string `yaml:"ports" json:"ports"`
}

type Step struct {
	ID        *string        `yaml:"id" json:"id"`
	Name      *string        `yaml:"name" json:"name"`
	Condition *string        `yaml:"if" json:"if"`
	Uses      *string        `yaml:"uses" json:"uses"`
	Run       *string        `yaml:"run" json:"run"`
	Shell     *string        `yaml:"shell" json:"shell"`
	With      map[string]any `yaml:"with" json:"with"`
}

// BenchmarkLoadTyped measures decoding into structs. Names are
// LoadTyped/<workload>/library=<library>.
func BenchmarkLoadTyped(b *testing.B) {
	type model struct {
		name string
		data []byte
		new  func() any
	}
	var models []model
	models = append(models, model{"records-40k", recordsDoc(40 << 10), func() any { return new([]Record) }})
	if kit := os.Getenv("ONEYAML_KIT"); kit != "" {
		raw, err := os.ReadFile(filepath.Join(kit, "bench/workloads/real/gha-workflow/input.yaml"))
		if err != nil {
			b.Fatal(err)
		}
		models = append(models, model{"gha-workflow", raw, func() any { return new(Workflow) }})
	}
	for _, m := range models {
		for _, l := range loaders {
			if err := l.fn(m.data, m.new()); err != nil {
				b.Logf("%s/%s: %v (skipped)", m.name, l.name, err)
				continue
			}
			b.Run(fmt.Sprintf("%s/library=%s", m.name, l.name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(m.data)))
				for b.Loop() {
					if err := l.fn(m.data, m.new()); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// TestLoadDifferential checks that oneyaml and yaml.v3 decode every workload to
// the same generic value, and the same typed value.
func TestLoadDifferential(t *testing.T) {
	ws := []loadInput{{"records", recordsDoc(8 << 10), 1}}
	if kit := os.Getenv("ONEYAML_KIT"); kit != "" {
		files, _ := filepath.Glob(filepath.Join(kit, "bench/workloads/real/*/input.yaml"))
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			ws = append(ws, loadInput{filepath.Base(filepath.Dir(f)), raw, workloadDocs(f)})
		}
	}
	for _, w := range ws {
		if w.name == "records" {
			typedDifferential(t, w)
		}
		var ga, gc any
		if err := oneyaml.Unmarshal(w.data, &ga); err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		if err := yamlv3.Unmarshal(w.data, &gc); err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		if fmt.Sprint(ga) != fmt.Sprint(gc) {
			t.Errorf("%s: generic values differ", w.name)
		}
	}
}

func typedDifferential(t *testing.T, w loadInput) {
	{
		var a, c []Record
		if err := oneyaml.Unmarshal(w.data, &a); err != nil {
			t.Fatal(err)
		}
		if err := yamlv3.Unmarshal(w.data, &c); err != nil {
			t.Fatal(err)
		}
		ja, _ := json.Marshal(a)
		jc, _ := json.Marshal(c)
		if string(ja) != string(jc) {
			t.Errorf("%s: typed values differ", w.name)
		}
	}
}
