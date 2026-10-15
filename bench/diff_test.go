package bench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	goccy "github.com/goccy/go-yaml"
	yamlv3 "go.yaml.in/yaml/v3"
	oneyaml "oneyaml.dev/go"
)

// The differential test writes values with oneyaml.Marshal and reads the text
// back with two independent readers, go.yaml.in/yaml/v3 and goccy/go-yaml. Both
// must return the value that was written. That is the practical form of the rule
// that plain scalars are only used when every common reader agrees on them.

// reader is one YAML reader under test.
type reader struct {
	name string
	read func([]byte) (any, error)
}

var readers = []reader{
	{"own", ownRead},
	{"yaml.v3", func(b []byte) (any, error) {
		var v any
		err := yamlv3.Unmarshal(b, &v)
		return v, err
	}},
	{"goccy", func(b []byte) (any, error) {
		var v any
		err := goccy.Unmarshal(b, &v)
		return v, err
	}},
}

// exception names a case where a reader is wrong and the reason, so the list of
// accepted disagreements stays short and visible.
type exception struct {
	reader string
	match  func(in any) bool
	reason string
}

// sameTextKeys reports a map holding a string key and a non-string key with the
// same text, such as "1" and 1, which both readers refuse as a duplicate.
func sameTextKeys(in any) bool {
	m, ok := in.(map[any]any)
	if !ok {
		return false
	}
	seen := map[string]bool{}
	for k := range m {
		if _, isStr := k.(string); !isStr {
			seen[fmt.Sprint(k)] = true
		}
	}
	for k := range m {
		if s, isStr := k.(string); isStr && seen[s] {
			return true
		}
	}
	return false
}

var exceptions = []exception{
	{"yaml.v3", sameTextKeys, "yaml.v3 reports a string key and an int key with the same text as duplicates"},
	{"goccy", sameTextKeys, "goccy turns every key into a string, so \"1\" and 1 collide"},
}

func isException(rd string, in any) (string, bool) {
	for _, ex := range exceptions {
		if ex.reader == rd && ex.match(in) {
			return ex.reason, true
		}
	}
	return "", false
}

// norm converts a value into a canonical form so values from different readers
// compare: integers become int64 (or a decimal string when wider), maps with only
// string keys become map[string]any, NaN becomes a marker string.
func norm(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case int:
		return int64(x)
	case int8, int16, int32:
		return reflect.ValueOf(x).Int()
	case uint:
		return normU(uint64(x))
	case uint8, uint16, uint32:
		return int64(reflect.ValueOf(x).Uint())
	case uint64:
		return normU(x)
	case float32:
		return norm(float64(x))
	case float64:
		if math.IsNaN(x) {
			return "\x00NaN"
		}
		return x
	case *big.Int:
		if x.IsInt64() {
			return x.Int64()
		}
		return bigText{x.String()}
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = norm(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = norm(e)
		}
		return out
	case map[any]any:
		allStr := true
		for k := range x {
			if _, ok := k.(string); !ok {
				allStr = false
			}
		}
		if allStr {
			out := make(map[string]any, len(x))
			for k, e := range x {
				out[k.(string)] = norm(e)
			}
			return out
		}
		out := make(map[any]any, len(x))
		for k, e := range x {
			out[norm(k)] = norm(e)
		}
		return out
	}
	return v
}

type bigText struct{ s string }

func normU(u uint64) any {
	if u > math.MaxInt64 {
		return bigText{fmt.Sprint(u)}
	}
	return int64(u)
}

// readerValue is the reader's answer made comparable to want.
func sameValue(want, got any) bool {
	return reflect.DeepEqual(norm(want), norm(got))
}

// isWide reports whether n needs more than 64 bits.
func isWide(n *big.Int) bool { return !n.IsInt64() && !n.IsUint64() }

