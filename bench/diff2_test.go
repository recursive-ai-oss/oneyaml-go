package bench

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
)

type flowDoc struct {
	L []string          `yaml:"l,flow"`
	M map[string]string `yaml:"m,flow"`
	N []any             `yaml:"n,flow"`
}

// TestDifferentialFlow checks strings inside flow collections, where plain
// scalars may not contain commas or brackets.
func TestDifferentialFlow(t *testing.T) {
	strs := append([]string{}, hazards...)
	for _, a := range []string{"a", ",", "[", "]", "{", "}", ":", " ", "#", "1", "-", "?"} {
		for _, b := range []string{"a", ",", "[", "]", "{", "}", ":", " ", "#", "1"} {
			strs = append(strs, a+b, a+b+"x", "x"+a+b)
		}
	}
	for _, s := range strs {
		out, err := oneyaml.Marshal(flowDoc{L: []string{s, "z"}, M: map[string]string{s: s, "k": "v"}, N: []any{s, 1, nil, map[string]any{"q": s}}})
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"l": []any{s, "z"},
			"m": map[string]any{s: s, "k": "v"},
			"n": []any{s, 1, nil, map[string]any{"q": s}},
		}
		for _, rd := range readers {
			got, err := rd.read(out)
			if err != nil {
				t.Errorf("flow(%q): %s cannot read %q: %v", s, rd.name, trunc(out), err)
				continue
			}
			if !sameValue(want, got) {
				t.Errorf("flow(%q): %s reads %q as %#v", s, rd.name, trunc(out), got)
			}
		}
	}
}

// TestDifferentialRandomStrings writes random strings of up to 14 pieces from the
// same kind of alphabet as the exhaustive test, with a fixed seed.
func TestDifferentialRandomStrings(t *testing.T) {
	if testing.Short() {
		t.Skip("long; skipped in short mode")
	}
	alphabet := []string{"a", "b", "o", "1", "0", "2", " ", " ", "-", "?", ":", ",", "[", "]", "{", "}", "#", "&", "*", "!", "|", ">", "'", "\"", "%", "@", ".", "e", "x", "_", "\n", "\n", "\t", "+", "<", "=", "é", "\r", "y", "n", "null", "true", "0x", "T", ": ", " #", "---"}
	seed, count := uint64(1), 60000
	if v, err := strconv.Atoi(os.Getenv("ONEYAML_RANDOM_COUNT")); err == nil {
		count = v
	}
	if v, err := strconv.Atoi(os.Getenv("ONEYAML_RANDOM_SEED")); err == nil {
		seed = uint64(v)
	}
	rng := rand.New(rand.NewPCG(seed, 2))
	for i := 0; i < count; i++ {
		n := 2 + rng.IntN(12)
		var sb strings.Builder
		for j := 0; j < n; j++ {
			sb.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		checkStringOnce(t, sb.String())
		if t.Failed() {
			break
		}
	}
}

// signedPrefix lists text that strconv-based readers take for an integer even
// though the schemas do not: a sign after the radix prefix, and sign-less
// exponents.
var signedPrefix = []string{
	"0b-1", "0b+1", "0o-7", "0o+7", "0x-1", "0x+F", "0b-1_0", "-0b-1", "+0o+7", "0B-1", "0O+7", "0X-f",
	"+e5", "-E1", "e5", "+.e5", "-.E1", "+e", "+e+5", "-e-5", "0e5", "+0e5", ".5e5",
}

// unicodeSpaces lists characters that look like blanks but are not YAML white
// space, at the edges and inside strings.
var unicodeSpaces = []string{
	"\u00a0", "\u1680", "\u2000", "\u2003", "\u200b", "\u202f", "\u205f", "\u3000", "\ufeff", "\u2028", "\u2029", "\u0085",
	"a\u00a0", "\u00a0a", "a\u2003b", "\u3000x", "x\u3000", "a\u00a0: b", "\u00a0#", "a \u00a0#b",
}

// invalidUTF8 lists strings that are not valid UTF-8.
var invalidUTF8 = []string{"\xff", "a\xffb", "\xc0\xaf", "\xed\xa0\x80", "\xf4\x90\x80\x80", "ok\xe2\x82"}

func TestDifferentialHazardousStrings(t *testing.T) {
	for _, list := range [][]string{signedPrefix, unicodeSpaces, invalidUTF8} {
		for _, s := range list {
			checkString(t, s)
		}
	}
}

// TestDifferentialKeyBoundary writes keys whose raw or written length is near
// the 1024-character limit for implicit keys, with and without quoting and
// escaping, so the explicit "? " form is chosen on the written length.
func TestDifferentialKeyBoundary(t *testing.T) {
	var keys []string
	for n := 1018; n <= 1030; n++ {
		keys = append(keys,
			strings.Repeat("a", n),           // plain
			" "+strings.Repeat("a", n-1),     // single-quoted: 2 more characters
			strings.Repeat("\u00e9", n),      // multi-byte: characters, not bytes
			strings.Repeat("\u65e5", n)+"\n", // double-quoted
		)
	}
	for n := 505; n <= 515; n++ {
		keys = append(keys, strings.Repeat("'", n)) // single-quoted, each quote doubled
	}
	for n := 250; n <= 260; n++ {
		keys = append(keys, strings.Repeat("\x01", n)) // double-quoted, 4 characters each
	}
	for _, k := range keys {
		for _, v := range []any{
			map[string]any{k: "v", "zz": 1},
			map[string]any{"a": []any{map[string]any{k: nil}}},
		} {
			check(t, fmt.Sprintf("key len %d", len(k)), v)
		}
	}
}
