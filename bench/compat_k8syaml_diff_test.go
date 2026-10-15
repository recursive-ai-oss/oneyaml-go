package bench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	sigsyaml "sigs.k8s.io/yaml"

	oneyaml "oneyaml.dev/go"
	k8s "oneyaml.dev/go/compat/k8syaml"
)

// The differential tests hold compat/k8syaml against the real sigs.k8s.io/yaml.
// Every difference is either a failure or sits on a named list with its reason.

type k8sInput struct {
	name string
	data []byte
}

// k8sCorpus is every YAML file the kit holds (the pinned conformance suite, the
// family's own cases and the real-world workloads) plus the Kubernetes-shaped
// documents in compat_k8syaml_corpus_test.go.
func k8sCorpus(t testing.TB) []k8sInput {
	t.Helper()
	var out []k8sInput
	kit := os.Getenv("ONEYAML_KIT")
	if kit == "" {
		t.Log("ONEYAML_KIT is not set: running on the built-in documents only")
	} else {
		_ = filepath.WalkDir(kit, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".yaml") {
				return nil
			}
			rel, _ := filepath.Rel(kit, p)
			// The alias bomb expands to hundreds of megabytes in sigs; it has its own test.
			if strings.Contains(rel, "alias-byte-expansion-bomb") {
				return nil
			}
			if strings.HasPrefix(rel, "suites/") || strings.HasPrefix(rel, "bench/workloads/") {
				if b, err := os.ReadFile(p); err == nil {
					out = append(out, k8sInput{rel, b})
				}
			}
			return nil
		})
		sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
		if testing.Short() {
			out = k8sShortSubset(out)
		}
	}
	for i, s := range k8sBuiltin {
		out = append(out, k8sInput{fmt.Sprintf("builtin/%02d", i), []byte(s)})
	}
	return out
}

// k8sShortSubset keeps every k8sShortStride-th input and every input on the list
// of intended differences, so a -short run stays fast and still exercises each
// listed difference. The corpus is sorted, so the subset is the same on every run.
func k8sShortSubset(in []k8sInput) []k8sInput {
	const k8sShortStride = 8
	var out []k8sInput
	for i, c := range in {
		if _, listed := k8sIntended[c.name]; listed || i%k8sShortStride == 0 {
			out = append(out, c)
		}
	}
	return out
}

const ytsPrefix = "suites/yaml-test-suite/data-2022-01-17/"

// kitInvalid reports whether the kit says the input is not YAML: the suite marks
// those with an error file, and the family's diagnostics cases are all invalid.
func kitInvalid(name string) bool {
	if strings.HasPrefix(name, "suites/own/cases/diagnostics/") {
		return true
	}
	if strings.HasPrefix(name, "suites/own/cases/") {
		// The family's own cases say in case.json whether the input is an error.
		raw, err := os.ReadFile(filepath.Join(os.Getenv("ONEYAML_KIT"), filepath.Dir(name), "case.json"))
		var c struct {
			Expect struct {
				Kind string `json:"kind"`
			} `json:"expect"`
		}
		if err == nil && json.Unmarshal(raw, &c) == nil && c.Expect.Kind == "error" {
			return true
		}
	}
	if rest, ok := strings.CutPrefix(name, ytsPrefix); ok {
		dir := filepath.Dir(rest)
		kit := os.Getenv("ONEYAML_KIT")
		if _, err := os.Stat(filepath.Join(kit, ytsPrefix, dir, "error")); err == nil {
			return true
		}
		// Variants of a case ("4MUZ/00") keep the error file in the case directory.
		if _, err := os.Stat(filepath.Join(kit, ytsPrefix, filepath.Dir(dir), "error")); err == nil && strings.Contains(dir, "/") {
			return true
		}
	}
	return false
}

