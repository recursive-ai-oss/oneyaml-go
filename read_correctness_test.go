package oneyaml_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"oneyaml.dev/go"
)

// syntaxAt asserts that err is a *SyntaxError of the given category at the
// given line and column, and returns it.
func syntaxAt(t *testing.T, what string, err error, cat oneyaml.Category, line, col int) *oneyaml.SyntaxError {
	t.Helper()
	var se *oneyaml.SyntaxError
	if !errors.As(err, &se) {
		t.Errorf("%s: got %T %v, want a syntax error", what, err, err)
		return nil
	}
	if se.Category != cat || se.Line != line || se.Column != col {
		t.Errorf("%s: got category %q at %d:%d (%s), want %q at %d:%d", what, se.Category, se.Line, se.Column, se.Msg, cat, line, col)
	}
	return se
}

// readers runs the same input through every way of reading it into a value and
// returns the errors by name.
func readers(in string) map[string]error {
	out := map[string]error{}
	var a any
	out["Unmarshal any"] = oneyaml.Unmarshal([]byte(in), &a)
	var s struct{ B int }
	out["Unmarshal struct"] = oneyaml.Unmarshal([]byte(in), &s)
	var m map[string]any
	out["Unmarshal map"] = oneyaml.Unmarshal([]byte(in), &m)
	var d any
	out["Decoder"] = oneyaml.NewDecoder(strings.NewReader(in)).Decode(&d)
	doc, err := oneyaml.Parse([]byte(in))
	out["Parse"] = err
	if err == nil {
		var x any
		out["Document.Decode"] = doc.Decode(&x)
	}
	return out
}

// A double-quoted escape that names no character is refused where the scalar is
// read, by every way of reading, at the backslash, whether or not the scalar's
// value is ever used.
func TestInvalidEscapesAreRefusedEverywhere(t *testing.T) {
	for _, c := range []struct {
		name, in  string
		line, col int
		cat       oneyaml.Category
	}{
		{"lone high surrogate", "a: \"\\ud800\"\n", 1, 5, "syntax"},
		{"lone low surrogate", "a: \"\\udc00\"\n", 1, 5, "syntax"},
		{"surrogate then letter", "a: \"\\ud800\\u0041\"\n", 1, 5, "syntax"},
		{"last surrogate", "a: \"\\udfff\"\n", 1, 5, "syntax"},
		{"above the last code point", "a: \"\\U00110000\"\n", 1, 5, "syntax"},
		{"far above", "a: \"\\UFFFFFFFF\"\n", 1, 5, "syntax"},
		{"surrogate as eight digits", "a: \"\\U0000D800\"\n", 1, 5, "syntax"},
		{"escape after text", "a: \"ab\\ud800\"\n", 1, 7, "syntax"},
		{"second escape", "a: \"\\u0041\\ud800\"\n", 1, 11, "syntax"},
		{"in a key", "\"\\ud800\": 1\n", 1, 2, "syntax"},
		{"on a later line", "a: \"x\n  \\ud800\"\n", 2, 3, "syntax"},
		{"in a flow sequence", "[\"\\ud800\"]\n", 1, 3, "syntax"},
		{"unknown letter", "a: \"\\q\"\n", 1, 6, "syntax"},
		{"short hex escape", "a: \"\\x4\"\n", 1, 8, "syntax"},
		{"short four digit escape", "a: \"\\u12\"\n", 1, 9, "syntax"},
		{"short eight digit escape", "a: \"\\U0000123\"\n", 1, 14, "syntax"},
		{"non hex digit", "a: \"\\u00g1\"\n", 1, 9, "syntax"},
	} {
		for name, err := range readers(c.in) {
			syntaxAt(t, c.name+" via "+name, err, c.cat, c.line, c.col)
		}
	}
}

func TestValidEscapesStillRead(t *testing.T) {
	for in, want := range map[string]string{
		`"\ud7ff"`:       "\ud7ff",
		`"\ue000"`:       "\ue000",
		`"\U0010FFFF"`:   "\U0010FFFF",
		`"\U00010000"`:   "\U00010000",
		`"\u0000"`:       "\x00",
		`"\x41\u00e9"`:   "Aé",
		`"\uFFFD\uFFFF"`: "\uFFFD\uFFFF",
	} {
		doc, err := oneyaml.Parse([]byte("v: " + in + "\n"))
		if err != nil {
			t.Errorf("%s: Parse: %v", in, err)
			continue
		}
		var got struct{ V string }
		if err := doc.Decode(&got); err != nil || got.V != want {
			t.Errorf("%s: got %q, %v, want %q", in, got.V, err, want)
		}
	}
}

