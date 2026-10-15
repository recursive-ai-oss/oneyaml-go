package bench

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	yv3 "go.yaml.in/yaml/v3"
	oneyaml "oneyaml.dev/go"
	compat "oneyaml.dev/go/compat/yamlv3"
)

// This file compares oneyaml.dev/go/compat/yamlv3 with the real yaml.v3 on the
// kit's inputs and on hand-made and generated values. A result is one of: same,
// both refuse, or different. A difference must be on the list of intended
// differences below (each is a yaml.v3 behaviour the shim does not reproduce,
// with the reason); anything else fails the test.

// diffTally counts outcomes and keeps the first few examples of each kind.
type diffTally struct {
	name                    string
	same, bothErr, intended int
	unexplained             []string
	byReason                map[string]int
	examples                map[string]string
}

func newTally(name string) *diffTally {
	return &diffTally{name: name, byReason: map[string]int{}, examples: map[string]string{}}
}

func (d *diffTally) report(t *testing.T) {
	t.Helper()
	t.Logf("%s: same=%d bothErr=%d intended=%d unexplained=%d", d.name, d.same, d.bothErr, d.intended, len(d.unexplained))
	var reasons []string
	for r := range d.byReason {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		t.Logf("  intended[%s] x%d  e.g. %s", r, d.byReason[r], d.examples[r])
	}
	for i, u := range d.unexplained {
		if i >= 12 {
			t.Logf("  ... %d more", len(d.unexplained)-i)
			break
		}
		t.Errorf("unexplained: %s", u)
	}
	if p := os.Getenv("ONEYAML_REPORT"); p != "" {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%s: same=%d bothErr=%d intended=%d unexplained=%d\n", d.name, d.same, d.bothErr, d.intended, len(d.unexplained))
			for _, r := range reasons {
				fmt.Fprintf(f, "  intended[%s] x%d  e.g. %s\n", r, d.byReason[r], d.examples[r])
			}
			for _, u := range d.unexplained {
				fmt.Fprintf(f, "  UNEXPLAINED %s\n", u)
			}
			f.Close()
		}
	}
}

func (d *diffTally) note(reason, what string) {
	d.intended++
	d.byReason[reason]++
	if _, ok := d.examples[reason]; !ok {
		d.examples[reason] = what
	}
}

// ---- corpus ----

type corpusItem struct {
	name string
	data []byte
	// dir is the kit case directory, when the item comes from one; it holds the
	// kit's verdict on the input.
	dir string
}

// compatCorpus gathers YAML inputs from the kit: the pinned test suite, the
// family's own cases and the benchmark workloads, plus a few built-in texts.
func compatCorpus(t testing.TB) []corpusItem {
	var items []corpusItem
	for i, s := range builtinTexts {
		items = append(items, corpusItem{name: fmt.Sprintf("builtin#%d", i), data: []byte(s)})
	}
	if kit := os.Getenv("ONEYAML_KIT"); kit != "" {
		yts := os.Getenv("ONEYAML_YTS_DIR")
		if yts == "" {
			yts = filepath.Join(kit, "suites/yaml-test-suite/data-2022-01-17")
		}
		add := func(root string) {
			filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				base := filepath.Base(p)
				if base == "in.yaml" || (strings.HasSuffix(base, ".yaml") && !strings.HasPrefix(base, "out") && base != "expected.yaml") {
					if info.Size() > 1<<20 {
						return nil
					}
					raw, err := os.ReadFile(p)
					if err == nil {
						rel, _ := filepath.Rel(kit, p)
						items = append(items, corpusItem{name: rel, data: raw, dir: filepath.Dir(p)})
					}
				}
				return nil
			})
		}
		add(yts)
		add(filepath.Join(kit, "suites/own"))
		files, _ := filepath.Glob(filepath.Join(kit, "bench/workloads/real/*/input.yaml"))
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err == nil && len(raw) < 4<<20 {
				items = append(items, corpusItem{name: filepath.Base(filepath.Dir(f)), data: raw})
			}
		}
	}
	if testing.Short() {
		items = shortSubset(items)
	}
	return items
}

