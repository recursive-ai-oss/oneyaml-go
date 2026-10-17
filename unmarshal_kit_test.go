package oneyaml_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"oneyaml.dev/go"
	"oneyaml.dev/go/internal/kitcase"
)

// These tests run the shared kit's own schema and safety cases against Unmarshal.
// Point ONEYAML_KIT at a checkout of the kit; the tests skip when it is unset.

type kitCase struct {
	ID        string `json:"id"`
	Operation struct {
		Type    string          `json:"type"`
		Profile string          `json:"profile"`
		Patch   json.RawMessage `json:"patch"`
		// PatchRecipe stands in for Patch when the patch is too large to store.
		PatchRecipe json.RawMessage `json:"patch_recipe"`
		Format      string          `json:"format"`
	} `json:"operation"`
	kitcase.Meta
	Expect struct {
		Kind string `json:"kind"`
		kitcase.Source
		Category any `json:"category"`
		Error    any `json:"error"`
		Accept   *struct {
			Kind string `json:"kind"`
			kitcase.Source
		} `json:"accept"`
		TimeSeconds float64 `json:"time_seconds"`
		MemoryMB    float64 `json:"memory_mb"`
	} `json:"expect"`
	dir string
}

func loadKitCases(t *testing.T, categories ...string) []kitCase {
	root := os.Getenv("ONEYAML_KIT")
	if root == "" {
		t.Skip("ONEYAML_KIT is not set")
	}
	var out []kitCase
	for _, cat := range categories {
		base := filepath.Join(root, "suites", "own", "cases", cat)
		entries, err := os.ReadDir(base)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			dir := filepath.Join(base, e.Name())
			raw, err := os.ReadFile(filepath.Join(dir, "case.json"))
			if err != nil {
				continue
			}
			var c kitCase
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatalf("%s: %v", dir, err)
			}
			c.dir = dir
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func decodeKitValues(raw []byte) ([]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	docs, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected value is not a list of documents")
	}
	return docs, nil
}

// kitCategory maps a returned error to the kit's category names.
func kitCategory(err error) string {
	var se *oneyaml.SyntaxError
	var le *oneyaml.LimitError
	var te *oneyaml.TypeError
	switch {
	case errors.As(err, &se):
		if se.Category == "encoding" || se.Category == "duplicate-key" {
			return string(se.Category)
		}
		return "syntax"
	case errors.As(err, &le):
		return "limit"
	case errors.As(err, &te):
		return "type"
	case errors.Is(err, oneyaml.ErrNotFound):
		return "patch-path"
	case errors.Is(err, oneyaml.ErrTestFailed):
		return "patch-test"
	case errors.Is(err, oneyaml.ErrInvalidPatch):
		return "patch-invalid"
	case errors.Is(err, oneyaml.ErrAliasWrite), errors.Is(err, oneyaml.ErrAnchorInUse):
		return "refused-alias"
	}
	return "other"
}

func categoryAllowed(want any, got string) bool {
	switch w := want.(type) {
	case nil:
		return true
	case string:
		return w == "any" || w == got
	case []any:
		for _, x := range w {
			if x == got {
				return true
			}
		}
	}
	return false
}

// readAll decodes every document of the stream into generic values.
func readAll(in []byte) ([]any, error) {
	dec := oneyaml.NewDecoder(bytes.NewReader(in))
	var docs []any
	for {
		var v any
		err := dec.Decode(&v)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return docs, nil
			}
			return docs, err
		}
		docs = append(docs, v)
	}
}

// goLimits are cases whose expected value has no Go form: a collection as a
// mapping key cannot be a Go map key. They must fail with a *TypeError, which
// is the whole of the deviation.
var goLimits = map[string]string{
	"schema/mapping-as-a-mapping-key":  "Go map keys must be comparable",
	"schema/sequence-as-a-mapping-key": "Go map keys must be comparable",
}

