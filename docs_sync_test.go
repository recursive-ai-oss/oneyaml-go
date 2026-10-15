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
