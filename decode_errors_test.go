package oneyaml_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"oneyaml.dev/go"
)

type serverConf struct {
	Port, Workers int
	Name          string
	Tags          []int
	Limits        map[string]int
	TLS           struct{ Cert, Key int }
}

const badConf = "port: x\nworkers: y\nname: kept\ntags: [1, a, 3, [4]]\nlimits: {a: 1, b: z, c: 3}\ntls: {cert: q, key: 2}\n"

func TestUnmarshalKeepsReadingAfterTypeErrors(t *testing.T) {
	var c serverConf
	c.Port = 80
	err := oneyaml.Unmarshal([]byte(badConf), &c)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) {
		t.Fatalf("want a *TypeError, got %T %v", err, err)
	}
	// Everything that fits is set; a field that did not fit is left as it was.
	if c.Name != "kept" || c.Port != 80 || c.Workers != 0 || c.TLS.Key != 2 {
		t.Errorf("fields after a type error: %+v", c)
	}
	if fmt.Sprint(c.Tags) != "[1 0 3 0]" || fmt.Sprint(c.Limits) != "map[a:1 c:3]" {
		t.Errorf("collections: tags %v limits %v", c.Tags, c.Limits)
	}
	want := []struct {
		path      string
		line, col int
	}{{"/port", 1, 7}, {"/workers", 2, 10}, {"/tags/1", 4, 11}, {"/tags/3", 4, 17}, {"/limits/b", 5, 19}, {"/tls/cert", 6, 13}}
	all := te.All()
	if len(all) != len(want) {
		t.Fatalf("got %d errors, want %d: %v", len(all), len(want), err)
	}
	for i, w := range want {
		if all[i].Path != w.path || all[i].Line != w.line || all[i].Column != w.col {
			t.Errorf("error %d: %s at %d:%d, want %s at %d:%d", i, all[i].Path, all[i].Line, all[i].Column, w.path, w.line, w.col)
		}
	}
	if !strings.Contains(err.Error(), "line 1, column 7: /port") || !strings.Contains(err.Error(), "line 2, column 10: /workers") {
		t.Errorf("message lacks the positions: %v", err)
	}
}

func TestTypeErrorSingleStaysSingle(t *testing.T) {
	var c serverConf
	err := oneyaml.Unmarshal([]byte("port: x\n"), &c)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) || len(te.More) != 0 || len(te.All()) != 1 {
		t.Fatalf("got %#v", err)
	}
	if !strings.HasPrefix(err.Error(), "oneyaml: line 1, column 7: /port: ") {
		t.Errorf("message %q", err)
	}
}

func TestSyntaxErrorStopsAtOnce(t *testing.T) {
	var c serverConf
	err := oneyaml.Unmarshal([]byte("port: x\nname: kept\nworkers: [1\n"), &c)
	var se *oneyaml.SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("want a syntax error, got %T %v", err, err)
	}
}

func TestHookErrorStopsAtOnce(t *testing.T) {
	var v struct {
		A int
		B hookFails
		C string
	}
	err := oneyaml.Unmarshal([]byte("a: x\nb: 1\nc: later\n"), &v)
	if err == nil || !strings.Contains(err.Error(), "hook says no") {
		t.Fatalf("got %v", err)
	}
	if v.C != "" {
		t.Errorf("decoding went on past a hook error: %+v", v)
	}
}

type hookFails int

func (hookFails) UnmarshalText([]byte) error { return errors.New("hook says no") }

func TestTypeErrorsAreBounded(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString("- x\n")
	}
	var v []int
	err := oneyaml.Unmarshal([]byte(sb.String()), &v)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) {
		t.Fatalf("got %v", err)
	}
	if n := len(te.All()); n != 100 {
		t.Errorf("kept %d errors, want 100", n)
	}
}

