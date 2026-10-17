package oneyaml_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Markdown documents show Go code that must compile and run. Every fenced go
// block is the body of an Example function, and the line before the fence names
// it:
//
//	<!-- example: ExampleDocument_Set -->
//	<!-- example: compat/yamlv3 ExampleNode -->
//
// A name alone looks in the root package; a directory before it looks in that
// package's tests. This test fails when a block has no marker, when the named
// Example does not exist or has no Output comment, and when the block differs
// from the Example's body. It also checks that every relative link and anchor in
// the Markdown files resolves.

var (
	markerRE = regexp.MustCompile(`^<!-- example: (?:(\S+) )?(Example\w*) -->$`)
	linkRE   = regexp.MustCompile(`\]\(([^)\s]+)\)`)
)

func markdownFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, pat := range []string{"*.md", "docs/*.md", ".github/*.md", ".github/*/*.md"} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	return files
}

// exampleBody returns the body of the named Example in dir's test files, with one
// tab of indentation removed.
func exampleBody(t *testing.T, dir, name string) (string, bool) {
	t.Helper()
	tests, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range tests {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Name.Name != name || fn.Recv != nil {
				continue
			}
			body := string(src[fset.Position(fn.Body.Lbrace).Offset+1 : fset.Position(fn.Body.Rbrace).Offset])
			var lines []string
			for _, l := range strings.Split(body, "\n") {
				lines = append(lines, strings.TrimRight(strings.TrimPrefix(l, "\t"), " \t\r"))
			}
			return strings.Trim(strings.Join(lines, "\n"), "\n"), true
		}
	}
	return "", false
}

func TestDocsGoBlocksAreExamples(t *testing.T) {
	blocks := 0
	for _, file := range markdownFiles(t) {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		for i := 0; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) != "```go" {
				continue
			}
			start := i + 1
			end := start
			for end < len(lines) && strings.TrimSpace(lines[end]) != "```" {
				end++
			}
			if end == len(lines) {
				t.Errorf("%s:%d: unterminated go block", file, i+1)
				break
			}
			i = end
			blocks++

			var m []string
			if start >= 2 {
				m = markerRE.FindStringSubmatch(strings.TrimSpace(lines[start-2]))
			}
			if m == nil {
				t.Errorf("%s:%d: go block without a <!-- example: Name --> line before it", file, start)
				continue
			}
			dir := m[1]
			if dir == "" {
				dir = "."
			}
			want, ok := exampleBody(t, dir, m[2])
			if !ok {
				t.Errorf("%s:%d: no %s in %s", file, start, m[2], dir)
				continue
			}
			if !strings.Contains(want, "// Output:") {
				t.Errorf("%s:%d: %s has no Output comment, so go test does not run it", file, start, m[2])
			}
			var got []string
			for _, l := range lines[start:end] {
				got = append(got, strings.TrimRight(l, " \t\r"))
			}
			if g := strings.Join(got, "\n"); g != want {
				t.Errorf("%s:%d: block differs from %s\n--- block\n%s\n--- example\n%s", file, start, m[2], g, want)
			}
		}
	}
	if blocks == 0 {
		t.Skip("no go blocks in the Markdown files")
	}
}

// slug returns the anchor GitHub gives a heading.
func slug(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	var b strings.Builder
	for _, r := range h {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r > 127:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func anchors(t *testing.T, file string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	seen := map[string]int{}
	inFence := false
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, "```") {
			inFence = !inFence
		}
		if inFence || !strings.HasPrefix(l, "#") {
			continue
		}
		s := slug(strings.TrimLeft(l, "# "))
		if n := seen[s]; n > 0 {
			set[s+"-"+string(rune('0'+n))] = true
		} else {
			set[s] = true
		}
		seen[s]++
	}
	return set
}

