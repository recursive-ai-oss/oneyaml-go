package bench

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	yv3 "go.yaml.in/yaml/v3"
	compat "oneyaml.dev/go/compat/yamlv3"
)

// ---- reading into typed targets ----

type typedTarget struct {
	name string
	mk   func() any
}

type v3Embedded struct {
	X int
	Y string `yaml:"why"`
}

type v3Tags struct {
	Plain   string
	Renamed int            `yaml:"other"`
	Dash    int            `yaml:"-"`
	OmitE   int            `yaml:"omit,omitempty"`
	In      v3Embedded     `yaml:",inline"`
	PIn     *v3Embedded    `yaml:",inline"`
	Extra   map[string]any `yaml:",inline"`
}

type v3PtrInline struct {
	A int
	P *v3Embedded `yaml:",inline"`
}

type v3Embeds struct {
	v3Embedded
	Z int
}

type v3Plain struct {
	Name string
	Age  int
	Tags []string
}

type v3Num struct {
	A int
	B int64
	C uint
	D float64
	E bool
	F string
	G any
	H *int
	I []byte
	J time.Time
	K time.Duration
	L map[string]string
	M [2]int
}

var typedTargets = []typedTarget{
	{"any", func() any { return new(any) }},
	{"map[string]any", func() any { return new(map[string]any) }},
	{"map[any]any", func() any { return new(map[any]any) }},
	{"[]any", func() any { return new([]any) }},
	{"map[string]string", func() any { return new(map[string]string) }},
	{"map[string]int", func() any { return new(map[string]int) }},
	{"map[int]string", func() any { return new(map[int]string) }},
	{"[]int", func() any { return new([]int) }},
	{"[]string", func() any { return new([]string) }},
	{"[2]int", func() any { return new([2]int) }},
	{"string", func() any { return new(string) }},
	{"int", func() any { return new(int) }},
	{"int8", func() any { return new(int8) }},
	{"uint", func() any { return new(uint) }},
	{"float64", func() any { return new(float64) }},
	{"bool", func() any { return new(bool) }},
	{"*int", func() any { return new(*int) }},
	{"time.Time", func() any { return new(time.Time) }},
	{"time.Duration", func() any { return new(time.Duration) }},
	{"v3Plain", func() any { return new(v3Plain) }},
	{"v3Num", func() any { return new(v3Num) }},
	{"v3Tags", func() any { return new(v3Tags) }},
	{"v3Embeds", func() any { return new(v3Embeds) }},
	{"v3PtrInline", func() any { return new(v3PtrInline) }},
	{"v3All", func() any { return new(v3All) }},
	{"[]v3Plain", func() any { return new([]v3Plain) }},
	{"map[string]v3Plain", func() any { return new(map[string]v3Plain) }},
}

var typedDocs = []string{
	"name: x\nage: 3\ntags: [a, b]\n",
	"name: 5\nage: three\ntags: x\n",
	"name: yes\nage: 3.0\n",
	"age: 3.5\n",
	"age: 0x10\n",
	"age: 0777\n",
	"age: 1_000\n",
	"age: -5\n",
	"age: 99999999999999999999\n",
	"age: ~\nname: null\ntags: ~\n",
	"a: 1\nb: 2\nc: 3\nd: 1.5\ne: yes\nf: hello\ng: [1, 2]\nh: 5\ni: aGk=\nj: 2001-01-01\nk: 1h30m\nl: {a: b}\nm: [1, 2]\n",
	"a: 1\nb: 2\nc: -3\nd: 1e3\ne: on\nf: 12\ng: {x: 1}\nh: ~\nj: 2001-02-03T04:05:06Z\nk: 90\nl: [1]\nm: [1, 2, 3]\n",
	"e: Yes\n", "e: y\n", "e: NO\n", "e: off\n", "e: 1\n", "e: 'true'\n", "e: \"on\"\n",
	"j: 2001-01-01 10:00:00\n", "j: \"2001-01-01T10:00:00Z\"\n", "j: 2001-1-1\n", "j: 2001-01-01t10:00:00+01:00\n", "j: nope\n",
	"k: 1h\n", "k: 1.5\n", "k: \"90s\"\n", "k: 90\n",
	"i: !!binary aGk=\n", "i: !!binary '***'\n", "i: [104, 105]\n", "i: hi\n",
	"m: [1]\n", "m: [1, 2, 3]\n", "m: [1, 2]\n",
	"plain: p\nother: 4\ndash: 9\nomit: 3\nx: 1\nwhy: w\nextra: e\nmore: {k: v}\n",
	"x: 1\nwhy: w\nz: 3\nunknown: 4\n",
	"- name: a\n  age: 1\n- name: b\n  age: x\n",
	"first: {name: a}\nsecond: {age: 2}\n",
	"1: a\n2: b\n", "a: 1\n", "[1, 2, 3]", "[a, b]", "- 1\n- x\n- 3\n", "3", "3.5", "-.inf", ".nan", "hello", "'quoted'", "yes", "~", "", "true",
	"a: &x [1, 2]\nb: *x\n", "base: &b {name: n, age: 4}\nuse:\n  <<: *b\n  age: 5\n", "<<: {name: m}\nage: 7\n",
	"a: 1\na: 2\n", "? [a]\n: b\n", "{a: 1, b: [x, y]}", "!!str 5", "!!int '7'", "!!float 2", "!!bool yes",
	"0b101", "0o17", "+5", ".5", "1e2", "1:30", "2001-01-01", "2001-01-01T01:01:01Z",
	"name: [a]\n", "name: {a: b}\n", "age: [1]\n", "tags: {a: b}\n", "tags: [[a]]\n",
	"A: 1\nNAME: x\n", "age: 4\nage: 5\n", "[1, null, 2]", "[a, ~, b]", "m: [1, 2, 3, 4]", "m: []", "tags: [a, null]\nage: [1, null]\n",
}