// readerView rewrites integers wider than 64 bits into what each reader is known
// to return for them: no Go reader has an integer type for them, so yaml.v3
// rounds to float64 and goccy returns the digits as a string. Known reader
// behaviour, listed here by name instead of skipping the case.
func readerView(rd string, v any) any {
	switch x := v.(type) {
	case string:
		// Known goccy behaviour: it returns a !!binary scalar as []byte, not as a
		// string, so invalid UTF-8 (written as !!binary) reads back as []byte.
		if rd == "goccy" && !utf8.ValidString(x) {
			return []byte(x)
		}
	case *big.Int:
		if !isWide(x) {
			return x
		}
		switch rd {
		case "yaml.v3":
			f, _ := new(big.Float).SetInt(x).Float64()
			return f
		case "goccy":
			return x.String()
		}
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = readerView(rd, x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if rd == "goccy" && !utf8.ValidString(k) {
				// The same goccy behaviour for a key: it prints the bytes.
				k = fmt.Sprint([]byte(k))
			}
			out[k] = readerView(rd, e)
		}
		return out
	case map[any]any:
		if rd == "goccy" {
			// goccy returns every key as a string.
			out := make(map[string]any, len(x))
			for k, e := range x {
				key := "null"
				if k != nil {
					key = fmt.Sprint(k)
				}
				out[key] = readerView(rd, e)
			}
			return out
		}
		out := make(map[any]any, len(x))
		for k, e := range x {
			out[k] = readerView(rd, e)
		}
		return out
	}
	return v
}

// check writes want, reads it back with every reader and reports mismatches.
func check(t *testing.T, label string, want any) {
	t.Helper()
	out, err := oneyaml.Marshal(want)
	if err != nil {
		t.Errorf("%s: Marshal: %v", label, err)
		return
	}
	for _, rd := range readers {
		got, err := rd.read(out)
		if err != nil {
			if why, ok := isException(rd.name, want); ok {
				t.Logf("%s: %s: known reader problem (%s): %v", label, rd.name, why, err)
				continue
			}
			t.Errorf("%s: %s cannot read %q: %v", label, rd.name, trunc(out), err)
			continue
		}
		if !sameValue(readerView(rd.name, want), got) {
			if why, ok := isException(rd.name, want); ok {
				t.Logf("%s: %s: known reader problem (%s)", label, rd.name, why)
				continue
			}
			t.Errorf("%s: %s reads %q as %#v, want %#v", label, rd.name, trunc(out), got, want)
		}
	}
}

func trunc(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "..."
	}
	return string(b)
}

// wrappers: the same string as a root, a map value, a map key and a sequence item.
func contexts(s string) map[string]any {
	return map[string]any{
		"root":  s,
		"value": map[string]any{"k": s, "after": 1},
		"key":   map[string]any{s: "v", "z": 1},
		"seq":   []any{s, "next"},
		"deep":  map[string]any{"a": []any{map[string]any{"b": s, "c": []any{s}}}},
	}
}

func checkString(t *testing.T, s string) {
	t.Helper()
	for name, v := range contexts(s) {
		check(t, fmt.Sprintf("%s(%q)", name, s), v)
	}
}

