package bench

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/palantir/pkg/yamlpatch"
	"github.com/palantir/pkg/yamlpatch/goccyyamlpatcher"
	sigs "sigs.k8s.io/yaml"

	oneyaml "oneyaml.dev/go"
)

// Patch benchmarks: apply an RFC 6902 patch to a text and write the text back.
// Every competitor takes text and returns text, so parsing and writing are inside
// the timed work for all of them; oneyaml also has a variant that holds the parsed
// Document and applies the patch to it, which is what a program that edits the same
// file repeatedly does.
//
// Workloads are the kit's patch workloads, written by the kit's generator into the
// directory named by ONEYAML_WORKLOADS (its "workloads" folder).

type patchWorkload struct {
	name     string
	input    []byte
	ops      json.RawMessage // the patch as JSON text
	failing  json.RawMessage
	expected []byte
	list     []patchJSONOp
}

type patchJSONOp struct {
	Op       string          `json:"op"`
	Path     string          `json:"path"`
	From     string          `json:"from"`
	Value    json.RawMessage `json:"value"`
	HasValue bool            `json:"-"`
}

func loadPatchWorkloads(tb testing.TB) []patchWorkload {
	dir := os.Getenv("ONEYAML_WORKLOADS")
	if dir == "" {
		tb.Skip("ONEYAML_WORKLOADS is not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*", "patch.json"))
	var out []patchWorkload
	for _, f := range files {
		d := filepath.Dir(f)
		var p struct {
			Ops     json.RawMessage `json:"ops"`
			Failing json.RawMessage `json:"failing_ops"`
		}
		raw, _ := os.ReadFile(f)
		if err := json.Unmarshal(raw, &p); err != nil {
			tb.Fatal(err)
		}
		in, _ := os.ReadFile(filepath.Join(d, "input.yaml"))
		exp, _ := os.ReadFile(filepath.Join(d, "patch.expected.yaml"))
		w := patchWorkload{name: filepath.Base(d), input: in, ops: p.Ops, failing: p.Failing, expected: exp}
		if err := json.Unmarshal(p.Ops, &w.list); err != nil {
			tb.Fatal(err)
		}
		var generic []map[string]json.RawMessage
		_ = json.Unmarshal(p.Ops, &generic)
		for i := range w.list {
			_, w.list[i].HasValue = generic[i]["value"]
		}
		out = append(out, w)
	}
	return out
}

func (w *patchWorkload) palantir() yamlpatch.Patch {
	var p yamlpatch.Patch
	for _, o := range w.list {
		op := yamlpatch.Operation{Type: o.Op}
		var err error
		if op.Path, err = yamlpatch.ParsePath(o.Path); err != nil {
			panic(err)
		}
		if o.HasValue {
			if err := json.Unmarshal(o.Value, &op.Value); err != nil {
				panic(err)
			}
		}
		p = append(p, op)
	}
	return p
}

type patcher struct {
	name  string
	apply func(w *patchWorkload) func([]byte) ([]byte, error)
	// valueOnly libraries cannot keep the text, so only the value is compared.
	valueOnly bool
}

func patchers() []patcher {
	return []patcher{
		{name: "oneyaml", apply: func(w *patchWorkload) func([]byte) ([]byte, error) {
			ops := []byte(w.ops)
			return func(in []byte) ([]byte, error) {
				d, err := oneyaml.Parse(in)
				if err != nil {
					return nil, err
				}
				if err := d.ApplyPatch(ops); err != nil {
					return nil, err
				}
				return d.Bytes(), nil
			}
		}},
		{name: "palantir-yaml.v3", apply: func(w *patchWorkload) func([]byte) ([]byte, error) {
			p := w.palantir()
			return func(in []byte) ([]byte, error) { return yamlpatch.Apply(in, p) }
		}},
		{name: "palantir-goccy", apply: func(w *patchWorkload) func([]byte) ([]byte, error) {
			p := w.palantir()
			return func(in []byte) ([]byte, error) { return goccyyamlpatcher.New().Apply(in, p) }
		}},
		{name: "json-patch", valueOnly: true, apply: func(w *patchWorkload) func([]byte) ([]byte, error) {
			p, err := jsonpatch.DecodePatch(w.ops)
			if err != nil {
				panic(err)
			}
			return func(in []byte) ([]byte, error) {
				doc, err := sigs.YAMLToJSON(in)
				if err != nil {
					return nil, err
				}
				out, err := p.Apply(doc)
				if err != nil {
					return nil, err
				}
				return sigs.JSONToYAML(out)
			}
		}},
	}
}

// TestPatchWorkloads checks each library's output against the kit's expected bytes
// (or, for the value-only baseline, its value) and reports who gets it right.
func TestPatchWorkloads(t *testing.T) {
	for _, w := range loadPatchWorkloads(t) {
		for _, p := range patchers() {
			out, err := p.apply(&w)(w.input)
			switch {
			case err != nil:
				t.Logf("%s/%s: error: %v", w.name, p.name, err)
			case p.valueOnly:
				var a, b any
				_ = sigs.Unmarshal(out, &a)
				_ = sigs.Unmarshal(w.expected, &b)
				ja, _ := json.Marshal(a)
				jb, _ := json.Marshal(b)
				t.Logf("%s/%s: value equal: %v", w.name, p.name, bytes.Equal(ja, jb))
			default:
				t.Logf("%s/%s: bytes equal: %v", w.name, p.name, bytes.Equal(out, w.expected))
				if p.name == "oneyaml" && !bytes.Equal(out, w.expected) {
					t.Errorf("%s: oneyaml output differs from the expected bytes:\n%s", w.name, out)
				}
			}
		}
		// A patch with a failing step must fail and leave a held document alone.
		d, err := oneyaml.Parse(w.input)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.ApplyPatch(w.failing); err == nil || !bytes.Equal(d.Bytes(), w.input) {
			t.Errorf("%s: failing patch: err %v, unchanged %v", w.name, err, bytes.Equal(d.Bytes(), w.input))
		}
	}
}

func BenchmarkPatch(b *testing.B) {
	for _, w := range loadPatchWorkloads(b) {
		for _, p := range patchers() {
			b.Run(w.name+"/"+p.name, func(b *testing.B) {
				run := p.apply(&w)
				if _, err := run(w.input); err != nil {
					b.Skipf("the library fails on this patch: %v", err)
				}
				b.ReportAllocs()
				b.SetBytes(int64(len(w.input)))
				for b.Loop() {
					if _, err := run(w.input); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
		// A held Document: the patch is applied to a parsed document and rolled back
		// by parsing again outside the timer is not possible, so the patch is made
		// repeatable: it is applied to a fresh copy built from the same parse.
		b.Run(w.name+"/oneyaml-doc", func(b *testing.B) {
			ops := []byte(w.ops)
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				d, err := oneyaml.Parse(w.input)
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				if err := d.ApplyPatch(ops); err != nil {
					b.Fatal(err)
				}
				_ = d.Bytes()
			}
		})
	}
}
