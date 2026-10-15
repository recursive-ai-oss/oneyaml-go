// Package publictext checks that files and commit messages of a public
// repository carry no internal process text.
//
// What counts as internal (private names, paths, identifier schemes) is not
// written in this repository: it is read at run time from a pattern file, so the
// list of forbidden terms is not itself published. Two generic checks need no
// file: a commit hash outside a provenance line, and the "publictext:ok" marker
// that excuses one line.
//
// Pattern file format: one regular expression (Go syntax) per line; blank lines
// and lines starting with "#" are ignored. A line starting with "prose:" holds a
// pattern that is matched only in comments, Markdown and commit messages, for
// short names that are ordinary identifiers in code. Every other line is matched
// against whole lines.
package publictext

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Checker holds the compiled patterns. The zero value and nil apply only the
// generic checks.
type Checker struct {
	general *regexp.Regexp // matched against whole lines
	prose   *regexp.Regexp // matched against comment text only
}

// NewChecker compiles a pattern file's content.
func NewChecker(patterns string) (*Checker, error) {
	var gen, pro []string
	for n, line := range strings.Split(patterns, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		list := &gen
		if rest, ok := strings.CutPrefix(line, "prose:"); ok {
			line, list = strings.TrimSpace(rest), &pro
		}
		if _, err := regexp.Compile(line); err != nil {
			return nil, fmt.Errorf("pattern line %d: %v", n+1, err)
		}
		*list = append(*list, line)
	}
	c := &Checker{}
	if len(gen) > 0 {
		c.general = regexp.MustCompile(strings.Join(gen, "|"))
	}
	if len(pro) > 0 {
		c.prose = regexp.MustCompile(strings.Join(pro, "|"))
	}
	return c, nil
}

// Empty reports whether the checker holds no pattern at all.
func (c *Checker) Empty() bool { return c == nil || (c.general == nil && c.prose == nil) }

// LoadChecker reads the pattern file at path.
func LoadChecker(file string) (*Checker, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return NewChecker(string(data))
}

var (
	hashRE = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	// A line that states where something came from may name a commit.
	provenanceRE = regexp.MustCompile(`(?i)revision|provenance|uses:|reverts commit`)
)

// okMarker excuses the line it is on.
const okMarker = "publictext:ok"

// maxFileSize bounds what is read; larger files are the secret scanner's concern.
const maxFileSize = 1 << 20

// skipPrefixes are vendored third-party trees, which are not ours to edit.
var skipPrefixes = []string{"testdata/yaml-test-suite/"}

// skipNames are files the check never reads.
var skipNames = map[string]bool{"go.sum": true, "LICENSE-APACHE": true, "LICENSE-MIT": true}

// Finding is one match.
type Finding struct {
	Path  string // file path, or a label such as "commit 1a2b3c4"
	Line  int
	Match string
	Text  string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %q: %s", f.Path, f.Line, f.Match, f.Text)
}

type style int

const (
	styleOther style = iota // no comment syntax known
	styleGo                 // // and /* */ comments
	styleHash               // # comments
	styleProse              // every line is prose
)

func styleOf(name string) style {
	switch strings.ToLower(path.Ext(name)) {
	case ".go":
		return styleGo
	case ".yml", ".yaml", ".sh", ".toml", ".mod", ".gitignore":
		return styleHash
	case ".md", ".txt":
		return styleProse
	}
	if path.Base(name) == ".gitignore" || path.Ext(name) == "" {
		return styleHash // scripts and dot files without an extension
	}
	return styleOther
}

// commentPart returns the comment text of a line, tracking block comments in
// *inBlock.
func commentPart(line string, st style, inBlock *bool) string {
	switch st {
	case styleProse:
		return line
	case styleHash:
		if i := strings.Index(line, "#"); i >= 0 {
			return line[i:]
		}
	case styleGo:
		if *inBlock {
			if i := strings.Index(line, "*/"); i >= 0 {
				*inBlock = false
				return line[:i]
			}
			return line
		}
		for i := 0; i+1 < len(line); i++ {
			if line[i] != '/' {
				continue
			}
			if line[i+1] == '/' && (i == 0 || line[i-1] != ':') {
				return line[i:]
			}
			if line[i+1] == '*' {
				rest := line[i+2:]
				if j := strings.Index(rest, "*/"); j >= 0 {
					return rest[:j]
				}
				*inBlock = true
				return rest
			}
		}
	}
	return ""
}

