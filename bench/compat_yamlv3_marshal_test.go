package bench

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	yv3 "go.yaml.in/yaml/v3"
	oneyaml "oneyaml.dev/go"
	compat "oneyaml.dev/go/compat/yamlv3"
)

// ---- writing: values decoded from the corpus ----

var indicatorRE = regexp.MustCompile(`[|>][0-9]`)

// nodeShape lists a Node tree's kinds, values, anchors and alias targets in
// order, so two reads can be compared without positions or comments.
func nodeShape(n *compat.Node) []string {
	var out []string
	var walk func(n *compat.Node)
	walk = func(n *compat.Node) {
		if n == nil {
			return
		}
		out = append(out, fmt.Sprint(n.Kind, "|", n.Value, "|", n.Anchor))
		if n.Alias != nil {
			out = append(out, "->"+n.Alias.Anchor)
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(n)
	return out
}

// aliasKeyGapRE matches an alias key followed by a space and its colon.
var aliasKeyGapRE = regexp.MustCompile(`(\*[^\s,\[\]{}]+) :`)

// keepHeaderRE matches a block scalar header with keep chomping.
var keepHeaderRE = regexp.MustCompile(`(?m)[|>][1-9]?\+[^\n]*$`)

// writeDifference names the reason the shim's output may differ from yaml.v3's.
// A difference is intended only when yaml.v3's output is the wrong one:
//   - yaml.v3 cannot read its own output back, or
//   - yaml.v3 reads both outputs alike but a reader that follows the YAML 1.2
//     text reads yaml.v3's output as a different value than ours.
func writeDifference(v any, ybytes, cbytes []byte, yerr, cerr error) string {
	if yerr == nil && cerr != nil && strings.Contains(cerr.Error(), "cannot be written in a YAML comment") {
		// yaml.v3 copies the comment as it is; a YAML reader refuses the output.
		var x any
		if oneyaml.Unmarshal(ybytes, &x) != nil {
			return "yaml.v3 writes a comment with a character YAML does not allow (the output does not read back); the shim refuses it"
		}
		return ""
	}
	if yerr != nil || cerr != nil {
		return ""
	}
	if aliasKeyGapRE.ReplaceAllString(string(cbytes), "$1:") == string(ybytes) && string(cbytes) != string(ybytes) {
		// yaml.v3 writes an alias key right against its colon (`*a:`). A YAML 1.2
		// reader takes the colon as part of the alias name, so the shim writes `*a :`.
		// Read both with the shim's Node reader, which takes keys of any kind.
		var spec, ours compat.Node
		if compat.Unmarshal(cbytes, &ours) == nil && (compat.Unmarshal(ybytes, &spec) != nil || !reflect.DeepEqual(nodeShape(&spec), nodeShape(&ours))) {
			return "yaml.v3 writes an alias key against its colon, which a YAML 1.2 reader takes as part of the alias name; the shim writes a space before the colon"
		}
	}
	if strings.Contains(string(cbytes), "'<<'") && strings.ReplaceAll(string(cbytes), "'<<'", "<<") == string(ybytes) {
		return "yaml.v3 writes the key << unquoted, so it reads back as a merge key (or fails)"
	}
	var yy, yc, cy, cc any
	yyErr := yv3.Unmarshal(ybytes, &yy)
	ycErr := yv3.Unmarshal(cbytes, &yc)
	if ycErr != nil {
		return ""
	}
	if yyErr != nil {
		return "yaml.v3 writes text it cannot read back (wrong indentation indicator on a block scalar that starts with a space or a line break)"
	}
	if n, ok := v.(*yv3.Node); ok {
		// yaml.v3 puts a blank line between a keep-chomped block scalar and a comment
		// that follows it; the reader takes that line as part of the scalar, so the
		// value changes. The shim leaves it out and keeps the value.
		var orig any
		if n.Decode(&orig) == nil && !compatSame(orig, yy) {
			// Ours reads back to the value with yaml.v3's reader or with a reader that
			// follows the YAML 1.2 text (a top-level indicator differs between them).
			var spec any
			if compatSame(orig, yc) || (oneyaml.Unmarshal(cbytes, &spec) == nil && compatSame(orig, spec)) {
				if !keepHeaderRE.Match(ybytes) {
					return "yaml.v3 writes a blank line in folded text before a line that starts with white space, or before the trailing breaks of a value that starts with a line break; its reader takes the line as text (its output reads back as a different value)"
				}
				return "yaml.v3 writes a blank line before a trailing comment that a keep-chomped block scalar takes as text (its output reads back as a different value)"
			}
		}
	}
	if compatSame(yy, yc) && indicatorRE.ReplaceAllString(string(ybytes), "|") == indicatorRE.ReplaceAllString(string(cbytes), "|") {
		return "yaml.v3 writes a wrong indentation indicator digit on a block scalar (the text is blank, so both read alike)"
	}
	if indicatorRE.ReplaceAllString(string(ybytes), "|") == indicatorRE.ReplaceAllString(string(cbytes), "|") {
		var spec any
		if oneyaml.Unmarshal(cbytes, &spec) == nil && yyErr == nil && compatSame(spec, yy) {
			return "yaml.v3 reads the indentation indicator of a top-level block scalar from column 0; the specification counts from -1, and the shim follows it"
		}
	}
	cyErr := oneyaml.Unmarshal(ybytes, &cy)
	ccErr := oneyaml.Unmarshal(cbytes, &cc)
	if ccErr == nil && (cyErr != nil || !compatSame(cy, cc)) && compatSame(yy, yc) {
		return "yaml.v3 writes text a YAML 1.2 reader reads differently (indentation after U+2028 or U+2029 in a quoted scalar, or an indentation indicator on a top-level block scalar)"
	}
	return ""
}

func compareMarshal(tally *diffTally, name string, v any) {
	yb, yerr := yv3.Marshal(v)
	cb, cerr := compat.Marshal(v)
	switch {
	case yerr != nil && cerr != nil:
		tally.bothErr++
	case yerr == nil && cerr == nil && bytes.Equal(yb, cb):
		tally.same++
	default:
		if r := writeDifference(v, yb, cb, yerr, cerr); r != "" {
			tally.note(r, name)
			return
		}
		tally.unexplained = append(tally.unexplained, fmt.Sprintf("%s: yaml.v3 err=%v, shim err=%v\n%s", name, yerr, cerr, firstDiff(yb, cb)+readNote(yb, cb)+fullIf(yb, cb)))
	}
}

// firstDiff shows the lines around the first difference of two outputs.
func firstDiff(a, b []byte) string {
	al, bl := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	i := 0
	for i < len(al) && i < len(bl) && al[i] == bl[i] {
		i++
	}
	show := func(l []string) string {
		lo, hi := max(i-1, 0), min(i+3, len(l))
		if lo > hi {
			lo = hi
		}
		return strings.Join(l[lo:hi], "\n    | ")
	}
	return fmt.Sprintf("  line %d\n  v3  | %s\n  me  | %s", i+1, show(al), show(bl))
}

func TestCompatV3MarshalCorpus(t *testing.T) {
	tally := newTally("marshal-corpus")
	for _, it := range compatCorpus(t) {
		var v any
		if yv3.Unmarshal(it.data, &v) != nil {
			continue
		}
		compareMarshal(tally, it.name, v)
	}
	tally.report(t)
}

// ---- typed values ----

type v3Inner struct {
	A int
	B string `yaml:"bee"`
}

type v3All struct {
	S        string
	I        int
	I8       int8
	U        uint
	U8       uint8
	F        float64
	F32      float32
	B        bool
	P        *int
	PS       *string
	PI       *v3Inner
	Sl       []int
	SS       []string
	Arr      [3]int
	M        map[string]int
	MI       map[int]string
	MA       map[string]any
	Any      any
	T        time.Time
	D        time.Duration
	By       []byte
	Inl      v3Inner           `yaml:",inline"`
	Flow     []int             `yaml:"flow,flow"`
	FlowM    map[string]string `yaml:"flowm,flow"`
	Om       string            `yaml:",omitempty"`
	OmS      []string          `yaml:",omitempty"`
	OmP      *int              `yaml:",omitempty"`
	OmT      time.Time         `yaml:",omitempty"`
	OmSt     v3Inner           `yaml:",omitempty"`
	Skip     string            `yaml:"-"`
	unexport int
}

var trickyStrings = []string{
	"", "a", "yes", "No", "on", "OFF", "y", "N", "true", "False", "null", "~", "0777", "0o17", "0b11", "1_000", "1e3", "1.5",
	".inf", "-.inf", ".nan", "2001-01-01", "2001-01-01T10:00:00Z", " lead", "trail ", "a: b", "a #b", "#x", "- x", "? x", "-", "--- x",
	"multi\nline", "multi\nline\n", "\nlead", "two\n\nbreaks\n", "tab\there", "emoji \U0001F600", "caf\u00e9", "q'uote", "d\"quote", "back\\slash",
	"{x}", "[x]", "a, b", "@at", "`tick", "%pct", "!bang", "&amp", "*star", "|pipe", ">gt", "<<", "1:30", "1:30:00", "+1", "-1", "0x1F", "12345678901234567890",
	"a:b", "a:", ":a", "http://x.y/z", "x\ry", "bell\a", "nul\x00", "nel\u0085x", "ls\u2028x", "bom\ufeffx", "\x80bad", "long " + strings.Repeat("word ", 40),
	"trailing space \n", "x \ny", "x\n y", " x\ny", "k: v\nk2: v2\n",
}

// safeStrings are the tricky strings without the four traits that make yaml.v3's
// output wrong (leading-space multi-line text, U+2028, a "<<" key, digit runs
// that overflow its key order), so that a difference in a random document is
// never explained away by one of them. They are tested alone in the scalar test.
var safeStrings = func() []string {
	var out []string
	for _, s := range trickyStrings {
		switch {
		case s == "<<", s == "12345678901234567890", strings.ContainsRune(s, '\u2028'):
		case strings.Contains(s, "\n") && (s[0] == ' ' || s[0] == '\n' || strings.Contains(s, "\n ")):
		default:
			out = append(out, s)
		}
	}
	return out
}()

func randAll(r *rand.Rand) *v3All {
	str := func() string { return safeStrings[r.Intn(len(safeStrings))] }
	a := &v3All{S: str(), I: r.Intn(2000) - 1000, I8: int8(r.Intn(256) - 128), U: uint(r.Intn(1 << 20)), U8: uint8(r.Intn(256)),
		B: r.Intn(2) == 0}
	switch r.Intn(5) {
	case 0:
		a.F = float64(r.Intn(1000)) / 8
	case 1:
		a.F = math.Inf(1 - 2*r.Intn(2))
	case 2:
		a.F = r.NormFloat64() * 1e10
	case 3:
		a.F = math.NaN()
	}
	a.F32 = float32(r.NormFloat64())
	if r.Intn(2) == 0 {
		x := r.Intn(100)
		a.P = &x
	}
	if r.Intn(2) == 0 {
		s := str()
		a.PS = &s
	}
	if r.Intn(2) == 0 {
		a.PI = &v3Inner{A: r.Intn(10), B: str()}
	}
	if r.Intn(3) != 0 {
		for i := r.Intn(4); i >= 0; i-- {
			a.Sl = append(a.Sl, r.Intn(100))
		}
	}
	if r.Intn(3) != 0 {
		for i := r.Intn(4); i >= 0; i-- {
			a.SS = append(a.SS, str())
		}
	}
	a.Arr = [3]int{r.Intn(5), r.Intn(5), r.Intn(5)}
	if r.Intn(3) != 0 {
		a.M = map[string]int{}
		for i := r.Intn(5); i >= 0; i-- {
			a.M[str()] = r.Intn(10)
		}
	}
	if r.Intn(3) != 0 {
		a.MI = map[int]string{}
		for i := r.Intn(5); i >= 0; i-- {
			a.MI[r.Intn(30)-5] = str()
		}
	}
	if r.Intn(3) != 0 {
		a.MA = map[string]any{}
		for i := r.Intn(4); i >= 0; i-- {
			a.MA[str()] = randAny(r, 3)
		}
	}
	a.Any = randAny(r, 3)
	if r.Intn(2) == 0 {
		a.T = time.Date(2000+r.Intn(30), time.Month(1+r.Intn(12)), 1+r.Intn(28), r.Intn(24), r.Intn(60), r.Intn(60), r.Intn(2)*123456789, time.UTC)
	}
	a.unexport = r.Intn(3)
	a.D = time.Duration(r.Intn(100000)) * time.Millisecond
	if r.Intn(2) == 0 {
		a.By = []byte(str())
	}
	a.Inl = v3Inner{A: r.Intn(3), B: str()}
	if r.Intn(2) == 0 {
		a.Flow = []int{1, 2, r.Intn(9)}
	}
	if r.Intn(2) == 0 {
		a.FlowM = map[string]string{"k": str(), str(): str()}
	}
	if r.Intn(2) == 0 {
		a.Om = str()
	}
	if r.Intn(2) == 0 {
		a.OmS = []string{str()}
	}
	if r.Intn(2) == 0 {
		x := r.Intn(3)
		a.OmP = &x
	}
	if r.Intn(2) == 0 {
		a.OmT = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	}
	if r.Intn(2) == 0 {
		a.OmSt = v3Inner{A: r.Intn(2)}
	}
	return a
}

func randAny(r *rand.Rand, depth int) any {
	str := func() string { return safeStrings[r.Intn(len(safeStrings))] }
	k := r.Intn(9)
	if depth == 0 && k >= 6 {
		k = r.Intn(6)
	}
	switch k {
	case 0:
		return nil
	case 1:
		return str()
	case 2:
		return r.Intn(1000) - 500
	case 3:
		return r.Intn(2) == 0
	case 4:
		return float64(r.Intn(100)) / 4
	case 5:
		return int64(r.Intn(1<<40)) * 1000
	case 6:
		var l []any
		for i := r.Intn(4); i > 0; i-- {
			l = append(l, randAny(r, depth-1))
		}
		return l
	case 7:
		m := map[string]any{}
		for i := r.Intn(4); i > 0; i-- {
			m[str()] = randAny(r, depth-1)
		}
		return m
	}
	m := map[any]any{}
	for i := r.Intn(4); i > 0; i-- {
		var key any
		switch r.Intn(4) {
		case 0:
			key = r.Intn(20)
		case 1:
			key = r.Intn(2) == 0
		case 2:
			key = float64(r.Intn(5)) / 2
		default:
			key = str()
		}
		m[key] = randAny(r, depth-1)
	}
	return m
}

func TestCompatV3MarshalTyped(t *testing.T) {
	tally := newTally("marshal-typed")
	r := rand.New(rand.NewSource(7))
	n := 3000
	if testing.Short() {
		n = 300
	}
	for i := 0; i < n; i++ {
		a := randAll(r)
		compareMarshal(tally, fmt.Sprintf("all#%d", i), a)
		compareMarshal(tally, fmt.Sprintf("all-ptr#%d", i), &a)
		compareMarshal(tally, fmt.Sprintf("any#%d", i), randAny(r, 4))
		compareMarshal(tally, fmt.Sprintf("seq#%d", i), []any{a.Any, a.S, a.I})
	}
	tally.report(t)
}

// TestCompatV3MarshalScalars writes each tricky string alone, as a map value, as
// a key, in a sequence and in flow style.
func TestCompatV3MarshalScalars(t *testing.T) {
	tally := newTally("marshal-scalars")
	for i, s := range trickyStrings {
		compareMarshal(tally, fmt.Sprintf("str#%d %q", i, s), s)
		compareMarshal(tally, fmt.Sprintf("in-map#%d %q", i, s), map[string]string{"k": s})
		compareMarshal(tally, fmt.Sprintf("as-key#%d %q", i, s), map[string]int{s: 1})
		compareMarshal(tally, fmt.Sprintf("in-seq#%d %q", i, s), []string{s, s})
		compareMarshal(tally, fmt.Sprintf("flow#%d %q", i, s), struct {
			F []string `yaml:"f,flow"`
		}{[]string{s}})
		compareMarshal(tally, fmt.Sprintf("nested#%d %q", i, s), map[string]any{"a": []any{map[string]any{"b": s}, []any{s}}})
	}
	for _, f := range []float64{0, 1, -1, 0.1, 1e6, 1e21, 1e-7, 123456789.123, math.MaxFloat64, math.SmallestNonzeroFloat64, math.Inf(1), math.Inf(-1), math.NaN()} {
		compareMarshal(tally, fmt.Sprintf("float %v", f), f)
		compareMarshal(tally, fmt.Sprintf("float32 %v", f), float32(f))
	}
	for _, v := range []any{nil, true, false, 0, -7, int8(5), uint64(math.MaxUint64), int64(math.MinInt64), "", []int(nil), map[string]int(nil), []any{}, map[string]any{},
		struct{}{}, &struct{ A int }{}, (*int)(nil), [2]int{1, 2}, []byte("hi"), []byte(nil), time.Duration(90 * time.Second), time.Time{},
		time.Date(2001, 2, 3, 4, 5, 6, 7, time.FixedZone("x", 3600)), map[string][]int{"a": {1}, "b": nil}, [][]int{{1}, {2, 3}}, []*int{nil}} {
		compareMarshal(tally, fmt.Sprintf("value %T %v", v, v), v)
	}
	tally.report(t)
}

// fullIf appends both outputs when they are short enough to read.
func fullIf(a, b []byte) string {
	if len(a) > 700 || len(b) > 700 {
		return ""
	}
	return fmt.Sprintf("\n--v3--\n%s--me--\n%s", a, b)
}

// readNote says what yaml.v3 makes of each output.
func readNote(yb, cb []byte) string {
	var a, b any
	return fmt.Sprintf("\n  v3 reads v3: %v; v3 reads mine: %v", yv3.Unmarshal(yb, &a), yv3.Unmarshal(cb, &b))
}
