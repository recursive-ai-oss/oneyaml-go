package parse_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/scan"
)

func TestErrorPositions(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		line, col int
		cat       parse.Category
	}{
		{"unclosed flow at end", "[a, b", 1, 6, parse.Syntax},
		{"bom not counted", "\xEF\xBB\xBF[a}", 1, 3, parse.Syntax},
		{"crlf is one break", "a: 1\r\nb: [\r\n", 3, 1, parse.Syntax},
		{"lone cr is one break", "a: b\rc: *", 2, 5, parse.Syntax},
		{"columns count characters", "é: [ü, ü}", 1, 9, parse.Syntax},
		{"invalid utf8", "a: \xff\n", 1, 4, parse.Encoding},
		{"control char", "a: \x01\n", 1, 4, parse.Syntax},
		{"utf16 le", "a\x00:\x00", 1, 1, parse.Encoding},
		{"utf16 bom", "\xFF\xFEa\x00", 1, 1, parse.Encoding},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eventLines([]byte(tc.in))
			var e *parse.Error
			if !errors.As(err, &e) {
				t.Fatalf("want *parse.Error, got %v", err)
			}
			if e.Line != tc.line || e.Column != tc.col || e.Category != tc.cat {
				t.Errorf("got %v:%v %v (%s), want %v:%v %v", e.Line, e.Column, e.Category, e.Msg, tc.line, tc.col, tc.cat)
			}
		})
	}
}

func TestDepthLimit(t *testing.T) {
	const n = 200000
	deep := map[string]string{
		"flow seq":    strings.Repeat("[", n),
		"flow map":    strings.Repeat("{a: ", n),
		"block seq":   strings.Repeat("- ", n) + "x",
		"block map":   nestedBlockMap(20000),
		"mixed":       strings.Repeat("a: [", n),
		"explicit":    strings.Repeat("? ", n),
		"closed deep": strings.Repeat("[", n) + strings.Repeat("]", n),
	}
	for name, in := range deep {
		t.Run(name, func(t *testing.T) {
			p := parse.New([]byte(in), 0)
			for {
				_, err := p.Next()
				if err == nil {
					continue
				}
				var e *parse.Error
				if !errors.As(err, &e) || e.Category != parse.Limit {
					t.Fatalf("want a limit error, got %v", err)
				}
				return
			}
		})
	}

	t.Run("custom limit", func(t *testing.T) {
		p := parse.New([]byte("[[[[1]]]]"), 3)
		for {
			if _, err := p.Next(); err != nil {
				var e *parse.Error
				if !errors.As(err, &e) || e.Category != parse.Limit {
					t.Fatalf("got %v", err)
				}
				return
			}
		}
	})
	t.Run("no limit", func(t *testing.T) {
		in := strings.Repeat("[", 20000) + strings.Repeat("]", 20000)
		p := parse.New([]byte(in), -1)
		n := 0
		for {
			_, err := p.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			n++
		}
		if n != 2*20000+4 {
			t.Errorf("got %d events", n)
		}
	})
}

func nestedBlockMap(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString(strings.Repeat(" ", i))
		sb.WriteString("k:\n")
	}
	return sb.String()
}

