package bench

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	sigsyaml "sigs.k8s.io/yaml"

	k8s "oneyaml.dev/go/compat/k8syaml"
)

// roundTrips reports whether yamlText reads back as the JSON j.
func roundTrips(yamlText, j []byte) bool {
	back, err := k8s.YAMLToJSON(yamlText)
	return err == nil && bytes.Equal(back, j)
}

// sigsRoundTrips is the same check with the reader behind sigs, which is the
// reader its users have.
func sigsRoundTrips(yamlText, j []byte) bool {
	back, err := sigsyaml.YAMLToJSON(yamlText)
	return err == nil && bytes.Equal(back, j)
}

// checkEmit compares our YAML for j with sigs'. They must be equal, unless sigs
// fails, or its output does not read back as j in one of the two readers (ours
// and the one behind sigs) while ours reads back in both: then the difference is
// intended.
func checkEmit(t *testing.T, name string, j []byte, st *emitStats) {
	t.Helper()
	want, werr := sigsyaml.JSONToYAML(j)
	got, gerr := k8s.JSONToYAML(j)
	switch {
	case gerr != nil:
		t.Errorf("%s: ours failed: %v\n json: %.300s", name, gerr, j)
	case werr != nil:
		st.reasons[strings.SplitN(werr.Error(), ":", 3)[len(strings.SplitN(werr.Error(), ":", 3))-1]]++
		if !roundTrips(got, j) {
			t.Errorf("%s: sigs fails (%v) and ours does not read back\n json: %.300s\n ours: %.300s", name, werr, j, got)
			return
		}
		st.sigsFails++
	case bytes.Equal(want, got):
		st.same++
	case (!sigsRoundTrips(want, j) || !roundTrips(want, j)) && roundTrips(got, j) && sigsRoundTrips(got, j):
		st.sigsLoses++
		if st.sample == "" {
			st.sample = string(j)
		}
	case oddBreakIn(j) && roundTrips(got, j) && sigsRoundTrips(got, j):
		st.oddBreak++
	default:
		t.Errorf("%s: output differs\n json: %.300s\n--- sigs\n%.600s\n--- ours\n%.600s", name, j, want, got)
	}
}

type emitStats struct {
	same, sigsFails, sigsLoses, oddBreak int
	sample                               string
	reasons                              map[string]int
}

// oddBreakIn reports a line break character other than "\n" in j: U+2028 and
// U+2029 (json.Marshal writes them as escapes) and U+0085 (written raw). A YAML
// 1.1 reader treats them as line breaks and a 1.2 reader does not, so a form
// that suits one misreads in the other. We write them as escapes, which both read.
func oddBreakIn(j []byte) bool {
	return bytes.Contains(j, []byte(`\u2028`)) || bytes.Contains(j, []byte(`\u2029`)) || bytes.Contains(j, []byte("\u0085")) || bytes.Contains(j, []byte{0xE2, 0x80, 0xA8}) || bytes.Contains(j, []byte{0xE2, 0x80, 0xA9})
}

func TestK8sDiffJSONToYAMLCorpus(t *testing.T) {
	st := emitStats{reasons: map[string]int{}}
	for _, c := range k8sCorpus(t) {
		j, err := sigsyaml.YAMLToJSON(c.data)
		if err != nil || len(j) > 1<<20 {
			continue
		}
		checkEmit(t, c.name, j, &st)
	}
	t.Logf("JSONToYAML on the kit's documents: %d byte-identical, %d where sigs fails and ours reads back, %d where sigs's output loses data and ours does not, %d with unusual line breaks", st.same, st.sigsFails, st.sigsLoses, st.oddBreak)
}

