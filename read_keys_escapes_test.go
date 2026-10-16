package oneyaml_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"oneyaml.dev/go"
	yaml "oneyaml.dev/go/compat/yamlv3"
)

// A float key and a string key with the same text are two keys: they have
// different tags. Ignored by the target, they must not be taken for a repeat.
func TestIgnoredFloatAndStringKeysAreDifferentKeys(t *testing.T) {
	type b struct{ B int }
	for _, in := range []string{
		"1.5: a\n\"1.5\": b\nb: 1\n",
		"\"1.5\": a\n1.5: b\nb: 1\n",
		"x:\n  .inf: a\n  \".inf\": b\nb: 1\n",
		"!!float 1: a\n\"1\": b\nb: 1\n",
		"!!float 1: a\n1: b\nb: 1\n",
		"1e3: a\n'1e3': b\nb: 1\n",
	} {
		var s b
		if err := oneyaml.Unmarshal([]byte(in), &s); err != nil {
			t.Errorf("%q: %v", in, err)
		}
		if err := oneyaml.Unmarshal([]byte(in), &s, oneyaml.KnownFields()); err != nil {
			var te *oneyaml.TypeError
			if !errors.As(err, &te) {
				t.Errorf("%q with KnownFields: %v", in, err)
			}
		}
	}
	// The same float twice is still a repeat.
	var s b
	syntaxAt(t, "same float", oneyaml.Unmarshal([]byte("1.5: a\n1.5: b\nb: 1\n"), &s), oneyaml.CategoryDuplicateKey, 2, 1)
	syntaxAt(t, "!!float twice", oneyaml.Unmarshal([]byte("!!float 1: a\n!!float 1: b\nb: 1\n"), &s), oneyaml.CategoryDuplicateKey, 2, 9)
}

// An integer key the target ignores is held to the digit limit like one that is
// read into a value, so a huge one is refused before it is turned into a number.
func TestIgnoredHugeIntegerKeyIsLimited(t *testing.T) {
	huge := "? 1" + strings.Repeat("0", 100_000) + "\n: a\nb: 1\n"
	for name, target := range map[string]any{"struct": new(struct{ B int }), "any": new(any)} {
		err := oneyaml.Unmarshal([]byte(huge), target)
		var le *oneyaml.LimitError
		if !errors.As(err, &le) || le.Limit != "number digits" {
			t.Errorf("%s: got %v, want the number digits limit", name, err)
		}
	}
	// Inside an ignored value too, and not when the limit is lifted or the key fits.
	var s struct{ B int }
	nested := "x: {? 1" + strings.Repeat("0", 100_000) + " : a}\nb: 1\n"
	if err := oneyaml.Unmarshal([]byte(nested), &s); err == nil {
		t.Errorf("nested huge key: no error")
	}
	if err := oneyaml.Unmarshal([]byte(huge), &s, oneyaml.Limits(oneyaml.LimitSet{MaxNumberDigits: oneyaml.NoBound})); err != nil {
		t.Errorf("no bound: %v", err)
	}
	if err := oneyaml.Unmarshal([]byte("1"+strings.Repeat("0", 99)+": a\nb: 1\n"), &s); err != nil {
		t.Errorf("small: %v", err)
	}
}

// Keys of ignored mappings are judged per mapping however the mappings nest:
// a child's keys neither hide nor invent a repeat in its parent, and a mapping
// with many keys is judged the same as one with few.
func TestIgnoredKeysInNestedMappings(t *testing.T) {
	var s struct{ B int }
	level := func(n int, extra string) string {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&sb, "k%d: %d, ", i, i)
		}
		return sb.String() + extra
	}
	for _, c := range []struct {
		name, in string
		dup      bool
	}{
		{"parent key after child", "x: {a: {a: 1, b: 2}, b: 1, a: 3}\n", true},
		{"same keys in parent and child", "x: {a: {a: 1, b: 2}, b: 1, c: 3}\n", false},
		{"siblings share names", "x: [{a: 1}, {a: 2}, {a: 3}]\n", false},
		{"many keys then a repeat after a child", "x: {" + level(40, "c: {k1: 1, k1: 2}") + "}\n", true},
		{"many keys then a repeat of the first", "x: {" + level(40, "k0: 9") + "}\n", true},
		{"many keys, child reuses names", "x: {" + level(40, "c: {"+level(40, "z: 1")+"}, d: 1") + "}\n", false},
		{"child with many keys, parent repeats after", "x: {a: 1, c: {" + level(40, "z: 1") + "}, a: 2}\n", true},
		{"child with many keys, parent has none repeated", "x: {a: 1, c: {" + level(40, "z: 1") + "}, e: 2}\n", false},
	} {
		err := oneyaml.Unmarshal([]byte(c.in), &s)
		var se *oneyaml.SyntaxError
		isDup := errors.As(err, &se) && se.Category == oneyaml.CategoryDuplicateKey
		if isDup != c.dup || (err != nil) != c.dup {
			t.Errorf("%s: got %v, want duplicate=%v", c.name, err, c.dup)
		}
	}
}