// A syntax error later in the stream is reported even when the root value did
// not fit its target: the document is not valid whatever the target is.
func TestSyntaxErrorAfterATypeErrorWins(t *testing.T) {
	type nested struct{ A []int }
	for _, c := range []struct {
		name, in  string
		target    any
		line, col int
	}{
		{"sequence into struct", "[x, 1]\n]\n", new(struct{ B int }), 2, 1},
		{"sequence into slice of ints", "[x, 1]\n]\n", new([]int), 2, 1},
		{"mapping into slice", "a: 1\n]\n", new([]int), 2, 2},
		{"nested mismatch", "a: [x]\nb: 1\n]\n", new(nested), 3, 2},
		{"flow mapping", "{a: x}\n}\n", new([]string), 2, 1},
	} {
		err := oneyaml.Unmarshal([]byte(c.in), c.target)
		syntaxAt(t, c.name+" Unmarshal", err, "syntax", c.line, c.col)
		err = oneyaml.NewDecoder(strings.NewReader(c.in)).Decode(c.target)
		syntaxAt(t, c.name+" Decoder", err, "syntax", c.line, c.col)
		_, perr := oneyaml.Parse([]byte(c.in))
		syntaxAt(t, c.name+" Parse", perr, "syntax", c.line, c.col)
	}
	// Without the later fault the mismatch is still a type error.
	var s struct{ B int }
	var te *oneyaml.TypeError
	if err := oneyaml.Unmarshal([]byte("[x, 1]\n"), &s); !errors.As(err, &te) {
		t.Errorf("got %T %v, want a type error", err, err)
	}
}

func nanCase(t *testing.T, in string) {
	t.Helper()
	for name, err := range readers(in) {
		if name == "Parse" || name == "Unmarshal map" {
			continue // the tree holds keys as written; reading judges them. A map with string keys holds the text
		}
		var se *oneyaml.SyntaxError
		if !errors.As(err, &se) || se.Category != oneyaml.CategoryDuplicateKey {
			t.Errorf("%q via %s: got %v, want a duplicate key", in, name, err)
		}
	}
}