var genWords = []string{
	"", " ", "a", "b", "A", "z", "_x", "a1", "a2", "a10", "10", "9", "0", "00", "007", "1.5", "é", "日本語",
	"yes", "No", "on", "OFF", "y", "n", "true", "False", "null", "Null", "~", "1", "-1", "+1", "0x1F", "0b101", "0755", "0o17", "1_000",
	"1e3", ".5", ".inf", "-.inf", ".nan", "1:30", "12:30:45", "2001-12-14", "2001-12-14t21:59:43.10-05:00", "2001-12-14 21:59:43",
	"a b", "a  b", " a", "a ", "a: b", "a:b", "a #b", "a#b", "- a", "-a", "? a", "?a", "[a]", "{a}", "a,b", "&a", "*a", "!a", "|", "> a",
	"%a", "@a", "`a", "'a'", "\"a\"", "a'b", "a\"b", "---", "...", "--- a", "a\nb", "a\n", "\na", "\n", "a\n\nb", "a \nb", "a\n b",
	"\t", "a\tb", "\ta", "a\\b", "\\", "\u00a0", "\u2028", "\u2029", "\ufeff", "\ufeffabc", "\x00", "\x01", "\x7f", "\u0080", "\u009f",
	"\U0001F600", "x\U0001F600y", "\ufffd", "\ufffe", "\uffff", "=", "<", ">", "&", "<a>", "a&b", "/", "a/b",
	"The quick brown fox jumps over the lazy dog and keeps running through the forest until night falls over the hills",
	"The quick brown fox jumps over the lazy dog and keeps running through the forest until night falls over the hills\nsecond line is also fairly long so that it needs to be folded somewhere around here",
	strings.Repeat("word ", 40), strings.Repeat("x", 100), strings.Repeat("ab ", 50) + "end", strings.Repeat("k", 130),
	"key: value with: many: colons that run quite long and force wrapping of the double quoted form: yes indeed ok",
}

func genString(r *rand.Rand) string {
	if r.IntN(4) == 0 {
		return genWords[r.IntN(len(genWords))] + genWords[r.IntN(len(genWords))]
	}
	return genWords[r.IntN(len(genWords))]
}

func genValue(r *rand.Rand, depth int) any {
	k := r.IntN(12)
	if depth <= 0 && k > 7 {
		k -= 6
	}
	switch k {
	case 0:
		return nil
	case 1:
		return r.IntN(2) == 0
	case 2:
		return int64(r.IntN(2000) - 1000)
	case 3:
		return []float64{0, 1, -1, 1.5, 0.1, 1e21, 1e-7, 123456789.125, math.MaxFloat64, 5e-324, 100, 2.5e10}[r.IntN(12)]
	case 4:
		return []uint64{0, 1 << 53, 1<<53 + 1, math.MaxInt64, math.MaxUint64}[r.IntN(5)]
	case 5, 6, 7:
		return genString(r)
	case 8, 9:
		n := r.IntN(5)
		a := make([]any, n)
		for i := range a {
			a[i] = genValue(r, depth-1)
		}
		return a
	default:
		n := r.IntN(6)
		m := make(map[string]any, n)
		for i := 0; i < n; i++ {
			m[genString(r)] = genValue(r, depth-1)
		}
		return m
	}
}

func TestK8sDiffJSONToYAMLGenerated(t *testing.T) {
	n := 6000
	if testing.Short() {
		n = 2500
	}
	r := rand.New(rand.NewPCG(1, 2))
	st := emitStats{reasons: map[string]int{}}
	for i := 0; i < n; i++ {
		v := genValue(r, 4)
		j, err := json.Marshal(v)
		if err != nil {
			continue
		}
		checkEmit(t, "generated", j, &st)
		if t.Failed() {
			break
		}
	}
	t.Logf("sigs failures: %v", st.reasons)
	t.Logf("JSONToYAML on %d generated values: %d byte-identical, %d where sigs fails and ours reads back, %d where sigs's output loses data and ours does not (e.g. %.80s), %d with unusual line breaks", n, st.same, st.sigsFails, st.sigsLoses, st.sample, st.oddBreak)
}

// Every string of the generator alone, as a value and as a key, at the root, in
// a mapping and in a sequence.
func TestK8sDiffJSONToYAMLStrings(t *testing.T) {
	st := emitStats{reasons: map[string]int{}}
	for _, s := range genWords {
		for _, form := range []func(string) any{
			func(s string) any { return s },
			func(s string) any { return map[string]any{s: s} },
			func(s string) any { return []any{s, []any{s}} },
			func(s string) any { return map[string]any{"k": []any{map[string]any{s: []any{s}}}} },
		} {
			j, _ := json.Marshal(form(s))
			checkEmit(t, "string", j, &st)
		}
	}
	t.Logf("sigs failures: %v", st.reasons)
	t.Logf("%d byte-identical, %d sigs fails, %d sigs loses data, %d with unusual line breaks", st.same, st.sigsFails, st.sigsLoses, st.oddBreak)
}

type mPort struct {
	Name string `json:"name,omitempty"`
	Port int32  `json:"port"`
}