// shortSubset keeps every shortStride-th input, the built-in texts and every
// input named on an exception list, so a -short run stays fast and still
// exercises each listed difference. The order of the corpus is fixed, so the
// subset is the same on every run.
func shortSubset(items []corpusItem) []corpusItem {
	const shortStride = 8
	var out []corpusItem
	for i, it := range items {
		rel := strings.TrimPrefix(it.name, "suites/")
		_, listed := readExceptions[strings.TrimPrefix(rel, "yaml-test-suite/data-2022-01-17/")]
		_, listed2 := nodeExceptions[rel]
		listed = listed || listed2 || commentReadGaps[gapName(it.name)] || commentWriteGaps[gapName(it.name)]
		if i%shortStride == 0 || it.dir == "" && strings.HasPrefix(it.name, "builtin#") || listed {
			out = append(out, it)
		}
	}
	return out
}

var builtinTexts = []string{
	"a: 1\nb: [1, 2, 3]\nc: {d: e}\n",
	"a: 0777\nb: 0o17\nc: 0b11\nd: 1_000\ne: 2001-01-01\nf: yes\ng: 1e3\nh: 0x1F\ni: !!binary aGk=\nj: .5\nk: +1\nl: 1:30\nm: 2001-01-01T10:00:00Z\nn: ~\no: null\np: True\nq: no\n",
	"base: &b {x: 1, y: 2}\nuse:\n  <<: *b\n  y: 3\n",
	"- &a [1, 2]\n- *a\n- {k: *a}\n",
	"? [a, b]\n: c\n",
	"1: a\n2.5: b\ntrue: c\nnull: d\n",
	"s: |\n  one\n  two\nt: >\n  folded\n  text\n",
	"a: 'single'\nb: \"double\\n\"\nc: plain text\n",
	"x: !!str 123\ny: !!int '42'\nz: !!float 3\n",
	"",
	"# only a comment\n",
	"---\n---\na: 1\n",
	"[a, b\n",
	"a: b: c\n",
	"- 1\n- 2\n- [3, 4]\n- {a: 1}\n",
	"a: 9223372036854775807\nb: 9223372036854775808\nc: 18446744073709551616\nd: -9223372036854775808\ne: -9223372036854775809\n",
	"a: 1.5\nb: .inf\nc: -.inf\nd: .nan\ne: 1e400\n",
}

// ---- comparison of decoded values ----

// compatSame compares decoded values, treating NaN as equal to NaN and time values
// by instant.
func compatSame(a, b any) bool {
	return compatDeepEq(reflect.ValueOf(a), reflect.ValueOf(b), 0)
}

func compatDeepEq(a, b reflect.Value, depth int) bool {
	if depth > 200 {
		return true
	}
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}
	if a.Type() != b.Type() {
		return false
	}
	switch a.Kind() {
	case reflect.Float32, reflect.Float64:
		x, y := a.Float(), b.Float()
		return x == y || (math.IsNaN(x) && math.IsNaN(y))
	case reflect.Interface, reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return compatDeepEq(a.Elem(), b.Elem(), depth+1)
	case reflect.Slice:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !compatDeepEq(a.Index(i), b.Index(i), depth+1) {
				return false
			}
		}
		return true
	case reflect.Array:
		for i := 0; i < a.Len(); i++ {
			if !compatDeepEq(a.Index(i), b.Index(i), depth+1) {
				return false
			}
		}
		return true
	case reflect.Map:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}
		for _, k := range a.MapKeys() {
			bv := b.MapIndex(k)
			if !bv.IsValid() {
				// NaN keys never match; fall back to a count comparison.
				if kk := k; kk.Kind() == reflect.Interface && kk.Elem().Kind() == reflect.Float64 && math.IsNaN(kk.Elem().Float()) {
					continue
				}
				return false
			}
			if !compatDeepEq(a.MapIndex(k), bv, depth+1) {
				return false
			}
		}
		return true
	case reflect.Struct:
		if a.Type() == reflect.TypeOf(time.Time{}) {
			return a.Interface().(time.Time).Equal(b.Interface().(time.Time))
		}
		for i := 0; i < a.NumField(); i++ {
			if a.Type().Field(i).PkgPath != "" {
				continue
			}
			if !compatDeepEq(a.Field(i), b.Field(i), depth+1) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a.Interface(), b.Interface())
}