// NaN is not equal to itself, so a Go map would keep every .nan key. They are
// one YAML value, and a repeat is a duplicate like any other.
func TestNaNKeysAreDuplicates(t *testing.T) {
	nanCase(t, "{.nan: 1, .nan: 2}\n")
	nanCase(t, ".nan: 1\n.nan: 2\n")
	nanCase(t, ".nan: 1\n.NaN: 2\n")
	nanCase(t, ".NAN: 1\nx: 0\n.nan: 2\n")
	nanCase(t, "!!float .nan: 1\n.nan: 2\n")
	for _, in := range []string{".nan: 1\n.nan: 2\n", "{.nan: 1, .NaN: 2}\n", ".nan: 1\n.NaN: 2\n"} {
		for _, target := range []any{new(map[float64]string), new(map[float32]string), new(map[any]string), new(map[any]any)} {
			err := oneyaml.Unmarshal([]byte(in), target)
			var se *oneyaml.SyntaxError
			if !errors.As(err, &se) || se.Category != oneyaml.CategoryDuplicateKey {
				t.Errorf("%q into %T: got %v, want a duplicate key", in, target, err)
			}
		}
	}
	// The error is at the second key, and names the first.
	var a any
	se := syntaxAt(t, "position", oneyaml.Unmarshal([]byte("x: 0\n.nan: 1\n.nan: 2\n"), &a), oneyaml.CategoryDuplicateKey, 3, 1)
	if se != nil && (se.FirstLine != 2 || se.FirstColumn != 1) {
		t.Errorf("first at %d:%d, want 2:1", se.FirstLine, se.FirstColumn)
	}
	se = syntaxAt(t, "position, spelled differently", oneyaml.Unmarshal([]byte(".nan: 1\n.NaN: 2\n"), &a), oneyaml.CategoryDuplicateKey, 2, 1)
	if se != nil && (se.FirstLine != 1 || se.FirstColumn != 1) {
		t.Errorf("first at %d:%d, want 1:1", se.FirstLine, se.FirstColumn)
	}
	// One .nan, or .nan next to other keys, still loads.
	for _, in := range []string{".nan: 1\n", ".nan: 1\n.inf: 2\n-.inf: 3\n", ".nan: 1\n\".nan\": 2\n", "{.nan: 1, nan: 2}\n"} {
		var m map[any]any
		if err := oneyaml.Unmarshal([]byte(in), &m); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
}

// A key written as an alias is reported where the alias is, not at the node the
// alias names.
func TestAliasKeyDuplicateIsReportedAtTheAlias(t *testing.T) {
	type k struct{ K int }
	for _, c := range []struct {
		name, in  string
		target    any
		line, col int
	}{
		{"any", "&a k: 1\n*a : 2\n", new(any), 2, 1},
		{"struct", "&a k: 1\n*a : 2\n", new(k), 2, 1},
		{"map", "&a k: 1\n*a : 2\n", new(map[string]int), 2, 1},
		{"after other keys", "&a k: 1\nz: 0\ny: 0\n*a : 2\n", new(any), 4, 1},
		{"flow mapping", "{&a k: 1, *a : 2}\n", new(any), 1, 11},
		{"flow into struct", "{&a k: 1, *a : 2}\n", new(k), 1, 11},
		{"indented", "x:\n  &a k: 1\n  *a : 2\n", new(any), 3, 3},
		{"ignored by the struct", "&a q: 1\n*a : 2\nb: 3\n", new(struct{ B int }), 2, 1},
		{"alias to alias is a node", "&a k: 1\nz: &b k\nw: 0\n*b : 3\n", new(any), 4, 1},
	} {
		err := oneyaml.Unmarshal([]byte(c.in), c.target)
		syntaxAt(t, c.name+" Unmarshal", err, oneyaml.CategoryDuplicateKey, c.line, c.col)
		doc, perr := oneyaml.Parse([]byte(c.in))
		if perr != nil {
			t.Errorf("%s: Parse: %v", c.name, perr)
			continue
		}
		var x any
		syntaxAt(t, c.name+" Document.Decode", doc.Decode(&x), oneyaml.CategoryDuplicateKey, c.line, c.col)
	}
	// An anchored key used once more in another mapping is not a duplicate.
	var a any
	if err := oneyaml.Unmarshal([]byte("&a k: 1\nz:\n  *a : 2\n"), &a); err != nil {
		t.Errorf("alias in another mapping: %v", err)
	}
}

// A key the target has no field for is still a key of its mapping: a repeat is
// refused, as it is when the same keys are read into a map.
func TestIgnoredKeysAreCheckedForDuplicates(t *testing.T) {
	type b struct{ B int }
	var many strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&many, "key%d: %d\n", i, i)
	}
	late := many.String() + "key7: x\nb: 1\n"
	for _, c := range []struct {
		name, in  string
		line, col int
	}{
		{"block", "a: 1\na: 2\nb: 3\n", 2, 1},
		{"not adjacent", "a: 1\nb: 3\nc: 0\na: 2\n", 4, 1},
		{"flow", "{a: 1, a: 2, b: 3}\n", 1, 8},
		{"integers spelled differently", "1: a\n0x1: b\nb: 3\n", 2, 1},
		{"null spelled differently", "null: a\n~: b\nb: 3\n", 2, 1},
		{"booleans spelled differently", "true: a\nTrue: b\nb: 3\n", 2, 1},
		{"quoted twice", "\"a\": 1\n'a': 2\nb: 3\n", 2, 1},
		{"quoted and plain", "\"a\": 1\na: 2\nb: 3\n", 2, 1},
		{"many keys then a repeat", late, 61, 1},
		{"inside an ignored value", "x:\n  a: 1\n  a: 2\nb: 3\n", 3, 3},
		{"inside an ignored sequence", "x:\n  - {a: 1}\n  - {p: 1, p: 2}\nb: 3\n", 3, 12},
		{"inside two levels", "x: {y: {z: 1, z: 2}}\nb: 3\n", 1, 15},
	} {
		var s b
		err := oneyaml.Unmarshal([]byte(c.in), &s)
		syntaxAt(t, c.name, err, oneyaml.CategoryDuplicateKey, c.line, c.col)
		err = oneyaml.Unmarshal([]byte(c.in), &s, oneyaml.KnownFields())
		syntaxAt(t, c.name+" with KnownFields", err, oneyaml.CategoryDuplicateKey, c.line, c.col)
	}
	var s b
	se := syntaxAt(t, "names the key", oneyaml.Unmarshal([]byte("a: 1\na: 2\nb: 3\n"), &s), oneyaml.CategoryDuplicateKey, 2, 1)
	if se != nil && (se.Key != "a" || se.FirstLine != 1 || se.FirstColumn != 1) {
		t.Errorf("got key %q first at %d:%d", se.Key, se.FirstLine, se.FirstColumn)
	}
	// Keys that differ are not repeats, and the same key in different mappings is fine.
	for _, in := range []string{
		"a: 1\nb: 2\nc: 3\n",
		"a: 1\n\"1\": 2\n1: 3\n",
		"x: {a: 1}\ny: {a: 2}\nb: 1\n",
		"x:\n  - {a: 1}\n  - {a: 2}\nb: 1\n",
		"1e-400: a\n2e-400: b\nb: 1\n",
		"true: a\n\"true\": b\nb: 1\n",
		"1: a\n\"1\": b\nb: 1\n",
	} {
		if err := oneyaml.Unmarshal([]byte(in), &s); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
	// With many keys that no field has, distinct ones are never mistaken for repeats.
	var big strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&big, "key%d: %d\n", i, i)
	}
	if err := oneyaml.Unmarshal([]byte(big.String()), &s); err != nil {
		t.Errorf("5000 distinct ignored keys: %v", err)
	}
	// An inline map and a known field are judged as before.
	var inl struct {
		B     int
		Extra map[string]int `yaml:",inline"`
	}
	syntaxAt(t, "inline", oneyaml.Unmarshal([]byte("a: 1\na: 2\n"), &inl), oneyaml.CategoryDuplicateKey, 2, 1)
}