func TestAppendScalar(t *testing.T) {
	tests := []struct{ in, want string }{
		{"a: plain  text\n", "plain  text"},
		{"a: multi\n  line\n\n  text\n", "multi line\ntext"},
		{"a: 'it''s'\n", "it's"},
		{"a: \"tab\\there \\u00e9 \\U0001F600 \\x41\"\n", "tab\there é 😀 A"},
		{"a: \"fold  \n   ed\"\n", "fold ed"},
		{"a: \"keep \\\n   joined\"\n", "keep joined"},
		{"a: \"x\\\n\n   y\"\n", "x\ny"},
		{"a: |\n  lit\n   eral\n\n  end\n", "lit\n eral\n\nend\n"},
		{"a: |-\n  lit\n\n", "lit"},
		{"a: |+\n  lit\n\n", "lit\n\n"},
		{"a: >\n  fold\n  ed\n\n  para\n   more\n  back\n", "fold ed\npara\n more\nback\n"},
		{"a: >-\n  x\n  y\n", "x y"},
		{"a: |2\n   two\n", " two\n"},
		{"a: |\r\n  crlf\r\n  lines\r\n", "crlf\nlines\n"},
		{"a:\n- |\n x\n", "x\n"},
		{"a: ''\n", ""},
	}
	for _, tc := range tests {
		p := parse.New([]byte(tc.in), 0)
		var got string
		found := false
		for {
			ev, err := p.Next()
			if err != nil {
				break
			}
			if ev.Kind == parse.Scalar && !found && ev.End > ev.Start {
				// The value is the second scalar of the first mapping.
				if string(tc.in[ev.Start]) == "a" && ev.Style == scan.Plain && ev.End-ev.Start == 1 {
					continue
				}
				b, err := p.AppendScalar(nil, &ev)
				if err != nil {
					t.Fatal(err)
				}
				got, found = string(b), true
			}
			if ev.Kind == parse.Scalar && !found && ev.End == ev.Start {
				found = true
			}
		}
		if got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTagResolution(t *testing.T) {
	src := "%TAG !e! tag:example.com,2000:app/\n%TAG !! tag:other,2020:\n---\n- !e!foo a\n- !!str b\n- !<tag:x%41> c\n- ! d\n- !local%20x e\n"
	p := parse.New([]byte(src), 0)
	var got []string
	for {
		ev, err := p.Next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			break
		}
		if ev.Kind == parse.Scalar && ev.HasTag() {
			b, err := p.AppendTag(nil, &ev)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, string(b))
		}
	}
	want := []string{"tag:example.com,2000:app/foo", "tag:other,2020:str", "tag:xA", "!", "!local x"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := eventLines([]byte("!x!y a\n")); err == nil {
		t.Error("undefined tag handle accepted")
	}
}

func TestRejects(t *testing.T) {
	bad := []string{
		"a: b: c", "- a\nb", "[a, b", "{a: b", "\"abc", "'abc", "a: &x &y b", "*", "&", "!<x", "--- |0\n x",
		"a: |\n  x\n y\n", "%YAML 1.2\n%YAML 1.2\n---\n", "%TAG !a! x\n%TAG !a! y\n---\n", "[a]]", "{a}}",
		"? a\n? b\nc: d\n- e", "a:\n\t- b", "key: \"a\n\"b\"", "a\n---\n%YAML 1.2\n---\nb",
	}
	for _, in := range bad {
		if lines, err := eventLines([]byte(in)); err == nil {
			t.Errorf("%q accepted: %v", in, lines)
		}
	}
}

// TestNoAllocPerEvent proves the hot path does not allocate once a Parser has
// grown its buffers: parsing a whole document with Reset costs no allocation.
func TestNoAllocPerEvent(t *testing.T) {
	src := []byte(syntheticK8s(20))
	p := parse.New(src, 0)
	var buf []byte
	run := func() {
		p.Reset(src, 0)
		for {
			ev, err := p.Next()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					t.Fatal(err)
				}
				return
			}
			if ev.Kind == parse.Scalar {
				buf, _ = p.AppendScalar(buf[:0], &ev)
			}
			if ev.HasTag() {
				buf, _ = p.AppendTag(buf[:0], &ev)
			}
		}
	}
	run()
	if n := testing.AllocsPerRun(20, run); n != 0 {
		t.Errorf("%v allocations per parse of %d bytes, want 0", n, len(src))
	}
}

