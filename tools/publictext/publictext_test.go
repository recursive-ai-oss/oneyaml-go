package publictext

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic patterns: they stand for whatever a real pattern file holds.
const testPatterns = `# a comment
(?i:zebra-internal)
(?i:\bgizmo[- ]?[0-9])
(?i:/secret/path)
(?i:\bzebra\.md\b)
\b(ZQ[1-6]a?)\b

prose: \b(zz[0-9]+)\b
`

func testChecker(t *testing.T) *Checker {
	t.Helper()
	c, err := NewChecker(testPatterns)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPatternClassesTrip(t *testing.T) {
	c := testChecker(t)
	cases := []struct{ name, file, text string }{
		{"insensitive word", "a.go", "// made by Zebra-Internal staff"},
		{"insensitive word in code", "a.go", `var x = "zebra-internal"`},
		{"numbered word", "a.go", "// gizmo 7 adds this"},
		{"numbered word dashed", "a.md", "Gizmo-12"},
		{"private path", "a.go", "// /secret/path/foo"},
		{"private file name", "a.md", "see zebra.md"},
		{"upper-case id", "a.go", "// meets ZQ3"},
		{"upper-case id with letter", "a.md", "ZQ5a says"},
		{"prose id in comment", "a.go", "// zz12 builds this"},
		{"prose id in markdown", "a.md", "from zz0"},
		{"prose id in shell comment", "a.sh", "# zz1 owns"},
		{"hash in go comment", "a.go", "// copied from " + strings.Repeat("ab12", 10)},
		{"hash in markdown", "a.md", "fixed in " + strings.Repeat("0f", 20)},
		{"inside block comment", "a.go", "/*\n zz4 here\n*/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.CheckFile(tc.file, []byte(tc.text)); len(got) == 0 {
				t.Fatalf("no finding for %q", tc.text)
			}
		})
	}
}

func TestOrdinaryTextPasses(t *testing.T) {
	c := testChecker(t)
	cases := []struct{ name, file, text string }{
		{"lower case ids as variables", "a.go", "zq1 := 1\nzq2 := zq1 + 1\n_ = zq2"},
		{"prose id in code", "a.go", "zz1 := 1\n_ = zz1"},
		{"prose id in a string", "a.go", `s := "zz1 zz2"`},
		{"spec section", "a.go", "// YAML 1.2 \u00a77.3.3 and section 5.1"},
		{"hex in go code", "a.go", `h := "da39a3ee5e6b4b0d3255bfef95601890afd80709"`},
		{"sha256 in comment", "a.go", "// " + strings.Repeat("ab", 32)},
		{"hash in test data", "testdata/x.yaml", "k: " + strings.Repeat("ab12", 10)},
		{"provenance hash", "a.md", "revision " + strings.Repeat("ab12", 10)},
		{"pinned action", "ci.yml", "uses: a/b@" + strings.Repeat("ab12", 10)},
		{"plain words", "a.md", "the shape of the parser; please review the change"},
		{"zebra as a plain word", "a.md", "a zebra crossing"},
		{"uppercase hex words", "a.go", "// 0xDEADBEEF and CAFE"},
		{"module path", "go.mod", "module oneyaml.dev/go"},
		{"url slashes", "a.go", `u := "http://example.com/zz1"`},
		{"id letters inside a token", "a.json", `{"k": "xZQ1y"}`},
		{"generic T parameter", "a.go", "func F[T any](v T) T { return v }"},
		{"ok marker", "a.md", "legacy ZQ3 publictext:ok"},
		{"vendored", "testdata/yaml-test-suite/x/in.yaml", "ZQ3 zebra-internal"},
		{"binary", "a.bin", "zebra-internal\x00"},
		{"go.sum", "go.sum", strings.Repeat("ab12", 10)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.CheckFile(tc.file, []byte(tc.text)); len(got) != 0 {
				t.Fatalf("false positive: %v", got)
			}
		})
	}
}

// Without a pattern file only the generic rules apply.
func TestNoPatternsGenericOnly(t *testing.T) {
	var c *Checker
	if got := c.CheckFile("a.md", []byte("zebra-internal ZQ3")); len(got) != 0 {
		t.Fatalf("pattern applied without a file: %v", got)
	}
	if got := c.CheckFile("a.md", []byte("fixed in "+strings.Repeat("ab12", 10))); len(got) == 0 {
		t.Fatal("hash not flagged")
	}
}

func TestPatternFile(t *testing.T) {
	if _, err := NewChecker("(unclosed"); err == nil {
		t.Fatal("bad pattern accepted")
	}
	file := filepath.Join(t.TempDir(), "p.txt")
	if err := os.WriteFile(file, []byte(testPatterns), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadChecker(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.CheckFile("a.md", []byte("zebra-internal"))) == 0 {
		t.Fatal("loaded pattern not applied")
	}
	if _, err := LoadChecker(file + ".missing"); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestCommentStateEnds(t *testing.T) {
	text := "/* open\n close */\nzz3 := 1\n_ = zz3\n"
	if got := testChecker(t).CheckFile("a.go", []byte(text)); len(got) != 0 {
		t.Fatalf("block comment state leaked into code: %v", got)
	}
}

func TestMessage(t *testing.T) {
	c := testChecker(t)
	if got := c.CheckMessage("commit abc", "Add scanner\n\nFixes zz1 ordering"); len(got) == 0 {
		t.Fatal("prose id in message not flagged")
	}
	if got := c.CheckMessage("commit abc", "Add scanner\n\nSpeeds up scanning of block scalars."); len(got) != 0 {
		t.Fatalf("clean message flagged: %v", got)
	}
	if got := c.CheckMessage("commit abc", "Revert x\n\nThis reverts commit "+strings.Repeat("ab12", 10)+"."); len(got) != 0 {
		t.Fatalf("revert flagged: %v", got)
	}
	if got := c.CheckMessage("commit abc", "Fix\n\nsee "+strings.Repeat("ab12", 10)); len(got) == 0 {
		t.Fatal("bare hash in message not flagged")
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestGitModes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, dir, "init", "-q", "-b", "main")
	write("old.md", "clean\n")
	write("gone.md", "clean\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")
	gitRun(t, dir, "checkout", "-q", "-b", "topic")
	write("new.md", "from zz2"+"\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "topic zz2")
	write("old.md", "now ZQ3\n")
	write("untracked.md", "clean\n")
	if err := os.Remove(filepath.Join(dir, "gone.md")); err != nil {
		t.Fatal(err)
	}

	changed, err := ChangedFiles(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(changed, ",")
	for _, want := range []string{"new.md", "old.md", "untracked.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("changed files %q lack %s", got, want)
		}
	}
	if strings.Contains(got, "gone.md") {
		t.Errorf("deleted file listed: %q", got)
	}
	if _, err := ChangedFiles(dir, "nope"); err == nil {
		t.Error("missing base accepted")
	}
	tree, err := TreeFiles(dir)
	if err != nil || len(tree) < 3 {
		t.Fatalf("tree files %v, %v", tree, err)
	}
	msgs, err := testChecker(t).CheckCommits(dir, "main..HEAD")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("commit findings %v, %v", msgs, err)
	}
	all, err := testChecker(t).CheckCommits(dir, "HEAD")
	if err != nil || len(all) != 1 {
		t.Fatalf("whole-history findings %v, %v", all, err)
	}
}