// k8sIntended lists the inputs on which the compat package intentionally gives a
// different answer from sigs.k8s.io/yaml, with the reason. Each entry is a case
// in which sigs loses or changes data, or accepts text that is not YAML.
var k8sIntended = map[string]string{
	// The reader behind sigs mis-parses valid YAML 1.2; our output equals the
	// suite's expected JSON (checked below).
	"suites/own/cases/parse/top-level-literal-with-indentation-indicator/input.yaml": "literal block scalar: sigs drops the space that belongs to a top-level scalar with an indentation indicator",
	ytsPrefix + "4ABK/in.yaml":    "flow mapping: a key before ':' with no value keeps the colon in the key",
	ytsPrefix + "652Z/in.yaml":    "flow mapping: '?foo' is a plain key, not an explicit-key indicator",
	ytsPrefix + "DK3J/in.yaml":    "folded block scalar with '#' in a text line is read as empty",
	ytsPrefix + "FP8R/in.yaml":    "folded block scalar with multiple lines is read as empty",
	ytsPrefix + "HM87/01/in.yaml": "flow sequence: '?x' is a plain scalar, not a key",
	ytsPrefix + "JEF9/02/in.yaml": "literal block scalar of only line breaks loses them",
	ytsPrefix + "L24T/01/in.yaml": "literal block scalar with trailing whitespace line loses a line break",
	ytsPrefix + "Y2GN/in.yaml":    "an anchor name may contain ':'; sigs ends the name at ':' and moves it into the value",
	ytsPrefix + "Y2GN/out.yaml":   "the same case, canonical form",
	// Two keys that print the same (1 and 1.0): sigs picks one at random.
	"suites/own/cases/edit/pointer-matches-text-not-value/expected.yaml":      "keys 1 and 1.0 both become \"1\": sigs picks the winner by map order, we take the last written",
	"suites/own/cases/edit/pointer-error-ambiguous-remove/input.yaml":         "keys 80 and \"80\" print the same: sigs picks the winner by map order, we take the last written",
	"suites/own/cases/edit/refuse-ambiguous-key/input.yaml":                   "keys 80 and \"80\" print the same: sigs picks the winner by map order, we take the last written",
	"suites/own/cases/schema/quoted-and-plain-keys-differ-by-type/input.yaml": "keys \"1\" and 1 print the same: sigs picks the winner by map order, we take the last written",
	"suites/own/cases/edit/pointer-error-ambiguous-after-success/input.yaml":  "keys 80 and \"80\" print the same: sigs picks the winner by map order, we take the last written",
	"suites/own/cases/edit/pointer-error-ambiguous-boolean-key/input.yaml":    "keys true and \"true\" print the same: sigs picks the winner by map order, we take the last written",
	"suites/own/cases/edit/pointer-error-ambiguous-test/input.yaml":           "keys 80 and \"80\" print the same: sigs picks the winner by map order, we take the last written",
	"suites/own/cases/edit/pointer-matches-text-not-value/input.yaml":         "keys 1 and 1.0 both become \"1\": sigs picks the winner by map order, we take the last written",
}

// k8sMisparseReason names the intended difference found from the kit's expected
// events rather than from a list.
const k8sMisparseReason = "sigs mis-parses valid YAML (kit expectation)"

// k8sMisparse reports whether everything points to sigs reading the input wrongly
// while we read it right, for a case whose expected events the kit holds (and no
// expected JSON):
//   - the core parser, which this package reads through, gives the kit's events;
//   - sigs reads the kit's structure, written out as flow YAML, differently from
//     how it reads the input;
//   - our reading of the input is what sigs makes of that flow text.
//
// So a defect in our scalar resolution or in our conversion cannot hide behind the
// parser being right: sigs resolves the scalars of the kit's structure.
func k8sMisparse(name string, ours, sigs func([]byte) ([]byte, error)) bool {
	kit := os.Getenv("ONEYAML_KIT")
	if kit == "" || !strings.HasPrefix(name, "suites/") {
		return false
	}
	want, ok := kitEvents(filepath.Join(kit, filepath.Dir(name)), name)
	if !ok {
		return false
	}
	data, err := os.ReadFile(filepath.Join(kit, name))
	if err != nil {
		return false
	}
	d, err := oneyaml.Parse(data)
	if err != nil || strings.Join(eventNotation(d), "\n") != strings.Join(want, "\n") {
		return false
	}
	tree, ok := parseEventTree(want)
	if !ok {
		return false
	}
	var flow strings.Builder
	if !tree.flowYAML(&flow) {
		return false
	}
	expect, err := sigs([]byte(flow.String()))
	if err != nil {
		return false
	}
	theirs, terr := sigs(data)
	mine, merr := ours(data)
	return merr == nil && bytes.Equal(mine, expect) && (terr != nil || !bytes.Equal(theirs, expect))
}