func TestDocsLinksResolve(t *testing.T) {
	for _, file := range markdownFiles(t) {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		inFence := false
		for n, l := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(l, "```") {
				inFence = !inFence
			}
			if inFence {
				continue
			}
			for _, m := range linkRE.FindAllStringSubmatch(l, -1) {
				target := m[1]
				if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				path, frag, _ := strings.Cut(target, "#")
				dest := file
				if path != "" {
					dest = filepath.Join(filepath.Dir(file), path)
					if _, err := os.Stat(dest); err != nil {
						t.Errorf("%s:%d: link %q: %v", file, n+1, target, err)
						continue
					}
				}
				if frag != "" && strings.HasSuffix(dest, ".md") && !anchors(t, dest)[frag] {
					t.Errorf("%s:%d: link %q: no heading %q in %s", file, n+1, target, frag, dest)
				}
			}
		}
	}
}

var (
	generatedBeginRE = regexp.MustCompile(`^<!-- generated:([a-z-]+) -->$`)
	reportBeginRE    = regexp.MustCompile(`^<!-- kit-report:([a-z-]+) -->$`)
	reportEndRE      = regexp.MustCompile(`^<!-- end kit-report:([a-z-]+) -->$`)
)

// The Markdown files hold two kinds of generated region, and this test checks
// their shape, not their numbers.
//
// Benchmark regions, <!-- generated:KIND --> ... <!-- end generated -->, are
// written by the oneyaml-kit `bin/bench readme` command. Every begin marker
// names a kind the renderer knows and is closed before the next begins, and no
// end marker stands alone. Every chart a region shows exists.
//
// Conformance report regions, <!-- kit-report:NAME --> ... <!-- end kit-report:NAME -->,
// are the kit's own report pasted in at release. The renderer never sees them.
// Each is closed by an end marker of its own name.
//
// The pages that must hold regions hold them.
func TestDocsGeneratedBlocksAreWellFormed(t *testing.T) {
	kinds := map[string]bool{"bench-hero": true, "bench-chart": true, "bench-full": true, "conformance-line": true, "conformance-deviations": true}
	reports := map[string]bool{"conformance-summary": true, "conformance": true}
	required := map[string][]string{
		"README.md":           {"bench-chart"},
		"docs/performance.md": {"bench-hero", "bench-full"},
		"docs/conformance.md": {"conformance"},
	}
	srcRE := regexp.MustCompile(`(?:src|srcset)="([^"]+)"`)
	for _, file := range markdownFiles(t) {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]int{}
		open := "" // the name of the open region, of either family
		report := false
		for i, line := range strings.Split(strings.ReplaceAll(string(src), "\r", ""), "\n") {
			line = strings.TrimSpace(line)
			where := func(format string, args ...any) {
				t.Errorf("%s:%d: "+format, append([]any{file, i + 1}, args...)...)
			}
			if m := generatedBeginRE.FindStringSubmatch(line); m != nil {
				if open != "" {
					where("block %q begins inside block %q", m[1], open)
				}
				if !kinds[m[1]] {
					where("unknown generated block kind %q", m[1])
				}
				open, report = m[1], false
				seen[m[1]]++
				continue
			}
			if m := reportBeginRE.FindStringSubmatch(line); m != nil {
				if open != "" {
					where("report %q begins inside block %q", m[1], open)
				}
				if !reports[m[1]] {
					where("unknown report region %q", m[1])
				}
				open, report = m[1], true
				seen[m[1]]++
				continue
			}
			if line == "<!-- end generated -->" {
				if open == "" || report {
					where("end marker does not close a generated block")
				}
				open = ""
				continue
			}
			if m := reportEndRE.FindStringSubmatch(line); m != nil {
				if open == "" || !report || open != m[1] {
					where("end marker for report %q does not close the open region", m[1])
				}
				open = ""
				continue
			}
			if open != "" && !report {
				for _, m := range srcRE.FindAllStringSubmatch(line, -1) {
					if strings.Contains(m[1], "://") {
						continue
					}
					if _, err := os.Stat(filepath.Join(filepath.Dir(file), m[1])); err != nil {
						where("chart %s does not exist", m[1])
					}
				}
			}
		}
		if open != "" {
			t.Errorf("%s: region %q is not closed", file, open)
		}
		for kind, n := range seen {
			if n > 1 {
				t.Errorf("%s: region %q appears %d times", file, kind, n)
			}
		}
		for _, kind := range required[filepath.ToSlash(file)] {
			if seen[kind] == 0 {
				t.Errorf("%s: no %q region", file, kind)
			}
		}
	}
}