func TestDecoderAndDocumentKeepReading(t *testing.T) {
	dec := oneyaml.NewDecoder(strings.NewReader("port: x\nname: first\n---\nport: 7\nname: second\n"))
	var a, b serverConf
	err := dec.Decode(&a)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) || a.Name != "first" {
		t.Fatalf("first document: %v %+v", err, a)
	}
	if err := dec.Decode(&b); err != nil || b.Port != 7 || b.Name != "second" {
		t.Fatalf("second document: %v %+v", err, b)
	}

	doc, err := oneyaml.Parse([]byte(badConf))
	if err != nil {
		t.Fatal(err)
	}
	var c serverConf
	err = doc.Decode(&c)
	if !errors.As(err, &te) || len(te.All()) != 6 || c.Name != "kept" {
		t.Fatalf("document: %v %+v", err, c)
	}
}

func TestUnknownFieldIsMatchable(t *testing.T) {
	var c serverConf
	err := oneyaml.Unmarshal([]byte("port: 1\nprot: 2\nname: n\n"), &c, oneyaml.KnownFields())
	if !errors.Is(err, oneyaml.ErrUnknownField) {
		t.Fatalf("errors.Is(%v, ErrUnknownField) is false", err)
	}
	if c.Name != "n" || c.Port != 1 {
		t.Errorf("fields: %+v", c)
	}
	// Found among other mismatches too, and not claimed for them.
	err = oneyaml.Unmarshal([]byte("port: x\nprot: 2\n"), &c, oneyaml.KnownFields())
	var te *oneyaml.TypeError
	if !errors.As(err, &te) || len(te.All()) != 2 || !errors.Is(err, oneyaml.ErrUnknownField) {
		t.Fatalf("got %v", err)
	}
	if err := oneyaml.Unmarshal([]byte("port: x\n"), &c, oneyaml.KnownFields()); errors.Is(err, oneyaml.ErrUnknownField) {
		t.Errorf("a plain mismatch is not an unknown field")
	}
}

func TestSyntaxMessagesNameTheMistake(t *testing.T) {
	cases := []struct {
		name, in  string
		line, col int
		want      []string
	}{
		{"unclosed flow sequence", "tags: [a, b\nname: x", 2, 1, []string{"flow sequence opened at line 1, column 7", "']'"}},
		{"unclosed flow mapping", "a: {b: 1,\n  c: 2\nd: 1", 3, 1, []string{"flow mapping opened at line 1, column 4", "'}'"}},
		{"unclosed double quote", "name: \"api\nport: 80", 2, 1, []string{"quoted scalar that starts at line 1, column 7"}},
		{"quote open at end", "a: 'x", 1, 6, []string{"quoted scalar that starts at line 1, column 4", "never closed"}},
		{"tab indentation", "name: api\n\tport: 80", 2, 2, []string{"tab", "spaces"}},
	}
	for _, c := range cases {
		var v any
		err := oneyaml.Unmarshal([]byte(c.in), &v)
		var se *oneyaml.SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("%s: got %v", c.name, err)
			continue
		}
		if se.Category != oneyaml.CategorySyntax || se.Line != c.line || se.Column != c.col {
			t.Errorf("%s: %s at %d:%d", c.name, se.Category, se.Line, se.Column)
		}
		for _, w := range c.want {
			if !strings.Contains(se.Msg, w) {
				t.Errorf("%s: message %q lacks %q", c.name, se.Msg, w)
			}
		}
	}
}