// kitValid reports whether the kit holds the input to be YAML: a conformance case
// with no error file, a workload, or one of the family's cases that expects a
// value.
func kitValid(name string) bool {
	switch {
	case strings.HasPrefix(name, "bench/workloads/"):
		return true
	case strings.HasPrefix(name, ytsPrefix):
		return !kitInvalid(name)
	case strings.HasPrefix(name, "suites/own/cases/"):
		kit := os.Getenv("ONEYAML_KIT")
		dir := filepath.Join(kit, filepath.Dir(name))
		raw, err := os.ReadFile(filepath.Join(dir, "case.json"))
		if err != nil {
			return false
		}
		var c struct {
			Expect struct {
				Kind string `json:"kind"`
			} `json:"expect"`
		}
		return json.Unmarshal(raw, &c) == nil && c.Expect.Kind != "error" && !kitInvalid(name)
	}
	return false
}

type outcome int

const (
	ocSame outcome = iota
	ocBothError
	ocAllowed
)

func (o outcome) String() string { return [...]string{"same", "both error", "allowed difference"}[o] }

// classify compares one sigs result with ours and returns the verdict, or an
// error describing an unexplained difference.
func classify(name string, want []byte, werr error, got []byte, gerr error) (outcome, string, error) {
	return classifyWith(name, want, werr, got, gerr, k8s.YAMLToJSON, sigsyaml.YAMLToJSON)
}

// classifyWith is classify for any pair of conversions.
func classifyWith(name string, want []byte, werr error, got []byte, gerr error, ours, sigs func([]byte) ([]byte, error)) (outcome, string, error) {
	switch {
	case werr != nil && gerr != nil:
		return ocBothError, "", nil
	case werr != nil:
		if kitInvalid(name) {
			return ocSame, "", fmt.Errorf("the kit says this is not YAML but we accept it: %s", got)
		}
		if !kitValid(name) {
			return ocSame, "", fmt.Errorf("sigs rejects this input and the kit does not say it is valid YAML (%v), but we accept it: %s", werr, got)
		}
		return ocAllowed, "sigs rejects valid YAML: " + werr.Error(), nil
	case gerr != nil:
		if r, ok := k8sIntended[name]; ok {
			return ocAllowed, r, nil
		}
		if kitInvalid(name) {
			return ocAllowed, "sigs accepts text that is not YAML", nil
		}
		return ocSame, "", fmt.Errorf("we fail where sigs does not: %v\n sigs: %.200s", gerr, want)
	case bytes.Equal(want, got):
		return ocSame, "", nil
	}
	if r, ok := k8sIntended[name]; ok {
		return ocAllowed, r, nil
	}
	if k8sMisparse(name, ours, sigs) {
		return ocAllowed, k8sMisparseReason, nil
	}
	if replacementOnly(want, got) {
		return ocAllowed, "U+FFFD is escaped differently by Go releases", nil
	}
	return ocSame, "", fmt.Errorf("outputs differ\n sigs: %.300s\n ours: %.300s", want, got)
}

// replacementOnly reports whether two JSON texts differ only in how U+FFFD is
// written: newer Go releases write the character itself where older ones, and
// we, write the escape. Both read back as the same string.
func replacementOnly(want, got []byte) bool {
	const esc = `\ufffd`
	return bytes.Contains(got, []byte(esc)) && bytes.Equal(bytes.ReplaceAll(got, []byte(esc), []byte("\uFFFD")), bytes.ReplaceAll(want, []byte(esc), []byte("\uFFFD")))
}

