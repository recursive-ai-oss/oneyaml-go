package parse_test

import (
	"errors"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/parse"
)

func mustEvents(t *testing.T, in string) string {
	t.Helper()
	lines, err := eventLines([]byte(in))
	if err != nil {
		t.Fatalf("%q: %v", in, err)
	}
	return strings.Join(lines, "|")
}

func mustReject(t *testing.T, in string) *parse.Error {
	t.Helper()
	lines, err := eventLines([]byte(in))
	var e *parse.Error
	if err == nil || !errors.As(err, &e) {
		t.Fatalf("%q accepted: %v", in, lines)
	}
	return e
}

// A byte order mark is allowed in a document prefix (production 211): at the
// start of the stream, between documents, or in front of comment lines that lead
// to a document marker or the end of the input. It is also allowed inside quoted
// scalars. Inside a document, in plain scalars, comments and block scalars it is not.
func TestByteOrderMarkMidStream(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"a: 1\n...\n\uFEFF--- b\n", "+STR|+DOC|+MAP|=VAL :a|=VAL :1|-MAP|-DOC ...|+DOC ---|=VAL :b|-DOC|-STR"},
		{"a\n\uFEFF# c\n", "+STR|+DOC|=VAL :a|-DOC|-STR"},
		{"a\n\uFEFF# c\n\uFEFF# d\n", "+STR|+DOC|=VAL :a|-DOC|-STR"},
		{"a\n\uFEFF\n# c\n---\nb\n", "+STR|+DOC|=VAL :a|-DOC|+DOC ---|=VAL :b|-DOC|-STR"},
		{"a: 1\n\uFEFF--- b\n", "+STR|+DOC|+MAP|=VAL :a|=VAL :1|-MAP|-DOC|+DOC ---|=VAL :b|-DOC|-STR"},
		{"a: \"x\uFEFFy\"\n", "+STR|+DOC|+MAP|=VAL :a|=VAL \"x\uFEFFy|-MAP|-DOC|-STR"},
	} {
		if got := mustEvents(t, tc.in); got != tc.want {
			t.Errorf("%q:\n got %s\nwant %s", tc.in, got, tc.want)
		}
	}
	for _, in := range []string{
		"a\uFEFFb: c\n", "# c\uFEFFd\n", "a: |\n  x\uFEFFy\n", "a: 1\n\uFEFFb: 2\n", "- Invalid use of BOM\n\uFEFF\n- Inside a document.\n", "a: 1\n\uFEFF# c\nb: 2\n", "&a\uFEFFb x\n", "a: b\uFEFF\n",
	} {
		mustReject(t, in)
	}
	// The mark is not a character: it does not move later columns.
	if e := mustReject(t, "a: 1\n...\n\uFEFF--- [a}"); e.Line != 3 || e.Column != 7 {
		t.Errorf("got %d:%d, want 3:7", e.Line, e.Column)
	}
	if e := mustReject(t, "a: 1\n\uFEFFb: 2\n"); e.Line != 2 || e.Column != 1 {
		t.Errorf("got %d:%d, want 2:1", e.Line, e.Column)
	}
}

func TestEncodingTable(t *testing.T) {
	for _, in := range []string{
		"\x00\x00\x00a", "a\x00\x00\x00", "\x00a", "a\x00", "\x00\x00\xFE\xFF\x00\x00\x00a", "\xFF\xFE\x00\x00a\x00\x00\x00", "\xFE\xFF\x00a",
	} {
		if e := mustReject(t, in); e.Category != parse.Encoding {
			t.Errorf("%q: category %v", in, e.Category)
		}
	}
}

func TestTabIsNotContinuationIndent(t *testing.T) {
	for _, in := range []string{"k: a\n\tb\n", "k: [a\n\tb]\n", "k:\n  - a\n\tb\n"} {
		mustReject(t, in)
	}
	// A tab after the indentation is separation and stays fine.
	mustEvents(t, "k: a\n \tb\n")
	mustEvents(t, "a\n\tb\n")
}

func TestTagCharacters(t *testing.T) {
	if got := mustEvents(t, "!<tag:x.com,2000:a#b> v\n"); !strings.Contains(got, "=VAL <tag:x.com,2000:a#b> :v") {
		t.Error(got)
	}
	if got := mustEvents(t, "!foo#bar x\n"); !strings.Contains(got, "=VAL <!foo#bar> :x") {
		t.Error(got)
	}
}

func TestPropertySeparation(t *testing.T) {
	for _, in := range []string{"&a[b]\n", "&a{b: c}\n", "!a!b!c x\n", "!e!x!y x\n", "!<x%4> y\n", "!x%zz y\n", "!x%4\n", "!a[b]\n"} {
		mustReject(t, in)
	}
	for _, in := range []string{"[&a , b]\n", "[&a, b]\n", "[!!str , b]\n", "!!str x\n", "%TAG !e! tag:x,1:\n---\n!e!a b\n", "!x%41 y\n"} {
		mustEvents(t, in)
	}
}

// A line of only a tab after a block scalar's content is a comment line; where the
// content should start it is an indentation error.
func TestTabOnlyLineAfterBlockScalar(t *testing.T) {
	if got := mustEvents(t, "k: |\n a\n\t\n"); !strings.Contains(got, "=VAL |a\\n") {
		t.Error(got)
	}
	mustEvents(t, "k: |\n a\n\t\nj: 1\n")
	mustReject(t, "foo: |\n\t\nbar: 1\n")
	mustReject(t, "k: [a\n\tb]\n")
}

// The position of a bad escape in a tag is the first character that is not a hex digit.
func TestTagPercentPosition(t *testing.T) {
	for _, tc := range []struct {
		in  string
		col int
	}{{"!a%2 x\n", 5}, {"!<a%zz> x\n", 5}, {"!a%\n", 4}} {
		if e := mustReject(t, tc.in); e.Line != 1 || e.Column != tc.col {
			t.Errorf("%q: got %d:%d, want 1:%d", tc.in, e.Line, e.Column, tc.col)
		}
	}
}