func TestCompatV3UnmarshalTyped(t *testing.T) {
	tally := newTally("unmarshal-typed")
	msgDiff := 0
	var msgExamples []string
	for _, tg := range typedTargets {
		for i, doc := range typedDocs {
			name := fmt.Sprintf("%s <- doc#%d %q", tg.name, i, truncate(doc, 40))
			compareRead(tally, name, doc, tg.mk, &msgDiff, &msgExamples)
		}
	}
	tally.report(t)
	t.Logf("typed error messages that differ: %d", msgDiff)
	for i, m := range msgExamples {
		if i >= 40 {
			break
		}
		t.Logf("  %s", m)
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// typedDifference lists where the shim's typed read differs from yaml.v3's on
// purpose.
var floatIntoInt = regexp.MustCompile(`cannot unmarshal !!float .* into u?int`)

func typedDifference(doc string, tgName string, ya, ca any, yerr, cerr error) string {
	var p errPanic
	switch {
	case errors.As(yerr, &p):
		return "yaml.v3 panics (the shim returns an error or a value)"
	case yerr == nil && cerr != nil && floatIntoInt.MatchString(cerr.Error()):
		return "yaml.v3 truncates a float into an integer target (3.5 becomes 3, -.inf becomes the smallest int); the shim reports a type error unless the float has no fraction"
	case yerr == nil && cerr == nil && (strings.Contains(doc, "null") || strings.Contains(doc, "~")) &&
		strings.Contains(valueDiff(reflect.ValueOf(ya).Elem(), reflect.ValueOf(ca).Elem(), "", 0), "len "):
		return "yaml.v3 drops a null element from a slice of values that cannot be null; the shim leaves a zero value in its place"
	case yerr != nil && cerr == nil && strings.Contains(yerr.Error(), "already defined"):
		return "the shim does not report a repeated key in a mapping the target ignores (yaml.v3 does)"
	}
	return ""
}

func compareRead(tally *diffTally, name, doc string, mk func() any, msgDiff *int, msgs *[]string) {
	ya, ca := mk(), mk()
	yerr := safeV3(func() error { return yv3.Unmarshal([]byte(doc), ya) })
	cerr := compat.Unmarshal([]byte(doc), ca)
	var yte *yv3.TypeError
	var cte *compat.TypeError
	yIsType, cIsType := errors.As(yerr, &yte), errors.As(cerr, &cte)
	switch {
	case yerr != nil && cerr != nil:
		tally.bothErr++
		if yIsType && cIsType && !reflect.DeepEqual(yte.Errors, cte.Errors) {
			*msgDiff++
			*msgs = append(*msgs, fmt.Sprintf("%s:\n    v3: %q\n    me: %q", name, yte.Errors, cte.Errors))
		}
	case yerr == nil && cerr == nil && compatSame(reflect.ValueOf(ya).Elem().Interface(), reflect.ValueOf(ca).Elem().Interface()):
		tally.same++
	default:
		if r := typedDifference(doc, name, ya, ca, yerr, cerr); r != "" {
			tally.note(r, name)
			return
		}
		tally.unexplained = append(tally.unexplained, fmt.Sprintf("%s: v3 err=%.200v | me err=%.200v | first difference: %s", name, yerr, cerr, valueDiff(reflect.ValueOf(ya).Elem(), reflect.ValueOf(ca).Elem(), "", 0)))
	}
}

// TestCompatV3UnmarshalRoundTrip reads the output of yaml.v3 for random typed
// values back into the same type with both libraries.
func TestCompatV3UnmarshalRoundTrip(t *testing.T) {
	tally := newTally("unmarshal-roundtrip")
	var msgDiff int
	var msgs []string
	r := rand.New(rand.NewSource(11))
	n := 1500
	if testing.Short() {
		n = 150
	}
	for i := 0; i < n; i++ {
		a := randAll(r)
		doc, err := yv3.Marshal(a)
		if err != nil {
			continue
		}
		compareRead(tally, fmt.Sprintf("roundtrip#%d", i), string(doc), func() any { return new(v3All) }, &msgDiff, &msgs)
		compareRead(tally, fmt.Sprintf("roundtrip-any#%d", i), string(doc), func() any { return new(any) }, &msgDiff, &msgs)
	}
	tally.report(t)
}

var _ = strings.Contains

// errPanic is what safeV3 returns for a panic in yaml.v3, which has several: a
// duplicated key in a struct's tags, a type it cannot write, an unsettable target.
type errPanic struct{ v any }

func (e errPanic) Error() string { return fmt.Sprint("yaml.v3 panicked: ", e.v) }

func safeV3(f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errPanic{r}
		}
	}()
	return f()
}