// Two keys that are different numbers that a float64 cannot tell apart are not
// duplicates: reading them is refused with an error that says why, the same one
// at every level of precision lost, in every way of reading, and the error is not
// a duplicate-key error. A target that ignores them reads fine.
func TestRoundedFloatKeysAreNotDuplicatesInAnyPath(t *testing.T) {
	for _, in := range []string{
		"1e-400: a\n2e-400: b\n",
		"1e400: a\n2e400: b\n",
		"1e400: a\n.inf: b\n",
		"-1e400: a\n-.inf: b\n",
		"{1e-400: a, 1e-401: b}\n",
	} {
		var a any
		var m map[float64]string
		doc, err := oneyaml.Parse([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		var x any
		errs := map[string]error{
			"any": oneyaml.Unmarshal([]byte(in), &a), "map": oneyaml.Unmarshal([]byte(in), &m), "Document.Decode": doc.Decode(&x),
		}
		for name, err := range errs {
			var te *oneyaml.TypeError
			var se *oneyaml.SyntaxError
			if errors.As(err, &se) || !errors.As(err, &te) || !strings.Contains(te.Msg, "round to the same float64") ||
				strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "already defined") {
				t.Errorf("%q via %s: got %T %v", in, name, err, err)
			}
		}
		var s struct{ B int }
		if err := oneyaml.Unmarshal([]byte(in), &s); err != nil {
			t.Errorf("%q into a struct that ignores the keys: %v", in, err)
		}
	}
}

// The messages for input that stops short say what is missing and where it was
// opened.
func TestUnclosedInputMessagesSayWhatIsMissing(t *testing.T) {
	for _, c := range []struct {
		name, in  string
		line, col int
		want      []string
	}{
		{"flow sequence cut by a mapping line", "tags: [a, b\nname: x\n", 2, 1,
			[]string{"flow sequence opened at line 1, column 7", "not closed", "expected ',' or ']'"}},
		{"flow mapping cut by a mapping line", "tags: {a: 1, b: 2\nname: x\n", 2, 1,
			[]string{"flow mapping opened at line 1, column 7", "not closed", "expected ',' or '}'"}},
		{"flow sequence at the end of input", "tags: [a, b", 1, 12,
			[]string{"flow sequence opened at line 1, column 7", "expected ',' or ']'"}},
		{"flow sequence at the end of a line", "tags: [a, b\n", 2, 1,
			[]string{"flow sequence opened at line 1, column 7", "expected ',' or ']'"}},
		{"wrong closing bracket", "a: [1, 2}\n", 1, 9,
			[]string{"flow sequence opened at line 1, column 4", "expected ',' or ']'"}},
		{"wrong closing brace", "a: {x: 1]\n", 1, 9,
			[]string{"flow mapping opened at line 1, column 4", "expected ',' or '}'"}},
		{"inner collection is the open one", "a: [1, [2, 3\n  c\n", 3, 1,
			[]string{"flow sequence opened at line 1, column 8", "expected ',' or ']'"}},
		{"closed inner, open outer", "a: [[1], 2\n  c\n", 3, 1,
			[]string{"flow sequence opened at line 1, column 4", "expected ',' or ']'"}},
		{"double quoted", "name: \"api\nport: 80\n", 2, 1,
			[]string{"quoted scalar that starts at line 1, column 7", "never closed"}},
		{"single quoted", "name: 'api\nport: 80\n", 2, 1,
			[]string{"quoted scalar that starts at line 1, column 7", "never closed"}},
		{"quoted at the end of input", "name: \"api", 1, 11,
			[]string{"quoted scalar that starts at line 1, column 7", "never closed"}},
	} {
		for name, err := range readers(c.in) {
			se := syntaxAt(t, c.name+" via "+name, err, "syntax", c.line, c.col)
			if se == nil {
				continue
			}
			for _, w := range c.want {
				if !strings.Contains(se.Msg, w) {
					t.Errorf("%s via %s: message %q lacks %q", c.name, name, se.Msg, w)
				}
			}
		}
	}
}