func unmarshalBoth(in []byte, mk func() any) (ya, ca any, yerr, cerr error) {
	ya, ca = mk(), mk()
	yerr = yv3.Unmarshal(in, ya)
	cerr = compat.Unmarshal(in, ca)
	return
}

// ---- intended differences in reading ----

// verdict is the kit's judgement of an input: valid, invalid or unknown.
type verdict uint8

const (
	verdictUnknown verdict = iota
	verdictValid
	verdictInvalid
)

func (it corpusItem) verdict() verdict {
	if it.dir == "" {
		return verdictUnknown
	}
	if _, err := os.Stat(filepath.Join(it.dir, "error")); err == nil {
		return verdictInvalid
	}
	if raw, err := os.ReadFile(filepath.Join(it.dir, "case.json")); err == nil {
		var c struct {
			Category  string `json:"category"`
			Operation struct {
				Type string `json:"type"`
			} `json:"operation"`
			Expect struct {
				Kind string `json:"kind"`
			} `json:"expect"`
		}
		if json.Unmarshal(raw, &c) == nil {
			switch {
			case c.Operation.Type == "patch" && c.Expect.Kind == "error":
				return verdictUnknown
			case c.Expect.Kind == "error":
				return verdictInvalid
			}
			return verdictValid
		}
		return verdictUnknown
	}
	if _, err := os.Stat(filepath.Join(it.dir, "in.json")); err == nil {
		return verdictValid
	}
	return verdictValid
}

// kitJSON returns the first value of the kit's expected JSON for the input.
func (it corpusItem) kitJSON() (any, bool) {
	if it.dir == "" {
		return nil, false
	}
	raw, err := os.ReadFile(filepath.Join(it.dir, "in.json"))
	if err != nil {
		return nil, false
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return nil, false
	}
	return v, true
}

// targetMisparses reports whether the kit holds an event stream for the input
// (and no expected JSON) and everything points to yaml.v3 reading the text wrongly
// while the shim reads it right:
//   - the core parser, which the shim reads through, gives exactly the kit's events;
//   - the structure yaml.v3 makes of the text differs from the kit's;
//   - the shim's value is the value yaml.v3 itself makes of the kit's structure
//     (the tree built from the events and decoded by yaml.v3), so the scalars
//     resolve as yaml.v3 resolves them and a defect in the shim's resolver, or in
//     what it makes of a collection, cannot hide behind the parser being right.
func (it corpusItem) targetMisparses(ca any) bool {
	want, ok := kitEvents(it.dir, it.name)
	if !ok {
		return false
	}
	d, err := oneyaml.Parse(it.data)
	if err != nil || strings.Join(eventNotation(d), "\n") != strings.Join(want, "\n") {
		return false
	}
	tree, ok := parseEventTree(want)
	if !ok {
		return false
	}
	var yn yv3.Node
	if yv3.Unmarshal(it.data, &yn) != nil ||
		strings.Join(yamlv3Shape(&yn, nil), "\n") == strings.Join(tree.shape(nil), "\n") {
		return false
	}
	var expect any
	if tree.yamlv3Tree(map[string]*yv3.Node{}).Decode(&expect) != nil {
		return false
	}
	return compatSame(expect, ca)
}

// duplicateKeysDifferByTag reports whether some mapping of the text has two keys
// with the same kind and text that resolve to different tags: yaml.v3 reports
// them as one key, the shim keeps them apart as the specification does.
func duplicateKeysDifferByTag(data []byte) bool {
	var root compat.Node
	if compat.Unmarshal(data, &root) != nil {
		return false
	}
	stack := []*compat.Node{&root}
	seen := map[*compat.Node]bool{}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		stack = append(stack, n.Content...)
		if n.Kind != compat.MappingNode {
			continue
		}
		tags := map[string]string{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			id := fmt.Sprintf("%d\x00%s", k.Kind, k.Value)
			if tag, ok := tags[id]; ok && tag != k.Tag {
				return true
			}
			tags[id] = k.Tag
		}
	}
	return false
}

var yamlv3ErrorLine = regexp.MustCompile(`line (\d+)`)

// errorAfterFirstDocument reports whether yaml.v3's error names a line after the
// first document of the text, which the shim never reads.
func errorAfterFirstDocument(data []byte, err error) bool {
	m := yamlv3ErrorLine.FindStringSubmatch(err.Error())
	if m == nil {
		return false
	}
	at, _ := strconv.Atoi(m[1])
	end := firstDocumentEnd(data)
	return end > 0 && at > end
}

