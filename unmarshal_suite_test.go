package oneyaml_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"oneyaml.dev/go"
)

// The yaml-test-suite is not vendored here. Point ONEYAML_YTS_DIR at a checkout
// of its data branch (one directory per case, or per sub-case). The optional
// ONEYAML_YTS_CORRECTIONS names a corrections file with expected values for
// cases where the suite's JSON is wrong; by default the file next to the data
// directory is used when it exists.

type valueCase struct {
	id      string
	in      []byte
	want    []any // expected documents
	isError bool  // the suite marks the input as invalid
}

func loadValueCases(t testing.TB) []valueCase {
	root := os.Getenv("ONEYAML_YTS_DIR")
	if root == "" {
		t.Skip("ONEYAML_YTS_DIR is not set")
	}
	corr := map[string][]any{}
	path := os.Getenv("ONEYAML_YTS_CORRECTIONS")
	if path == "" {
		path = filepath.Join(root, "..", "corrections.json")
	}
	if raw, err := os.ReadFile(path); err == nil {
		var doc struct {
			Entries []struct {
				Case  string `json:"case"`
				Value []any  `json:"value"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		for _, e := range doc.Entries {
			if strings.HasSuffix(e.Case, "@value") {
				corr[strings.TrimSuffix(strings.TrimPrefix(e.Case, "yts:"), "@value")] = e.Value
			}
		}
	}
	var cases []valueCase
	add := func(id, dir string) {
		in, err := os.ReadFile(filepath.Join(dir, "in.yaml"))
		if err != nil {
			return
		}
		if _, err := os.Stat(filepath.Join(dir, "error")); err == nil {
			cases = append(cases, valueCase{id: id, in: in, isError: true})
			return
		}
		raw, err := os.ReadFile(filepath.Join(dir, "in.json"))
		if err != nil {
			return
		}
		var docs []any
		if c, ok := corr[id]; ok {
			docs = c
		} else {
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			for {
				var v any
				if err := dec.Decode(&v); err == io.EOF {
					break
				} else if err != nil {
					t.Fatalf("%s: in.json: %v", id, err)
				}
				docs = append(docs, v)
			}
		}
		cases = append(cases, valueCase{id: id, in: in, want: docs})
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "in.yaml")); err == nil {
			add(e.Name(), dir)
			continue
		}
		subs, _ := os.ReadDir(dir)
		for _, s := range subs {
			if s.IsDir() {
				add(e.Name()+"/"+s.Name(), filepath.Join(dir, s.Name()))
			}
		}
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].id < cases[j].id })
	return cases
}

// compareValue compares a decoded value with an expected one in the kit's JSON
// value form, with these named projections: numbers compare by value (the
// suite's converter does not tell 450 from 450.00), tags are ignored, and a
// mapping with non-string keys compares by the text of its keys.
func compareValue(got, want any) string { return compareValueMode(got, want, false) }

// compareValueMode is compareValue; strict also separates integers from floats,
// and matches mappings with non-string keys by the kit's rules.
func compareValueMode(got, want any, strict bool) string {
	switch w := want.(type) {
	case nil:
		if got != nil {
			return fmt.Sprintf("want null, got %#v", got)
		}
	case bool:
		if g, ok := got.(bool); !ok || g != w {
			return fmt.Sprintf("want %v, got %#v", w, got)
		}
	case string:
		if g, ok := got.(string); !ok || g != w {
			return fmt.Sprintf("want %q, got %#v", w, got)
		}
	case json.Number:
		return compareNumber(got, w.String(), strict)
	case float64:
		return compareNumber(got, fmt.Sprint(w), strict)
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Sprintf("want sequence of %d, got %#v", len(w), got)
		}
		for i := range w {
			if d := compareValueMode(g[i], w[i], strict); d != "" {
				return fmt.Sprintf("[%d]: %s", i, d)
			}
		}
	case map[string]any:
		if len(w) == 1 {
			if f, ok := w["$float"]; ok {
				g, isF := got.(float64)
				switch f {
				case "inf":
					if !isF || !math.IsInf(g, 1) {
						return fmt.Sprintf("want +Inf, got %#v", got)
					}
				case "-inf":
					if !isF || !math.IsInf(g, -1) {
						return fmt.Sprintf("want -Inf, got %#v", got)
					}
				default:
					if !isF || !math.IsNaN(g) {
						return fmt.Sprintf("want NaN, got %#v", got)
					}
				}
				return ""
			}
			if tg, ok := w["$tagged"].(map[string]any); ok {
				return compareValueMode(got, tg["value"], strict)
			}
			if pairs, ok := w["$map"].([]any); ok && strict {
				return compareKitMap(got, pairs)
			}
			if pairs, ok := w["$map"].([]any); ok {
				m := map[string]any{}
				for _, p := range pairs {
					kv := p.([]any)
					m[fmt.Sprint(kv[0])] = kv[1]
				}
				return compareValue(got, m)
			}
		}
		gm := map[string]any{}
		switch g := got.(type) {
		case map[string]any:
			gm = g
		case map[any]any:
			for k, v := range g {
				ks, isStr := k.(string)
				if strict && !isStr {
					return fmt.Sprintf("non-string key %#v in an object", k)
				}
				if !isStr {
					ks = fmt.Sprint(k)
				}
				gm[ks] = v
			}
		default:
			return fmt.Sprintf("want mapping, got %#v", got)
		}
		if len(gm) != len(w) {
			return fmt.Sprintf("want %d keys, got %d", len(w), len(gm))
		}
		for k, wv := range w {
			gv, ok := gm[k]
			if !ok {
				return fmt.Sprintf("missing key %q", k)
			}
			if d := compareValueMode(gv, wv, strict); d != "" {
				return fmt.Sprintf("%q: %s", k, d)
			}
		}
	default:
		return fmt.Sprintf("unsupported expectation %T", want)
	}
	return ""
}

func compareKitMap(got any, pairs []any) string {
	entries := map[any]any{}
	switch g := got.(type) {
	case map[string]any:
		for k, v := range g {
			entries[k] = v
		}
	case map[any]any:
		entries = g
	default:
		return fmt.Sprintf("want mapping, got %#v", got)
	}
	if len(entries) != len(pairs) {
		return fmt.Sprintf("want %d keys, got %d", len(pairs), len(entries))
	}
	for _, p := range pairs {
		kv := p.([]any)
		found := false
		for k, v := range entries {
			if compareValueMode(k, kv[0], true) == "" {
				if d := compareValueMode(v, kv[1], true); d != "" {
					return fmt.Sprintf("%v: %s", kv[0], d)
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Sprintf("missing key %v", kv[0])
		}
	}
	return ""
}

func compareNumber(got any, want string, strict bool) string {
	if strict {
		isFloat := strings.ContainsAny(want, ".eE")
		_, gotFloat := got.(float64)
		if isFloat != gotFloat {
			return fmt.Sprintf("want %s, got %#v (integer and float differ)", want, got)
		}
	}
	wr, ok := new(big.Rat).SetString(want)
	if !ok {
		return "bad expected number " + want
	}
	var gr *big.Rat
	switch g := got.(type) {
	case int:
		gr = new(big.Rat).SetInt64(int64(g))
	case int64:
		gr = new(big.Rat).SetInt64(g)
	case uint64:
		gr = new(big.Rat).SetUint64(g)
	case *big.Int:
		gr = new(big.Rat).SetInt(g)
	case float64:
		gr = new(big.Rat)
		if gr.SetFloat64(g) == nil {
			return fmt.Sprintf("want %s, got %v", want, g)
		}
	default:
		return fmt.Sprintf("want number %s, got %#v", want, got)
	}
	if gr.Cmp(wr) != 0 {
		// Floats parse to the nearest double; compare through float64 as well.
		wf, _ := wr.Float64()
		if g, ok := got.(float64); ok && g == wf {
			return ""
		}
		return fmt.Sprintf("want %s, got %v", want, got)
	}
	return ""
}

func TestSuiteValues(t *testing.T) {
	cases := loadValueCases(t)
	pass := 0
	var fails []string
	for _, c := range cases {
		var got []any
		dec := oneyaml.NewDecoder(bytes.NewReader(c.in))
		var err error
		for {
			var v any
			if err = dec.Decode(&v); err != nil {
				break
			}
			got = append(got, v)
		}
		if c.isError {
			if err == io.EOF {
				fails = append(fails, c.id+": invalid input was accepted")
			} else {
				pass++
			}
			continue
		}
		if err != io.EOF {
			fails = append(fails, fmt.Sprintf("%s: error %v", c.id, err))
			continue
		}
		if len(got) != len(c.want) {
			fails = append(fails, fmt.Sprintf("%s: %d documents, want %d", c.id, len(got), len(c.want)))
			continue
		}
		bad := ""
		for i := range got {
			if d := compareValue(got[i], c.want[i]); d != "" {
				bad = fmt.Sprintf("%s: document %d: %s", c.id, i, d)
				break
			}
		}
		if bad == "" && len(got) > 0 {
			// Unmarshal reads the first document of the same stream the same way.
			var first any
			if err := oneyaml.Unmarshal(c.in, &first); err != nil {
				bad = fmt.Sprintf("%s: Unmarshal: %v", c.id, err)
			} else if d := compareValue(first, c.want[0]); d != "" {
				bad = fmt.Sprintf("%s: Unmarshal: %s", c.id, d)
			}
		}
		if bad != "" {
			fails = append(fails, bad)
			continue
		}
		pass++
	}
	t.Logf("value cases: %d pass, %d fail, %d total", pass, len(fails), len(cases))
	for _, f := range fails {
		t.Error(f)
	}
}