// TestPathologicalLinear checks inputs that could make a careless scanner
// quadratic. With a quadratic scanner these would take minutes.
func TestPathologicalLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("scaling test")
	}
	const n = 1 << 20
	inputs := map[string]string{
		"long flow seq":                          "[" + strings.Repeat("a, ", n) + "]",
		"long flow map":                          "{" + strings.Repeat("a: b, ", n) + "}",
		"long plain":                             strings.Repeat("word ", n),
		"many lines plain":                       strings.Repeat("word\n", n),
		"many keys":                              strings.Repeat("k: v\n", n),
		"long quoted lines":                      "\"" + strings.Repeat("word\n", n) + "\"",
		"multibyte columns":                      "[" + strings.Repeat("é, ", n) + "]\n---\n" + strings.Repeat("é ", n),
		"many docs":                              strings.Repeat("--- a\n...\n", n/4),
		"comments":                               strings.Repeat("# c\n", n),
		"block scalar":                           "a: |\n" + strings.Repeat("  line\n", n),
		"deep flow then lines":                   strings.Repeat("{", 9000) + strings.Repeat("a,\n", n/2) + strings.Repeat("}", 9000),
		"bom comment lines":                      "a\n" + strings.Repeat("\uFEFF# c\n", n/8),
		"deep flow mappings, many named keys":    strings.Repeat("{", 9000) + keyLines(140608),
		"shallow flow mappings, many named keys": strings.Repeat("{", 100) + keyLines(140608),
		"anchors":                                strings.Repeat("- &a x\n- *a\n", n/4),
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			p := parse.New([]byte(in), 0)
			for {
				if _, err := p.Next(); err != nil {
					// The unclosed shapes end in a syntax error; the point is the time.
					var pe *parse.Error
					if !errors.Is(err, io.EOF) && !(strings.Contains(name, "named keys") && errors.As(err, &pe)) {
						t.Fatal(err)
					}
					return
				}
			}
		})
	}
}

func TestReconstructionExamples(t *testing.T) {
	for _, in := range []string{
		"\xEF\xBB\xBF# c\r\n%YAML 1.2\n--- !t &a\n- x # y\n- [a, b]: c\n? |\n  q\n: >\n  r\n\n...\n# end",
		"", " ", "#", "a", "a: b\n\n\n", "- - - x\n",
	} {
		out, problem := reconstruct([]byte(in))
		if problem != "" || string(out) != in {
			t.Errorf("%q: %s (%q)", in, problem, out)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"a: b\n", "- [a, {b: c}]\n", "--- |\n x\n...\n", "&a !t x: *a\n", "%TAG !e! tag:x,1:\n---\n!e!a b",
		"\"a\\u00e9\\\nb\"", "? a\n: b\n", "[a: b, ? c]", "a: 'x\n y'", "\xEF\xBB\xBFa: b", "k: >-\n\n  x\n\n y",
	} {
		f.Add([]byte(s), uint8(0))
	}
	// Seed from a corpus directory when one is given (not stored in this repository).
	if dir := os.Getenv("ONEYAML_YTS_DIR"); dir != "" {
		files, _ := filepath.Glob(filepath.Join(dir, "*", "in.yaml"))
		more, _ := filepath.Glob(filepath.Join(dir, "*", "*", "in.yaml"))
		for _, name := range append(files, more...) {
			if b, err := os.ReadFile(name); err == nil {
				f.Add(b, uint8(1))
			}
		}
	}
	f.Fuzz(func(t *testing.T, src []byte, mode uint8) {
		depth := 0
		if mode&1 != 0 {
			depth = 8 // exercise the limit path
		}
		p := parse.New(src, depth)
		var buf []byte
		// Every event consumes input or is an empty node; this bound catches a
		// parser that stops making progress.
		budget := 8*len(src) + 64
		for {
			ev, err := p.Next()
			if err != nil {
				var e *parse.Error
				if !errors.Is(err, io.EOF) && !errors.As(err, &e) {
					t.Fatalf("unexpected error type %T: %v", err, err)
				}
				break
			}
			if budget--; budget < 0 {
				t.Fatalf("parser does not terminate on %q", src)
			}
			if ev.Start < 0 || ev.End < ev.Start || ev.End > len(src) {
				t.Fatalf("bad span %+v", ev)
			}
			if ev.Kind == parse.Scalar {
				var derr error
				if buf, derr = p.AppendScalar(buf[:0], &ev); derr != nil {
					var e *parse.Error
					if !errors.As(derr, &e) {
						t.Fatal(derr)
					}
				}
			}
			if ev.HasTag() {
				buf, _ = p.AppendTag(buf[:0], &ev)
			}
		}
		out, problem := reconstruct(src)
		if problem != "" {
			t.Fatalf("reconstruction: %s", problem)
		}
		if string(out) != string(src) {
			t.Fatalf("reconstruction differs: %q vs %q", out, src)
		}
	})
}

// keyLines returns n distinct names, each followed by a comma and a line break.
func keyLines(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteByte('x')
		for k, v := 0, i; k < 3; k, v = k+1, v/len(letters) {
			sb.WriteByte(letters[v%len(letters)])
		}
		sb.WriteString(",\n")
	}
	return sb.String()
}