// firstDocumentEnd returns the 1-based line of the marker that ends the first
// document (a "---" after content, or "..."), or 0 if there is none.
func firstDocumentEnd(data []byte) int {
	content := false
	for i, l := range strings.Split(string(data), "\n") {
		marker := func(m string) bool {
			return strings.HasPrefix(l, m) && (len(l) == len(m) || l[len(m)] == ' ' || l[len(m)] == '\t' || l[len(m)] == '\r')
		}
		switch t := strings.TrimSpace(l); {
		case marker("..."):
			return i + 1
		case marker("---"):
			if content {
				return i + 1
			}
			content = strings.TrimSpace(l[3:]) != "" && !strings.HasPrefix(strings.TrimSpace(l[3:]), "#")
		case t == "" || t[0] == '#' || t[0] == '%':
		default:
			content = true
		}
	}
	return 0
}

// matchesJSON compares a decoded value with the kit's expected JSON loosely:
// numbers by value, map keys by their text, and a time or a binary string
// against whatever the JSON holds.
func matchesJSON(v any, j any) bool {
	switch x := v.(type) {
	case nil:
		return j == nil
	case bool:
		b, ok := j.(bool)
		return ok && b == x
	case string:
		s, ok := j.(string)
		return ok && s == x
	case int, int64, uint64, float64:
		n, ok := j.(json.Number)
		if !ok {
			return false
		}
		f, err := n.Float64()
		if err != nil {
			return false
		}
		switch y := x.(type) {
		case int:
			return float64(y) == f
		case int64:
			return float64(y) == f
		case uint64:
			return float64(y) == f
		case float64:
			return y == f || (math.IsNaN(y) || math.IsInf(y, 0))
		}
	case time.Time:
		return true
	case []any:
		l, ok := j.([]any)
		if !ok || len(l) != len(x) {
			return false
		}
		for i := range x {
			if !matchesJSON(x[i], l[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		m, ok := j.(map[string]any)
		if !ok || len(m) != len(x) {
			return false
		}
		for k, e := range x {
			je, has := m[k]
			if !has || !matchesJSON(e, je) {
				return false
			}
		}
		return true
	case map[any]any:
		m, ok := j.(map[string]any)
		if !ok || len(m) != len(x) {
			return false
		}
		for k, e := range x {
			je, has := m[fmt.Sprint(k)]
			if !has || !matchesJSON(e, je) {
				return false
			}
		}
		return true
	}
	return false
}

// readExceptions are single inputs where yaml.v3 gives a wrong value and the kit has
// no expected JSON to show it.
var readExceptions = map[string]string{
	"4ABK/in.yaml": "yaml.v3 keeps the colon in the flow key 'omitted value:'",
	"suites/own/cases/parse/top-level-literal-with-indentation-indicator/input.yaml": "yaml.v3 drops the space that belongs to a top-level block scalar with an indentation indicator (the kit expects it)",
}

// readDifference names the reason a shim result may differ from yaml.v3's. An
// empty string means the difference is not on the list.
func readDifference(it corpusItem, ya, ca any, yerr, cerr error) string {
	vd := it.verdict()
	var le *oneyaml.LimitError
	switch {
	case yerr == nil && errors.As(cerr, &le):
		return "the shim enforces the alias-expansion and depth limits (yaml.v3 allows more)"
	case yerr != nil && cerr == nil && strings.Contains(yerr.Error(), "already defined") && duplicateKeysDifferByTag(it.data):
		return "yaml.v3 calls true and \"true\" (or 80 and \"80\") the same key"
	case yerr != nil && cerr == nil && vd == verdictInvalid && errorAfterFirstDocument(it.data, yerr):
		return "the shim reads one document, so a later syntax error is not reached"
	case cerr == nil && yerr == nil && readExceptions[strings.TrimPrefix(it.name, "suites/yaml-test-suite/data-2022-01-17/")] != "":
		return readExceptions[strings.TrimPrefix(it.name, "suites/yaml-test-suite/data-2022-01-17/")]
	case yerr != nil && cerr == nil && vd == verdictValid:
		return "yaml.v3 refuses valid YAML (libyaml scanner limits)"
	case yerr == nil && cerr != nil && vd == verdictInvalid:
		return "yaml.v3 accepts invalid YAML"
	case yerr == nil && cerr == nil && vd == verdictValid:
		if j, ok := it.kitJSON(); ok && matchesJSON(compatDeref(ca), j) && !matchesJSON(compatDeref(ya), j) {
			return "yaml.v3 mis-parses valid YAML (kit expectation)"
		}
		// A case with expected events but no JSON: see targetMisparses.
		if it.targetMisparses(compatDeref(ca)) {
			return "yaml.v3 mis-parses valid YAML (kit expectation)"
		}
	}
	return ""
}

func TestCompatV3UnmarshalAny(t *testing.T) {
	items := compatCorpus(t)
	tally := newTally("unmarshal-any")
	for _, it := range items {
		ya, ca, yerr, cerr := unmarshalBoth(it.data, func() any { return new(any) })
		switch {
		case yerr != nil && cerr != nil:
			tally.bothErr++
		case yerr == nil && cerr == nil && compatSame(ya, ca):
			tally.same++
		default:
			if r := readDifference(it, ya, ca, yerr, cerr); r != "" {
				tally.note(r, it.name)
				continue
			}
			tally.unexplained = append(tally.unexplained, fmt.Sprintf("%s: yaml.v3 err=%v value=%.200v | shim err=%v value=%.200v", it.name, yerr, compatDeref(ya), cerr, compatDeref(ca)))
		}
	}
	tally.report(t)
}

func compatDeref(v any) any {
	if p, ok := v.(*any); ok {
		return *p
	}
	return v
}

var _ = bytes.Equal

// The rule that excuses a difference as the target reading valid YAML wrongly must
// not hide a shim that reads it wrongly too: a shim value that is not what the
// target makes of the kit's structure is not excused.
func TestMisparseRuleChecksTheShimValue(t *testing.T) {
	kit := os.Getenv("ONEYAML_KIT")
	if kit == "" {
		t.Skip("ONEYAML_KIT is not set")
	}
	dir := filepath.Join(kit, "suites/own/cases/parse/bom-then-comment-after-bare-document")
	data, err := os.ReadFile(filepath.Join(dir, "input.yaml"))
	if err != nil {
		t.Skip(err)
	}
	it := corpusItem{name: "suites/own/cases/parse/bom-then-comment-after-bare-document/input.yaml", data: data, dir: dir}
	var target, shim any
	if err := yv3.Unmarshal(data, &target); err != nil {
		t.Fatal(err)
	}
	if err := compat.Unmarshal(data, &shim); err != nil {
		t.Fatal(err)
	}
	if !it.targetMisparses(shim) {
		t.Fatalf("the shim reads %#v, which is right, but the rule does not excuse the difference", shim)
	}
	if it.targetMisparses(7) || it.targetMisparses(nil) || it.targetMisparses(target) {
		t.Fatal("a wrong value was excused")
	}
}

func TestDuplicateKeyAndLaterErrorRules(t *testing.T) {
	if !duplicateKeysDifferByTag([]byte("true: 1\n\"true\": 2\n80: a\n\"80\": b\n")) {
		t.Error("keys that differ by tag were not seen")
	}
	if duplicateKeysDifferByTag([]byte("a: 1\na: 2\n")) {
		t.Error("a real duplicate was excused")
	}
	later := []byte("a: 1\n---\nb: [\n")
	if got := firstDocumentEnd(later); got != 2 {
		t.Errorf("first document ends at line %d, want 2", got)
	}
	if got := firstDocumentEnd([]byte("---\na: 1\n...\nb: 2\n")); got != 3 {
		t.Errorf("first document ends at line %d, want 3", got)
	}
	if got := firstDocumentEnd([]byte("a: [\n")); got != 0 {
		t.Errorf("one document: %d", got)
	}
	if !errorAfterFirstDocument([]byte("a: 1\n---\nb: 2\nc: [\n"), errors.New("yaml: line 4: did not find expected node content")) {
		t.Error("an error after the first document was not excused")
	}
	if errorAfterFirstDocument([]byte("a: 1\nb: [\n---\nc: 2\n"), errors.New("yaml: line 2: did not find expected node content")) {
		t.Error("an error inside the first document was excused")
	}
}