func TestK8sDiffYAMLToJSON(t *testing.T) {
	in := k8sCorpus(t)
	counts := map[outcome]int{}
	reasons := map[string]int{}
	for _, c := range in {
		want, werr := sigsyaml.YAMLToJSON(c.data)
		got, gerr := k8s.YAMLToJSON(c.data)
		o, why, err := classify(c.name, want, werr, got, gerr)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		counts[o]++
		if why != "" {
			reasons[firstWords(why)]++
		}
	}
	t.Logf("YAMLToJSON on %d inputs: %d identical, %d rejected by both, %d allowed differences", len(in), counts[ocSame], counts[ocBothError], counts[ocAllowed])
	for r, n := range reasons {
		t.Logf("  %4d  %s", n, r)
	}
}

func firstWords(s string) string {
	if i := strings.Index(s, ":"); i > 0 && i < 40 {
		return s[:i]
	}
	if len(s) > 60 {
		return s[:60]
	}
	return s
}

// The inputs we read differently from sigs read as the suite's expected value.
func TestK8sIntendedMatchSuite(t *testing.T) {
	kit := os.Getenv("ONEYAML_KIT")
	if kit == "" {
		t.Skip("ONEYAML_KIT is not set")
	}
	checked := 0
	for name := range k8sIntended {
		rest, ok := strings.CutPrefix(name, ytsPrefix)
		if !ok || !strings.HasSuffix(rest, "/in.yaml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(kit, name))
		if err != nil {
			t.Fatal(err)
		}
		exp, err := os.ReadFile(filepath.Join(kit, ytsPrefix, filepath.Dir(rest), "in.json"))
		if err != nil {
			t.Logf("%s: the suite has no expected JSON for this case", name)
			continue
		}
		got, err := k8s.YAMLToJSON(b)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var gv, ev any
		if err := json.Unmarshal(got, &gv); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(exp, &ev); err != nil {
			// A suite file may hold several documents; read the first.
			if err := json.NewDecoder(bytes.NewReader(exp)).Decode(&ev); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(gv, ev) {
			t.Errorf("%s: ours %s, the suite expects %s", name, got, exp)
		}
		checked++
	}
	t.Logf("%d intended differences equal the suite's expected value", checked)
}

func TestK8sDiffYAMLToJSONStrict(t *testing.T) {
	in := k8sCorpus(t)
	n, allowed := 0, 0
	for _, c := range in {
		want, werr := sigsyaml.YAMLToJSONStrict(c.data)
		got, gerr := k8s.YAMLToJSONStrict(c.data)
		n++
		if werr != nil && gerr != nil || werr == nil && gerr == nil && bytes.Equal(want, got) {
			continue
		}
		if werr == nil && gerr == nil && replacementOnly(want, got) {
			allowed++
			continue
		}
		if _, ok := k8sIntended[c.name]; ok || (werr == nil && gerr == nil && k8sMisparse(c.name, k8s.YAMLToJSONStrict, sigsyaml.YAMLToJSONStrict)) {
			allowed++
			continue
		}
		// Text that is not YAML, or YAML that the reader behind sigs rejects.
		if gerr != nil && werr == nil && kitInvalid(c.name) {
			allowed++
			continue
		}
		if _, nerr := sigsyaml.YAMLToJSON(c.data); werr != nil && nerr != nil {
			allowed++
			continue
		}
		// Sigs reports a key as a duplicate when a merge key puts it there twice,
		// although the mapping writes it once.
		if werr != nil && gerr == nil && strings.Contains(werr.Error(), "already set in map") && strings.Contains(string(c.data), "<<") {
			allowed++
			continue
		}
		t.Errorf("%s: strict differs\n sigs: %.200s / %v\n ours: %.200s / %v", c.name, want, werr, got, gerr)
	}
	t.Logf("YAMLToJSONStrict on %d inputs, %d allowed differences", n, allowed)
}

func TestK8sDiffUnmarshalAny(t *testing.T) {
	in := k8sCorpus(t)
	checked := 0
	for _, c := range in {
		if _, skip := k8sIntended[c.name]; skip {
			continue
		}
		for _, useNumber := range []bool{false, true} {
			var opts []k8s.JSONOpt
			var sopts []sigsyaml.JSONOpt
			if useNumber {
				opts = append(opts, func(d *json.Decoder) *json.Decoder { d.UseNumber(); return d })
				sopts = append(sopts, func(d *json.Decoder) *json.Decoder { d.UseNumber(); return d })
			}
			var want, got any
			werr := sigsyaml.Unmarshal(c.data, &want, sopts...)
			gerr := k8s.Unmarshal(c.data, &got, opts...)
			if werr == nil && gerr == nil {
				if !reflect.DeepEqual(want, got) && !k8sMisparse(c.name, k8s.YAMLToJSON, sigsyaml.YAMLToJSON) {
					t.Errorf("%s: values differ\n sigs: %.300v\n ours: %.300v", c.name, want, got)
				}
				checked++
				continue
			}
			if _, _, err := classify(c.name, nil, werr, nil, gerr); err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
		}
	}
	t.Logf("Unmarshal into interface{}: %d runs with equal values", checked)
}

// The kit's alias bomb is refused with a limit error. Sigs expands it to
// hundreds of megabytes of JSON, so it is not run here.
func TestK8sAliasBombIsRefused(t *testing.T) {
	kit := os.Getenv("ONEYAML_KIT")
	if kit == "" {
		t.Skip("ONEYAML_KIT is not set")
	}
	b, err := os.ReadFile(filepath.Join(kit, "suites/own/cases/safety/alias-byte-expansion-bomb/input.yaml"))
	if err != nil {
		t.Skip(err)
	}
	if _, err := k8s.YAMLToJSON(b); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("alias bomb: %v", err)
	}
	var v any
	if err := k8s.Unmarshal(b, &v); err == nil {
		t.Fatal("Unmarshal accepted the alias bomb")
	}
}

// The rule that excuses a difference as sigs reading valid YAML wrongly checks
// our output against what sigs makes of the kit's structure, so a wrong output of
// ours is not excused.
func TestK8sMisparseRuleChecksOurOutput(t *testing.T) {
	if os.Getenv("ONEYAML_KIT") == "" {
		t.Skip("ONEYAML_KIT is not set")
	}
	name := "suites/own/cases/parse/bom-then-comment-after-bare-document/input.yaml"
	if !k8sMisparse(name, k8s.YAMLToJSON, sigsyaml.YAMLToJSON) {
		t.Fatal("the difference on a valid input is not excused")
	}
	wrong := func([]byte) ([]byte, error) { return []byte(`"b"`), nil }
	if k8sMisparse(name, wrong, sigsyaml.YAMLToJSON) {
		t.Fatal("a wrong output was excused")
	}
}

// An input that sigs rejects is excused only when the kit holds it to be YAML.
func TestK8sKitValid(t *testing.T) {
	for name, want := range map[string]bool{
		"bench/workloads/real/gha-workflow/input.yaml":          true,
		ytsPrefix + "4ABK/in.yaml":                              true,
		"suites/own/cases/diagnostics/duplicate-key/input.yaml": false,
		"builtin/03": false,
		"suites/yaml-test-suite/data-2022-01-17/236B/in.yaml": false,
	} {
		if os.Getenv("ONEYAML_KIT") == "" {
			t.Skip("ONEYAML_KIT is not set")
		}
		if got := kitValid(name); got != want {
			t.Errorf("kitValid(%q) = %v, want %v", name, got, want)
		}
	}
}

// Negative zero follows sigs.k8s.io/yaml in both directions: a float -0 keeps its
// sign when written as YAML, and the text -0 (YAML or JSON) reads back as the
// integer 0.
func TestK8sNegativeZero(t *testing.T) {
	for _, in := range []string{"-.0", "-0.0", "-0", "-0e0", "x: [-0.00]", "[-.0, -0, 0.0]", "a: -.0"} {
		want, werr := sigsyaml.JSONToYAML([]byte(in))
		got, gerr := k8s.JSONToYAML([]byte(in))
		if (werr == nil) != (gerr == nil) || string(want) != string(got) {
			t.Errorf("JSONToYAML(%q): sigs %q (%v), ours %q (%v)", in, want, werr, got, gerr)
		}
		wj, _ := sigsyaml.YAMLToJSON(want)
		gj, _ := k8s.YAMLToJSON(got)
		if string(wj) != string(gj) {
			t.Errorf("YAMLToJSON of the output of %q: sigs %q, ours %q", in, wj, gj)
		}
	}
}