// A key that does not fit the key type is skipped with its value; a repeat of
// it is still a repeat.
func TestMismatchedMapKeysAreCheckedForDuplicates(t *testing.T) {
	var m map[int]int
	syntaxAt(t, "same bad key", oneyaml.Unmarshal([]byte("x: 1\nx: 2\n"), &m), oneyaml.CategoryDuplicateKey, 2, 1)
	m = nil
	syntaxAt(t, "inside the skipped value", oneyaml.Unmarshal([]byte("x: {a: 1, a: 2}\n"), &m), oneyaml.CategoryDuplicateKey, 1, 11)
	// Different bad keys are two type errors, as before.
	m = nil
	err := oneyaml.Unmarshal([]byte("x: 1\ny: 2\n1: 3\n"), &m)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) || m[1] != 3 {
		t.Errorf("different bad keys: got %v, %v", err, m)
	}
}

// An escaped high surrogate directly followed by an escaped low one is a pair
// and names one character; any other surrogate is refused at its escape.
func TestEscapedSurrogatePairs(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`"\ud83d\ude00"`, "\U0001F600"},
		{`"\uD83D\uDE00"`, "\U0001F600"},
		{`"\ud800\udc00"`, "\U00010000"},
		{`"\udbff\udfff"`, "\U0010FFFF"},
		{`"\U0000d83d\U0000de00"`, "\U0001F600"},
		{`"\ud83d\U0000de00"`, "\U0001F600"},
		{`"a\ud83d\ude00b"`, "a\U0001F600b"},
		{`"\ud83d\ude00\ud83d\ude01"`, "\U0001F600\U0001F601"},
	} {
		in := "v: " + c.in + "\n"
		var u struct{ V string }
		if err := oneyaml.Unmarshal([]byte(in), &u); err != nil || u.V != c.want {
			t.Errorf("%s Unmarshal: got %q, %v", c.in, u.V, err)
		}
		var a map[string]any
		if err := oneyaml.NewDecoder(strings.NewReader(in)).Decode(&a); err != nil || a["v"] != c.want {
			t.Errorf("%s Decoder: got %v, %v", c.in, a["v"], err)
		}
		doc, err := oneyaml.Parse([]byte(in))
		if err != nil {
			t.Errorf("%s Parse: %v", c.in, err)
			continue
		}
		var d struct{ V string }
		if err := doc.Decode(&d); err != nil || d.V != c.want {
			t.Errorf("%s Document.Decode: got %q, %v", c.in, d.V, err)
		}
		var y struct{ V string }
		if err := yaml.Unmarshal([]byte(in), &y); err != nil || y.V != c.want {
			t.Errorf("%s yamlv3: got %q, %v", c.in, y.V, err)
		}
		// A key is read the same way, and so is a value the target ignores.
		var ign struct{ B int }
		if err := oneyaml.Unmarshal([]byte(in+"b: 1\n"), &ign); err != nil {
			t.Errorf("%s ignored: %v", c.in, err)
		}
		var k map[string]int
		if err := oneyaml.Unmarshal([]byte(c.in+": 1\n"), &k); err != nil || k[c.want] != 1 {
			t.Errorf("%s as a key: got %v, %v", c.in, k, err)
		}
	}
	// Refused at the first escape, by every reader.
	for _, c := range []struct {
		name, in string
		col      int
	}{
		{"low then high", "a: \"\\ude00\\ud83d\"\n", 5},
		{"two highs", "a: \"\\ud83d\\ud83d\"\n", 5},
		{"high then letter", "a: \"\\ud83dx\"\n", 5},
		{"high then other escape", "a: \"\\ud83d\\n\"\n", 5},
		{"high then non-surrogate escape", "a: \"\\ud83d\\u0041\"\n", 5},
		{"high then a gap", "a: \"\\ud83d \\ude00\"\n", 5},
		{"pair then lone low", "a: \"\\ud83d\\ude00\\ude00\"\n", 17},
		{"high then truncated", "a: \"\\ud83d\\ude0\"\n", 5},
		{"high at the end", "a: \"x\\ud83d\"\n", 6},
	} {
		for name, err := range readers(c.in) {
			syntaxAt(t, c.name+" via "+name, err, "syntax", 1, c.col)
		}
		var y struct{ A string }
		if err := yaml.Unmarshal([]byte(c.in), &y); err == nil {
			t.Errorf("%s via yamlv3: no error", c.name)
		}
	}
}

// Keys written as 0x1 and 1 are one key in either order, and the error points
// at the earlier one.
func TestHexAndDecimalKeysAreDuplicatesInBothOrders(t *testing.T) {
	for _, c := range []struct {
		in    string
		first string
	}{
		{"0x1: 2\n1: 3\n", "0x1"},
		{"1: 3\n0x1: 2\n", "1"},
		{"0x10: 2\n16: 3\n", "0x10"},
		{"0o7: 2\n7: 3\n", "0o7"},
	} {
		for name, err := range readers(c.in) {
			if name == "Unmarshal map" || name == "Parse" {
				continue // string keys differ as text; Parse does not judge keys
			}
			se := syntaxAt(t, c.in+" via "+name, err, oneyaml.CategoryDuplicateKey, 2, 1)
			if se != nil && (se.FirstLine != 1 || se.FirstColumn != 1) {
				t.Errorf("%q via %s: first key at %d:%d, want 1:1", c.in, name, se.FirstLine, se.FirstColumn)
			}
			if se != nil && !strings.Contains(se.Msg, `"`+c.first+`"`) {
				t.Errorf("%q via %s: message %q does not name the earlier key %s", c.in, name, se.Msg, c.first)
			}
		}
	}
	// Numbers that merely round together are still the type error.
	var a any
	err := oneyaml.Unmarshal([]byte("1e-400: a\n2e-400: b\n"), &a)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) {
		t.Errorf("rounding keys: got %v, want a TypeError", err)
	}
}