// checkText scans text line by line. label names it in findings. withHash
// enables the commit-hash rule for comment text.
func (c *Checker) checkText(label, text string, st style, withHash bool) []Finding {
	var out []Finding
	inBlock := false
	for n, line := range strings.Split(text, "\n") {
		comment := commentPart(line, st, &inBlock)
		if strings.Contains(line, okMarker) {
			continue
		}
		add := func(m string) {
			t := strings.TrimSpace(line)
			if len(t) > 100 {
				t = t[:100]
			}
			out = append(out, Finding{label, n + 1, m, t})
		}
		if c != nil && c.general != nil {
			if m := c.general.FindString(line); m != "" {
				add(m)
				continue
			}
		}
		if c != nil && c.prose != nil {
			if m := c.prose.FindString(comment); m != "" {
				add(m)
				continue
			}
		}
		if withHash && !provenanceRE.MatchString(line) {
			target := comment
			if st == styleOther {
				target = line
			}
			if m := hashRE.FindString(target); m != "" {
				add("commit hash outside a provenance line")
			}
		}
	}
	return out
}

// CheckFile checks one file's content. name is the slash-separated path
// relative to the repository root. Binary, oversized and vendored files are
// not checked.
func (c *Checker) CheckFile(name string, data []byte) []Finding {
	name = filepath.ToSlash(name)
	for _, p := range skipPrefixes {
		if strings.HasPrefix(name, p) {
			return nil
		}
	}
	if skipNames[path.Base(name)] || len(data) > maxFileSize {
		return nil
	}
	head := data
	if len(head) > 4096 {
		head = head[:4096]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return nil
	}
	// Test data holds arbitrary hex, so the hash rule does not apply to it.
	withHash := !strings.HasPrefix(name, "testdata/") && !strings.Contains(name, "/testdata/")
	return c.checkText(name, string(data), styleOf(name), withHash)
}

// CheckMessage checks a commit message. label names the commit in findings.
func (c *Checker) CheckMessage(label, msg string) []Finding {
	return c.checkText(label, msg, styleProse, true)
}

// git runs git in dir and returns its standard output.
func git(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func splitZ(b []byte) []string {
	var out []string
	for _, s := range strings.Split(string(b), "\x00") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Toplevel returns the root of the repository containing dir.
func Toplevel(dir string) (string, error) {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TreeFiles lists the tracked files plus untracked, not-ignored files of the
// repository at root.
func TreeFiles(root string) ([]string, error) {
	out, err := git(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return splitZ(out), nil
}

// ChangedFiles lists files that differ from the merge base of base and HEAD
// (committed or in the working tree) plus untracked, not-ignored files.
// Deleted files are left out.
func ChangedFiles(root, base string) ([]string, error) {
	from := base
	if mb, err := git(root, "merge-base", base, "HEAD"); err == nil {
		from = strings.TrimSpace(string(mb))
	} else if _, err := git(root, "rev-parse", "--verify", "--quiet", base); err != nil {
		return nil, fmt.Errorf("base %q not found", base)
	}
	diff, err := git(root, "diff", "--name-only", "-z", "--diff-filter=d", from)
	if err != nil {
		return nil, err
	}
	others, err := git(root, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range append(splitZ(diff), splitZ(others)...) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

// CheckCommits checks the messages of the commits in rng, a revision range
// such as "a..b" or a single revision for its whole history.
func (c *Checker) CheckCommits(root, rng string) ([]Finding, error) {
	out, err := git(root, "log", "--format=%H%x00%B%x00", rng)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(out), "\x00")
	var res []Finding
	for i := 0; i+1 < len(parts); i += 2 {
		sha := strings.TrimSpace(parts[i])
		if sha == "" {
			continue
		}
		res = append(res, c.CheckMessage("commit "+sha[:7], parts[i+1])...)
	}
	return res, nil
}