type mSpec struct {
	Replicas *int32            `json:"replicas,omitempty"`
	Ports    []mPort           `json:"ports,omitempty"`
	Labels   map[string]string `json:"labels"`
	Nested   map[string]any    `json:"nested,omitempty"`
	Raw      json.RawMessage   `json:"raw,omitempty"`
	When     time.Time         `json:"when"`
	Dur      time.Duration     `json:"dur"`
	Num      json.Number       `json:"num"`
	Q        kQuantity         `json:"q"`
	IOS      kIntOrString      `json:"ios"`
	Skip     string            `json:"-"`
	Empty    []string          `json:"empty"`
	NilMap   map[string]string `json:"nilMap"`
	F        float64           `json:"f"`
	Big      uint64            `json:"big"`
}

func TestK8sDiffMarshal(t *testing.T) {
	two := int32(2)
	pod := kPod{kTypeMeta: kTypeMeta{Kind: "Pod", APIVersion: "v1"}}
	if err := k8s.Unmarshal([]byte(k8sBuiltin[0]), &pod); err != nil {
		t.Fatal(err)
	}
	values := []any{
		pod,
		&pod,
		mSpec{Replicas: &two, Ports: []mPort{{"http", 80}, {"", 443}}, Labels: map[string]string{"b": "yes", "a": "1", "a10": "x", "a2": "y", "A": "z"},
			Nested: map[string]any{"z": 1, "y": []any{1, "two", 3.5, nil, true}}, Raw: json.RawMessage(`{"k":[1,2,{"x":null}]}`),
			When: time.Date(2020, 1, 2, 3, 4, 5, 600, time.UTC), Dur: time.Second, Num: "12345678901234567890", Q: kQuantity{"500m"}, IOS: kIntOrString{Str: "http"},
			F: 0.1, Big: math.MaxUint64},
		mSpec{IOS: kIntOrString{IsInt: true, Int: 8080}, F: 1e21, Num: "1.5"},
		map[string]any{"a": []any{}, "b": map[string]any{}, "c": []int(nil), "d": map[string]int(nil), "e": [][]int{{1, 2}, {3}}},
		map[int]string{1: "a", 10: "b", 2: "c"},
		[]any{"multi\nline\ntext", "trailing\n", "trailing\n\n", "  lead", "tab\t", 1.0, float32(0.1), int8(-3), uint16(7)},
		"scalar", 42, 3.5, true, nil, []string{}, map[string]string{},
		struct {
			A string `json:"a"`
			B string `json:"b,omitempty"`
			C *string
		}{A: "x"},
	}
	for i, v := range values {
		want, werr := sigsyaml.Marshal(v)
		got, gerr := k8s.Marshal(v)
		if (werr == nil) != (gerr == nil) {
			t.Errorf("value %d: errors differ: sigs %v, ours %v", i, werr, gerr)
			continue
		}
		if !bytes.Equal(want, got) {
			t.Errorf("value %d differs\n--- sigs\n%s\n--- ours\n%s", i, want, got)
		}
	}
	// Values encoding/json refuses.
	for _, v := range []any{make(chan int), math.NaN(), func() {}, map[string]any{"c": math.Inf(1)}} {
		_, werr := sigsyaml.Marshal(v)
		_, gerr := k8s.Marshal(v)
		if werr == nil || gerr == nil {
			t.Errorf("%T: sigs %v, ours %v: both should fail", v, werr, gerr)
		}
	}
}

// A key "<<" is a merge key to a reader of YAML 1.1, so it is written quoted.
// Sigs writes it plain; reading that back gives a different document.
func TestK8sMergeKeyIsQuoted(t *testing.T) {
	j, _ := json.Marshal(map[string]string{"<<": "x", "a": "<<"})
	got, err := k8s.JSONToYAML(j)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "\"<<\": x\na: \"<<\"\n" {
		t.Fatalf("ours: %q", got)
	}
	if !roundTrips(got, j) {
		t.Fatalf("ours does not read back: %q", got)
	}
	want, err := sigsyaml.JSONToYAML(j)
	if err != nil {
		t.Fatal(err)
	}
	if back, err := sigsyaml.YAMLToJSON(want); err == nil && string(back) == string(j) {
		t.Fatalf("sigs now reads its own output back (%q): drop the intended difference", want)
	}
}