// hazards are strings that sit on the edges of plain-scalar syntax or of one of
// the schemas.
var hazards = []string{
	"", " ", "  ", "\t", "a", "ab", "yes", "Yes", "YES", "no", "No", "NO", "y", "Y", "n", "N", "on", "On", "ON", "off", "Off", "OFF",
	"true", "True", "TRUE", "false", "False", "FALSE", "null", "Null", "NULL", "~", "nil", "None",
	"0", "1", "-1", "+1", "00", "007", "0755", "0o17", "0O17", "0x1F", "0X1f", "0b101", "0B11", "1_000", "1,000", "1:30", "190:20:30", "0:30", "1:30:45.5",
	"1.5", "1.", ".5", "-.5", "+.5", "1e3", "1E3", "1.5e3", "1.5e+3", "1e+3", ".inf", ".Inf", ".INF", "-.inf", "+.inf", ".nan", ".NaN", "-.nan",
	"inf", "nan", "NaN", "Infinity", "1_000.5", "12345678901234567890123", "-12345678901234567890123", "9223372036854775808",
	"2001-12-14", "2001-12-14t21:59:43.10-05:00", "2001-12-14 21:59:43.10 -5", "2001-1-1", "12:30", "<<", "=", "!", "!!str", "&a", "*a", "&a x", "*a x",
	"- a", "-", "--", "---", "...", "-- x", "---x", "? a", "?", "?x", ": a", ":", ":x", "a:", "a: b", "a:b", "a :b", "a : b", "a #b", "a# b", "#a", "a#",
	"[a]", "{a}", "a,b", "[", "]", "{", "}", ",", "a, b", "@a", "`a", "%a", "|", ">", "|-", ">+", "'a'", "\"a\"", "'", "\"", "a'b", "a\"b", "\\", "a\\b",
	" a", "a ", "\ta", "a\t", "a\tb", "a  b", "x\ny", "x\n", "x\n\n", "\nx", "x\n\ny", "  x\ny", "x\n  y", "x\n \ny", "x\r\ny", "x\ry", "\r",
	"\x00", "\x01", "a\x7fb", "\u0085", "a\u2028b", "a\u2029b", "\ufeff", "\ufeffa", "a\u00a0b", "é", "日本語", "😀", "a😀b", "\u00e9\n\u00e9",
	"key: value\nother: value", "# comment\n", "- item\n- item", "line one\nline two\nline three\n", "  leading\n  block\n", "tab\tin\nblock\n",
	"a b c", "A", "http://example.com/a?b=c#d", "C:\\path", "$HOME", "50%", "100%", "a=b", "a;b", "a|b", "a>b", "a<b", "<a>", "x-y", "-x", "+x", ".x", "_x",
}

func TestDifferentialHazardStrings(t *testing.T) {
	for _, s := range hazards {
		checkString(t, s)
	}
}

// TestDifferentialShortStrings writes every string of up to three characters from
// an alphabet made of the characters that matter to YAML syntax and to the
// schemas.
func TestDifferentialShortStrings(t *testing.T) {
	if testing.Short() {
		t.Skip("exhaustive; skipped in short mode")
	}
	alphabet := []string{"a", "1", "0", " ", "-", "?", ":", ",", "[", "{", "#", "&", "*", "!", "|", ">", "'", "\"", "%", "@", ".", "e", "x", "_", "\n", "\t", "+", "<", "="}
	var gen func(prefix string, n int)
	count := 0
	gen = func(prefix string, n int) {
		if prefix != "" {
			checkStringOnce(t, prefix)
			count++
		}
		if n == 0 {
			return
		}
		for _, c := range alphabet {
			gen(prefix+c, n-1)
		}
	}
	gen("", 3)
	t.Logf("%d strings", count)
}

// checkStringOnce checks a string in the contexts that differ most.
func checkStringOnce(t *testing.T, s string) {
	t.Helper()
	check(t, fmt.Sprintf("value(%q)", s), map[string]any{"k": s, "z": []any{s}})
	check(t, fmt.Sprintf("key(%q)", s), map[string]any{s: 1})
	check(t, fmt.Sprintf("root(%q)", s), s)
}

func TestDifferentialScalars(t *testing.T) {
	values := []any{
		nil, true, false, 0, 1, -1, 42, math.MaxInt64, math.MinInt64, uint64(math.MaxUint64),
		0.0, 1.5, -1.5, 3.0, 1e21, 1e-7, 1.7976931348623157e308, 5e-324, 0.1, 1.0 / 3, 123456789.0, 1e6, 1e15, 1e20, 100.0,
		math.Inf(1), math.Inf(-1), math.NaN(),
		[]any{}, map[string]any{}, []any{[]any{}, map[string]any{}}, map[string]any{"a": []any{}, "b": map[string]any{}},
		map[string]any{"a": nil, "b": []any{nil, nil}},
	}
	for i, v := range values {
		check(t, fmt.Sprintf("scalar#%d(%v)", i, v), v)
		check(t, fmt.Sprintf("scalar-in-map#%d", i), map[string]any{"k": v})
		check(t, fmt.Sprintf("scalar-in-seq#%d", i), []any{v, v})
	}
}