func TestDuplicateKeyNamesKeyAndBothPositions(t *testing.T) {
	cases := []struct {
		in                     string
		line, col, fline, fcol int
		key                    string
	}{
		{"name: a\nport: 1\nname: b", 3, 1, 1, 1, "name"},
		{"{a: 1, a: 2}", 1, 8, 1, 2, "a"},
		{"x:\n  b: 1\n  c: 2\n  b: 3\n", 4, 3, 2, 3, "b"},
		{"1.5: a\n1.5: b", 2, 1, 1, 1, "1.5"},
	}
	for _, c := range cases {
		for _, target := range []any{new(any), new(map[string]any)} {
			if _, ok := target.(*map[string]any); ok && strings.HasPrefix(c.in, "x:") {
				continue
			}
			err := oneyaml.Unmarshal([]byte(c.in), target)
			var se *oneyaml.SyntaxError
			if !errors.As(err, &se) || se.Category != oneyaml.CategoryDuplicateKey {
				t.Errorf("%q into %T: got %v", c.in, target, err)
				continue
			}
			if se.Key != c.key || se.Line != c.line || se.Column != c.col || se.FirstLine != c.fline || se.FirstColumn != c.fcol {
				t.Errorf("%q: %+v", c.in, se)
			}
			if !strings.Contains(se.Msg, strconvQuote(c.key)) || !strings.Contains(se.Msg, fmt.Sprintf("line %d, column %d", c.fline, c.fcol)) {
				t.Errorf("message %q", se.Msg)
			}
		}
	}
	// Into a struct too.
	var s struct{ Name string }
	err := oneyaml.Unmarshal([]byte("name: a\nname: b\n"), &s)
	var se *oneyaml.SyntaxError
	if !errors.As(err, &se) || se.Key != "name" || se.FirstLine != 1 {
		t.Errorf("struct: %v", err)
	}
}

func strconvQuote(s string) string { return fmt.Sprintf("%q", s) }

func TestFloatKeysThatRoundTogetherAreNotCalledDuplicates(t *testing.T) {
	var v any
	err := oneyaml.Unmarshal([]byte("1e-400: a\n2e-400: b\n"), &v)
	var te *oneyaml.TypeError
	var se *oneyaml.SyntaxError
	if errors.As(err, &se) || !errors.As(err, &te) {
		t.Fatalf("want a type error, got %T %v", err, err)
	}
	for _, w := range []string{"different numbers", "round to the same float64", "line 1, column 1", "line 2, column 1"} {
		if !strings.Contains(te.Msg, w) {
			t.Errorf("message %q lacks %q", te.Msg, w)
		}
	}
	if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "more than once") || strings.Contains(err.Error(), "already defined") {
		t.Errorf("calls the keys duplicates: %v", err)
	}
	// The same value spelled two ways is a duplicate.
	for _, in := range []string{"1e400: a\n10e399: b\n", "0.5: a\n5e-1: b\n", ".inf: a\n.Inf: b\n"} {
		err = oneyaml.Unmarshal([]byte(in), &v)
		if !errors.As(err, &se) || se.Category != oneyaml.CategoryDuplicateKey || se.FirstLine != 1 || se.Line != 2 {
			t.Errorf("%q: got %v", in, err)
		}
	}
}

type rawJSON struct{ B string }

func (r *rawJSON) UnmarshalJSON(b []byte) error { r.B = string(b); return nil }

func TestJSONUnmarshalerGetsTheSourceDigits(t *testing.T) {
	for in, want := range map[string]string{
		"12345678901234567890":           "12345678901234567890",
		"123456789012345678901234567890": "123456789012345678901234567890",
		"1.23456789012345678901":         "1.23456789012345678901",
		"1.50":                           "1.50",
		"100000000000000000000.5":        "100000000000000000000.5",
		"9007199254740993":               "9007199254740993",
		"1e400":                          "1e400",
		"-0.0":                           "-0.0",
		"0x1F":                           "31",
		"+5":                             "5",
		".5":                             "0.5",
		"'12345678901234567890'":         `"12345678901234567890"`,
		"!!str 1.50":                     `"1.50"`,
	} {
		var v struct{ N rawJSON }
		if err := oneyaml.Unmarshal([]byte("n: "+in+"\n"), &v); err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if v.N.B != want {
			t.Errorf("%s: UnmarshalJSON got %s, want %s", in, v.N.B, want)
		}
	}
}

func TestCategoryConstants(t *testing.T) {
	for in, want := range map[string]string{"a: [": oneyaml.CategorySyntax, "a: 1\na: 2": oneyaml.CategoryDuplicateKey, "a: \xff": oneyaml.CategoryEncoding} {
		var v any
		var se *oneyaml.SyntaxError
		if err := oneyaml.Unmarshal([]byte(in), &v); !errors.As(err, &se) || se.Category != want {
			t.Errorf("%q: got %v, want category %s", in, err, want)
		}
	}
}
