package oneyaml_test

import (
	"bytes"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"oneyaml.dev/go"
)

type fuzzTarget struct {
	Name   string            `yaml:"name"`
	N      int8              `yaml:"n"`
	U      uint              `yaml:"u"`
	F      float32           `yaml:"f"`
	B      bool              `yaml:"b"`
	Tags   []string          `yaml:"tags"`
	Arr    [2]int            `yaml:"arr"`
	M      map[string]any    `yaml:"m"`
	IM     map[int]string    `yaml:"im"`
	P      *fuzzTarget       `yaml:"p"`
	T      time.Time         `yaml:"t"`
	D      time.Duration     `yaml:"d"`
	Big    *big.Int          `yaml:"big"`
	Data   []byte            `yaml:"data"`
	Any    any               `yaml:"any"`
	Inline map[string]string `yaml:",inline"`
}

// FuzzUnmarshal checks that no input panics, that limits end hostile input, and
// that the Decoder reads the first document the way Unmarshal does.
func FuzzUnmarshal(f *testing.F) {
	for _, s := range []string{
		"", "a: 1", "- [1, 2]\n- {a: b}", "a: &x [1]\nb: *x", "a: &a [x]\nb: &b [*a, *a]\nc: [*b, *b]",
		"--- a\n--- b\n...", "<<: {a: 1}\nb: 2", "? [a]\n: b", "!!int abc", "t: 2001-12-14t21:59:43.10-05:00",
		"\xff\xfea\x00:\x00 \x001\x00", "a: |\n  x\n  y\n", "\"\\u00e9\\x41\"", "d: 1h2m\nbig: 123456789012345678901234567890",
		"p: {p: {p: {name: x}}}", "name: [", "{a: 1, a: 2}", "%YAML 1.2\n---\nx",
	} {
		f.Add([]byte(s))
	}
	if kit := os.Getenv("ONEYAML_KIT"); kit != "" {
		files, _ := filepath.Glob(filepath.Join(kit, "suites/own/cases/schema/*/input.yaml"))
		for _, p := range files {
			if raw, err := os.ReadFile(p); err == nil && len(raw) < 4096 {
				f.Add(raw)
			}
		}
	}
	opts := [][]oneyaml.Option{
		nil,
		{oneyaml.KnownFields(), oneyaml.MergeKeys()},
		{oneyaml.Untrusted()},
		{oneyaml.Limits(oneyaml.LimitSet{MaxDepth: 20, MaxAliasNodes: 50, MaxAliasBytes: 500})},
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, o := range opts {
			var v any
			err := oneyaml.Unmarshal(data, &v, o...)
			var tgt fuzzTarget
			_ = oneyaml.Unmarshal(data, &tgt, o...)
			var seq []fuzzTarget
			_ = oneyaml.Unmarshal(data, &seq, o...)
			if err == nil && len(o) == 0 {
				var w any
				if derr := oneyaml.NewDecoder(bytes.NewReader(data)).Decode(&w); derr != nil && fmt.Sprint(derr) != "EOF" {
					t.Fatalf("Unmarshal accepted but Decoder failed: %v", derr)
				} else if derr == nil && fmt.Sprintf("%#v", v) != fmt.Sprintf("%#v", w) {
					t.Fatalf("Unmarshal and Decoder disagree:\n%#v\n%#v", v, w)
				}
			}
		}
	})
}