// ---- kit values ----

func kitDir(t testing.TB) string {
	dir := os.Getenv("ONEYAML_KIT")
	if dir == "" {
		t.Skip("ONEYAML_KIT is not set; it points at a checkout of the shared test-case kit")
	}
	return dir
}

// decodeKit turns the kit's JSON value encoding into Go values. Maps with
// non-string keys are {"$map": [[key, value], ...]} and special floats are
// {"$float": "inf"}. ok is false for values with no Go form.
func decodeKit(v any) (out any, ok bool) {
	switch x := v.(type) {
	case json.Number:
		s := x.String()
		if strings.ContainsAny(s, ".eE") {
			f, _ := x.Float64()
			return f, true
		}
		n, _ := new(big.Int).SetString(s, 10)
		return n, true
	case []any:
		res := make([]any, len(x))
		for i, e := range x {
			d, ok := decodeKit(e)
			if !ok {
				return nil, false
			}
			res[i] = d
		}
		return res, true
	case map[string]any:
		if f, has := x["$float"]; has && len(x) == 1 {
			switch f {
			case "inf":
				return math.Inf(1), true
			case "-inf":
				return math.Inf(-1), true
			}
			return math.NaN(), true
		}
		if _, has := x["$tagged"]; has {
			return nil, false
		}
		if pairs, has := x["$map"]; has && len(x) == 1 {
			res := map[any]any{}
			for _, p := range pairs.([]any) {
				kv := p.([]any)
				k, ok1 := decodeKit(kv[0])
				val, ok2 := decodeKit(kv[1])
				if !ok1 || !ok2 {
					return nil, false
				}
				if k != nil && !reflect.TypeOf(k).Comparable() {
					return nil, false // a Go map cannot have this key
				}
				res[k] = val
			}
			return res, true
		}
		res := make(map[string]any, len(x))
		for k, e := range x {
			d, ok := decodeKit(e)
			if !ok {
				return nil, false
			}
			res[k] = d
		}
		return res, true
	}
	return v, true
}

func readJSON(t testing.TB, path string) any {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return v
}

// widenInts maps *big.Int values that fit int64 to int64 so that the comparison
// does not depend on how a reader sizes small integers.
func TestDifferentialKitSchemaValues(t *testing.T) {
	kit := kitDir(t)
	files, _ := filepath.Glob(filepath.Join(kit, "suites/own/cases/schema/*/case.json"))
	if len(files) == 0 {
		t.Fatal("no schema cases found")
	}
	sort.Strings(files)
	checked, skipped := 0, 0
	for _, f := range files {
		var c struct {
			ID     string `json:"id"`
			Expect struct {
				Kind  string `json:"kind"`
				Value []any  `json:"value"`
			} `json:"expect"`
		}
		raw, _ := os.ReadFile(f)
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&c); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if c.Expect.Kind != "value" {
			continue
		}
		for i, doc := range c.Expect.Value {
			v, ok := decodeKit(doc)
			if !ok {
				skipped++
				t.Logf("skip %s doc %d: value has no Go form (tagged or unhashable key)", c.ID, i)
				continue
			}
			checked++
			check(t, fmt.Sprintf("%s#%d", c.ID, i), v)
		}
	}
	t.Logf("%d kit schema values checked, %d skipped", checked, skipped)
}

func TestDifferentialKitWorkloads(t *testing.T) {
	kit := kitDir(t)
	files, _ := filepath.Glob(filepath.Join(kit, "bench/workloads/*/*/expected.json"))
	if len(files) == 0 {
		t.Fatal("no workloads found")
	}
	for _, f := range files {
		raw := readJSON(t, f)
		docs := []any{raw}
		v, ok := decodeKit(raw)
		if !ok {
			t.Fatalf("%s: undecodable", f)
		}
		_ = docs
		check(t, filepath.Base(filepath.Dir(f)), v)
	}
	t.Logf("%d workloads checked", len(files))
}