func TestKitSchemaAndSafety(t *testing.T) {
	cases := loadKitCases(t, "parse", "schema", "safety")
	var pass, fail, skipped, deviations int
	var notes []string
	for _, c := range cases {
		if c.Operation.Profile != "" && c.Operation.Profile != "core" {
			skipped++
			continue
		}
		if c.Operation.Type == "patch" {
			skipped++
			continue
		}
		if c.Skip() {
			skipped++
			continue
		}
		in, err := c.Bytes(c.dir)
		if err != nil {
			t.Fatal(err)
		}
		var ms0, ms1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&ms0)
		docs, gotErr := readAll(in)
		runtime.ReadMemStats(&ms1)
		allocMB := float64(ms1.TotalAlloc-ms0.TotalAlloc) / (1 << 20)

		problem := ""
		if _, ok := goLimits[c.ID]; ok {
			if kitCategory(gotErr) != "type" {
				fail++
				notes = append(notes, c.ID+": want a TypeError, got "+fmt.Sprint(gotErr))
			} else {
				deviations++
			}
			continue
		}
		switch c.Expect.Kind {
		case "value":
			if c.Operation.Type == "events" {
				skipped++
				continue
			}
			problem = checkKitValue(c.dir, c.Expect.Source, docs, gotErr)
		case "error":
			switch {
			case gotErr == nil:
				problem = "no error"
			case !categoryAllowed(c.Expect.Category, kitCategory(gotErr)):
				problem = fmt.Sprintf("category %s, want %v (%v)", kitCategory(gotErr), c.Expect.Category, gotErr)
			}
		case "bounded":
			events := c.Operation.Type == "events"
			// The time limit is a hang guard, not a speed check. The memory bound is
			// on peak resident memory, so it is measured on a child run of the read.
			child := kitRunChild(t, c.ID, kitReadAllPath)
			switch {
			case child.Wall.Seconds() > c.Expect.TimeSeconds:
				problem = fmt.Sprintf("took %v, hang guard %vs", child.Wall, c.Expect.TimeSeconds)
			case child.OK && child.PeakMB > c.Expect.MemoryMB:
				problem = fmt.Sprintf("peak memory %.0f MB, bound %v MB", child.PeakMB, c.Expect.MemoryMB)
			case !child.OK && !c.Slow() && allocMB > c.Expect.MemoryMB:
				// No peak RSS on this OS: allocation is an upper bound, fair for small cases.
				problem = fmt.Sprintf("allocated %.0f MB, bound %v MB (no peak RSS on this OS)", allocMB, c.Expect.MemoryMB)
			case events:
				// An events case allows any outcome that stays inside the bounds: the
				// generic value is not an event stream, so only cost is checked.
			case gotErr != nil:
				if !categoryAllowed(c.Expect.Error, kitCategory(gotErr)) || c.Expect.Error == nil {
					problem = fmt.Sprintf("error %s, allowed %v (%v)", kitCategory(gotErr), c.Expect.Error, gotErr)
				}
			case c.Expect.Accept == nil:
				problem = "answered, but only an error is allowed"
			default:
				problem = checkKitValue(c.dir, c.Expect.Accept.Source, docs, nil)
			}
		default:
			skipped++
			continue
		}
		if problem == "" {
			pass++
			continue
		}
		fail++
		notes = append(notes, fmt.Sprintf("%s: %s", c.ID, problem))
	}
	t.Logf("kit cases: %d pass, %d fail, %d Go-form deviations, %d skipped (patch, other profiles)", pass, fail, deviations, skipped)
	for _, n := range notes {
		t.Error(n)
	}
}

func checkKitValue(dir string, src kitcase.Source, got []any, gotErr error) string {
	if gotErr != nil {
		return "error: " + gotErr.Error()
	}
	raw, err := src.Read(dir, true)
	if err != nil {
		return "bad expectation: " + err.Error()
	}
	want, err := decodeKitValues(raw)
	if err != nil {
		return "bad expectation: " + err.Error()
	}
	if len(got) != len(want) {
		return fmt.Sprintf("%d documents, want %d", len(got), len(want))
	}
	for i := range got {
		if d := compareValueMode(got[i], want[i], true); d != "" {
			return fmt.Sprintf("document %d: %s", i, d)
		}
	}
	return ""
}
