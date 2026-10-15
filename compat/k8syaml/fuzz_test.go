package yaml

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var fuzzSeeds = []string{
	"", "a: 1\n", "a: &x [1, 2]\nb: *x\n", "- 1\n- yes\n- ~\n", "{a: 1, b: [x, y]}", "a: !!binary aGVsbG8=\n",
	"base: &b {a: 1}\nd:\n  <<: *b\n  a: 2\n", "1: a\ntrue: b\n", "a: |\n  x\n  y\n", "? [1]\n: x\n", "a: 1\n---\nb: 2\n",
	"a: \"\\u2028\"\n", "\xef\xbb\xbfa: 1\n", "x: .nan\n", "a: 0x1F\nb: 0755\nc: 1_0\n", "[[[[[[[[[[1]]]]]]]]]]",
	"!00 |\n\n0", "- |2-\n\n    x\n", `{"a":[1,2,{"b":null}],"c":"d\ne"}`, `"` + strings.Repeat("word ", 40) + `"`,
}

func addSeeds(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add([]byte(s))
	}
	// The kit's cases, when a checkout is named.
	if kit := os.Getenv("ONEYAML_KIT"); kit != "" {
		n := 0
		_ = filepath.WalkDir(filepath.Join(kit, "suites"), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".yaml") && n < 600 {
				if b, err := os.ReadFile(p); err == nil && len(b) < 4096 {
					f.Add(b)
					n++
				}
			}
			return nil
		})
	}
}

// YAMLToJSON never panics, and what it returns is JSON that Unmarshal reads as
// the same value.
func FuzzYAMLToJSON(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, in []byte) {
		j, err := YAMLToJSON(in)
		if err != nil {
			return
		}
		if !json.Valid(j) {
			t.Fatalf("invalid JSON %q from %q", j, in)
		}
		var a, b any
		if err := Unmarshal(in, &a); err != nil {
			t.Fatalf("Unmarshal failed where YAMLToJSON did not: %v", err)
		}
		if err := json.Unmarshal(j, &b); err != nil {
			t.Fatal(err)
		}
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if !bytes.Equal(ja, jb) {
			t.Fatalf("Unmarshal and YAMLToJSON disagree on %q", in)
		}
		_, _ = YAMLToJSONStrict(in)
		var s struct {
			A string
			B []string
			C map[string]string
		}
		_ = Unmarshal(in, &s)
	})
}

// JSONToYAML never panics, and its output reads back as the value it was given.
func FuzzJSONToYAML(f *testing.F) {
	addSeeds(f)
	f.Fuzz(func(t *testing.T, in []byte) {
		y, err := JSONToYAML(in)
		if err != nil {
			return
		}
		want, werr := YAMLToJSON(in)
		if werr != nil {
			return
		}
		got, err := YAMLToJSON(y)
		if err != nil {
			t.Fatalf("output does not read back: %v\n in: %q\nout: %q", err, in, y)
		}
		if !bytes.Equal(got, want) {
			// Non-string keys in non-JSON input stay strings; compare the values.
			var a, b any
			if json.Unmarshal(got, &a) != nil || json.Unmarshal(want, &b) != nil {
				t.Fatalf("not JSON: %q %q", got, want)
			}
			ja, _ := json.Marshal(dropZeroSign(a))
			jb, _ := json.Marshal(dropZeroSign(b))
			if !bytes.Equal(ja, jb) {
				t.Fatalf("round trip changed the value\n in: %q\nout: %q\n got: %q\nwant: %q", in, y, got, want)
			}
		}
	})
}

// dropZeroSign turns every negative zero into zero. A float -0 is written as -0,
// which a YAML 1.1 reader (sigs.k8s.io/yaml included) reads back as the integer
// 0, so the sign cannot survive a round trip through YAML.
func dropZeroSign(v any) any {
	switch x := v.(type) {
	case float64:
		if x == 0 {
			return float64(0)
		}
	case []any:
		for i := range x {
			x[i] = dropZeroSign(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = dropZeroSign(x[k])
		}
	}
	return v
}
