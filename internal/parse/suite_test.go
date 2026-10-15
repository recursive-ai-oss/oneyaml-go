package parse_test

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/parse"
	"oneyaml.dev/go/internal/scan"
)

// The suite is not vendored in this repository. Point ONEYAML_YTS_DIR at a
// checkout of yaml-test-suite's data-2022-01-17 directory (the one holding one
// directory per case). Optionally ONEYAML_YTS_CORRECTIONS names a JSON file of
// corrected expectations in the format described in loadCorrections.

type ytsCase struct {
	id       string
	dir      string
	in       []byte
	expected []string // expected event lines
	isError  bool
}

func suiteDir(t testing.TB) string {
	dir := os.Getenv("ONEYAML_YTS_DIR")
	if dir == "" {
		t.Skip("ONEYAML_YTS_DIR is not set")
	}
	return dir
}

// loadCorrections reads a JSON document {"entries":[{"case":"yts:ID[/NN]","events":[...]}]}
// and returns the corrected event lists by case id.
func loadCorrections(t testing.TB) map[string][]string {
	out := map[string][]string{}
	path := os.Getenv("ONEYAML_YTS_CORRECTIONS")
	if path == "" {
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Entries []struct {
			Case   string   `json:"case"`
			Events []string `json:"events"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, e := range doc.Entries {
		if e.Events != nil {
			out[strings.TrimPrefix(e.Case, "yts:")] = e.Events
		}
	}
	return out
}

func loadCases(t testing.TB) []ytsCase {
	root := suiteDir(t)
	corr := loadCorrections(t)
	var cases []ytsCase
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	add := func(id, dir string) {
		in, err := os.ReadFile(filepath.Join(dir, "in.yaml"))
		if err != nil {
			return
		}
		ev, err := os.ReadFile(filepath.Join(dir, "test.event"))
		if err != nil {
			return
		}
		c := ytsCase{id: id, dir: dir, in: in}
		c.expected = strings.Split(strings.TrimRight(string(ev), "\n"), "\n")
		if x, ok := corr[id]; ok {
			c.expected = x
		}
		if _, err := os.Stat(filepath.Join(dir, "error")); err == nil {
			c.isError = true
		}
		cases = append(cases, c)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "in.yaml")); err == nil {
			add(e.Name(), dir)
			continue
		}
		subs, _ := os.ReadDir(dir)
		for _, s := range subs {
			if s.IsDir() {
				add(e.Name()+"/"+s.Name(), filepath.Join(dir, s.Name()))
			}
		}
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].id < cases[j].id })
	return cases
}

func escapeVal(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		switch c {
		case '\\':
			sb.WriteString(`\\`)
		case 0:
			sb.WriteString(`\0`)
		case 8:
			sb.WriteString(`\b`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// eventLines parses src fully and returns the event lines in the suite's
// notation, plus the error that stopped the parse, if any.
func eventLines(src []byte) ([]string, error) {
	p := parse.New(src, 0)
	var lines []string
	var buf []byte
	for {
		ev, err := p.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return lines, nil
			}
			return lines, err
		}
		var sb strings.Builder
		props := func() {
			if ev.HasAnchor() {
				sb.WriteString(" &" + string(src[ev.AnchorStart:ev.AnchorEnd]))
			}
			if ev.HasTag() {
				var terr error
				buf, terr = p.AppendTag(buf[:0], &ev)
				if terr != nil {
					sb.WriteString(" <ERR>")
				} else {
					sb.WriteString(" <" + string(buf) + ">")
				}
			}
		}
		switch ev.Kind {
		case parse.StreamStart:
			sb.WriteString("+STR")
		case parse.StreamEnd:
			sb.WriteString("-STR")
		case parse.DocumentStart:
			sb.WriteString("+DOC")
			if ev.Explicit {
				sb.WriteString(" ---")
			}
		case parse.DocumentEnd:
			sb.WriteString("-DOC")
			if ev.Explicit {
				sb.WriteString(" ...")
			}
		case parse.SequenceStart:
			sb.WriteString("+SEQ")
			if ev.Flow {
				sb.WriteString(" []")
			}
			props()
		case parse.MappingStart:
			sb.WriteString("+MAP")
			if ev.Flow {
				sb.WriteString(" {}")
			}
			props()
		case parse.SequenceEnd:
			sb.WriteString("-SEQ")
		case parse.MappingEnd:
			sb.WriteString("-MAP")
		case parse.Alias:
			sb.WriteString("=ALI *" + string(src[ev.Start+1:ev.End]))
		case parse.Scalar:
			sb.WriteString("=VAL")
			props()
			var verr error
			buf, verr = p.AppendScalar(buf[:0], &ev)
			if verr != nil {
				return lines, verr
			}
			sb.WriteString(" " + string(":'\"|>"[ev.Style]) + escapeVal(buf))
		}
		lines = append(lines, sb.String())
	}
}

// prefixDeviations lists invalid inputs that are rejected, as required, but
// whose events before the error differ from the expected notation. The test
// insists the input is still rejected and that the difference is still there,
// so a fix shows up as a failure that asks for the entry to be removed.
var prefixDeviations = map[string]string{
	"H7J7": "the expected events emit the anchored empty scalar before the error; this parser reports the error at the tag that follows the anchor",
	"SY6V": "the expected events stop before the document start; this parser starts the document, then rejects the '-' after the anchor",
}

func TestSuiteEvents(t *testing.T) {
	cases := loadCases(t)
	pass := 0
	var failed []string
	for _, c := range cases {
		lines, err := eventLines(c.in)
		ok := true
		why := ""
		if c.isError && err == nil {
			ok, why = false, "accepted invalid input"
		} else if !c.isError && err != nil {
			ok, why = false, "rejected valid input: "+err.Error()
		} else if strings.Join(lines, "\n") != strings.Join(c.expected, "\n") {
			ok, why = false, "events differ"
			if err != nil {
				why += " (before error: " + err.Error() + ")"
			}
			if testing.Verbose() {
				t.Logf("%s: want\n%s\ngot\n%s", c.id, strings.Join(c.expected, "\n"), strings.Join(lines, "\n"))
			}
		}
		if _, known := prefixDeviations[c.id]; known {
			if c.isError && err != nil && !ok {
				pass++ // rejected, with the documented difference in the events before the error
			} else {
				t.Errorf("%s: listed in prefixDeviations but behaviour changed (ok=%v); update the list", c.id, ok)
			}
			continue
		}
		if ok {
			pass++
		} else {
			failed = append(failed, c.id+": "+why)
		}
	}
	t.Logf("suite: %d of %d cases pass", pass, len(cases))
	for _, f := range failed {
		t.Error(f)
	}
}

// isTrivia reports whether b consists only of what may lie between tokens:
// white space, line breaks, comments and byte order marks.
func isTrivia(b []byte) bool {
	for i := 0; i < len(b); {
		switch c := b[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '#':
			for i < len(b) && b[i] != '\n' && b[i] != '\r' {
				i++
			}
		case c == 0xEF && i+2 < len(b) && b[i+1] == 0xBB && b[i+2] == 0xBF:
			i += 3
		default:
			return false
		}
	}
	return true
}

// reconstruct scans src and returns the bytes rebuilt from token spans and the
// gaps between them, plus a description of the first structural problem.
func reconstruct(src []byte) (out []byte, problem string) {
	s := scan.New(src, 0)
	cursor := 0
	for {
		tok, err := s.Peek()
		if err != nil {
			break
		}
		if tok.Start < cursor || tok.End < tok.Start || tok.End > len(src) {
			return out, "token span out of order"
		}
		gap := src[cursor:tok.Start]
		if !isTrivia(gap) {
			return out, "non-trivia bytes between tokens: " + string(gap)
		}
		out = append(out, gap...)
		out = append(out, src[tok.Start:tok.End]...)
		cursor = tok.End
		kind := tok.Kind
		s.Skip()
		if kind == scan.StreamEnd {
			break
		}
	}
	// After a clean end the tail is trivia; after an error it is whatever
	// the scanner had not reached.
	if string(out) != string(src[:len(out)]) {
		return out, "rebuilt bytes differ"
	}
	return append(out, src[cursor:]...), ""
}

func TestSuiteReconstruction(t *testing.T) {
	cases := loadCases(t)
	bad := 0
	for _, c := range cases {
		out, problem := reconstruct(c.in)
		if problem == "" && string(out) != string(c.in) {
			problem = "output differs from input"
		}
		if problem != "" {
			bad++
			t.Errorf("%s: %s", c.id, problem)
		}
	}
	t.Logf("reconstruction: %d of %d inputs byte-exact", len(cases)-bad, len(cases))
}

// TestSuiteCase prints one case in detail: ONEYAML_YTS_CASE=2JQS go test -run SuiteCase -v
func TestSuiteCase(t *testing.T) {
	id := os.Getenv("ONEYAML_YTS_CASE")
	if id == "" {
		t.Skip("ONEYAML_YTS_CASE is not set")
	}
	for _, c := range loadCases(t) {
		if c.id != id {
			continue
		}
		lines, err := eventLines(c.in)
		t.Logf("input:\n%q\nerror case: %v\nerr: %v\nwant:\n%s\ngot:\n%s", c.in, c.isError, err,
			strings.Join(c.expected, "\n"), strings.Join(lines, "\n"))
	}
}