// valueDiff describes the first place two decoded values differ.
func valueDiff(a, b reflect.Value, path string, depth int) string {
	if depth > 50 {
		return ""
	}
	short := func(v reflect.Value) string {
		if !v.IsValid() {
			return "<invalid>"
		}
		return fmt.Sprintf("%.60q", fmt.Sprintf("%#v", v.Interface()))
	}
	if !a.IsValid() || !b.IsValid() {
		return fmt.Sprintf("%s: %s vs %s", path, short(a), short(b))
	}
	if a.Type() != b.Type() {
		return fmt.Sprintf("%s: type %s vs %s", path, a.Type(), b.Type())
	}
	switch a.Kind() {
	case reflect.Interface, reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				return fmt.Sprintf("%s: nil %v vs %v", path, a.IsNil(), b.IsNil())
			}
			return ""
		}
		return valueDiff(a.Elem(), b.Elem(), path, depth+1)
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return fmt.Sprintf("%s: len %d vs %d", path, a.Len(), b.Len())
		}
		for i := 0; i < a.Len(); i++ {
			if d := valueDiff(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i), depth+1); d != "" {
				return d
			}
		}
	case reflect.Map:
		if a.IsNil() != b.IsNil() {
			return fmt.Sprintf("%s: nil map %v vs %v", path, a.IsNil(), b.IsNil())
		}
		for _, k := range a.MapKeys() {
			bv := b.MapIndex(k)
			if !bv.IsValid() {
				return fmt.Sprintf("%s: key %v missing in shim", path, k)
			}
			if d := valueDiff(a.MapIndex(k), bv, fmt.Sprintf("%s[%v]", path, k), depth+1); d != "" {
				return d
			}
		}
		if a.Len() != b.Len() {
			return fmt.Sprintf("%s: shim has extra keys", path)
		}
	case reflect.Struct:
		if a.Type() == reflect.TypeOf(time.Time{}) {
			if !a.Interface().(time.Time).Equal(b.Interface().(time.Time)) {
				return fmt.Sprintf("%s: %v vs %v", path, a.Interface(), b.Interface())
			}
			return ""
		}
		for i := 0; i < a.NumField(); i++ {
			if a.Type().Field(i).PkgPath != "" {
				continue
			}
			if d := valueDiff(a.Field(i), b.Field(i), path+"."+a.Type().Field(i).Name, depth+1); d != "" {
				return d
			}
		}
	default:
		if !compatDeepEq(a, b, 0) {
			return fmt.Sprintf("%s: %s vs %s", path, short(a), short(b))
		}
	}
	return ""
}
