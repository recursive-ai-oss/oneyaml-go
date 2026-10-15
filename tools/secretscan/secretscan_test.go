package secretscan

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func has(kinds []string, want string) bool {
	for _, k := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

// Samples are built from pieces so this file holds no secret-shaped text.
func TestShapesTrip(t *testing.T) {
	long := strings.Repeat("aB3", 14)
	hex := strings.Repeat("a1", 25)
	cases := []struct{ kind, text string }{
		{"anthropic key", "sk-" + "ant-" + long},
		{"openai key", "sk-" + "proj-" + long},
		{"aws access key id", "AK" + "IA" + strings.Repeat("A1", 8)},
		{"google api key", "AI" + "za" + strings.Repeat("a", 35)},
		{"github token", "gh" + "p_" + long},
		{"gitlab token", "gl" + "pat-" + long},
		{"slack token", "xo" + "xb-" + "1234567890abc"},
		{"crates.io token", "ci" + "o" + long},
		{"elevenlabs key", "sk" + "_" + hex},
		{"firecrawl key", "fc" + "-" + hex},
		{"private key", "-----BEGIN " + "RSA PRIVATE " + "KEY-----"},
		{"secret assignment", "MY_API" + "_KEY = \"" + long + "\""},
	}
	for _, tc := range cases {
		if got := Inspect("f", len(tc.text), []byte(tc.text)); !has(got, tc.kind) {
			t.Errorf("%s: not detected, got %v", tc.kind, got)
		}
	}
}

func TestOrdinaryContentPasses(t *testing.T) {
	for _, s := range []string{
		"package x\n\nfunc F() {}\n",
		"key: value\nlist: [1, 2, 3]\n",
		"GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}",
		"sk-short",
		"password = \"x\"",
		strings.Repeat("ab", 1000),
	} {
		if got := Inspect("f", len(s), []byte(s)); len(got) != 0 {
			t.Errorf("false positive %v for %.30q", got, s)
		}
	}
	// The shape definitions in the source must not trip the scanner.
	src, err := os.ReadFile("secretscan.go")
	if err != nil {
		t.Fatal(err)
	}
	if got := Inspect("secretscan.go", len(src), src); len(got) != 0 {
		t.Errorf("own source flagged: %v", got)
	}
}

func TestBinaryAndSize(t *testing.T) {
	if got := Inspect("f", 8, []byte("\x7fELF\x02\x01\x01\x00")); !has(got, "ELF binary or core dump") {
		t.Errorf("ELF not detected: %v", got)
	}
	if got := Inspect("f", 8, []byte{0xcf, 0xfa, 0xed, 0xfe, 0, 0, 0, 0}); !has(got, "Mach-O binary") {
		t.Errorf("Mach-O not detected: %v", got)
	}
	if got := Inspect("f", MaxBlob+1, nil); len(got) != 1 || !strings.HasPrefix(got[0], "larger than 1 MB") {
		t.Errorf("oversize not detected: %v", got)
	}
	if got := Inspect("f", MaxBlob, make([]byte, 10)); len(got) != 0 {
		t.Errorf("blob at the limit flagged: %v", got)
	}
}

func TestScanRange(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name string, b []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("ok.txt", []byte("fine\n"))
	git("add", ".")
	git("commit", "-q", "-m", "first")
	write("key.txt", []byte("sk-"+"ant-"+strings.Repeat("aB3", 14)))
	write("prog", []byte("\x7fELF\x02\x01\x01\x00"))
	write("big.dat", make([]byte, MaxBlob+1))
	git("add", ".")
	git("commit", "-q", "-m", "second")

	res, blobs, err := ScanRange(dir, "HEAD~1..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if blobs != 3 {
		t.Errorf("scanned %d blobs, want 3", blobs)
	}
	kinds := map[string]string{}
	for _, f := range res {
		kinds[f.Path] = f.Kind
	}
	if kinds["key.txt"] != "anthropic key" || kinds["prog"] != "ELF binary or core dump" ||
		!strings.HasPrefix(kinds["big.dat"], "larger than 1 MB") || len(res) != 3 {
		t.Errorf("unexpected findings: %v", res)
	}
	for _, f := range res {
		if strings.Contains(f.String(), "aB3aB3") {
			t.Errorf("finding leaks the value: %s", f)
		}
	}
	// The first commit alone is clean.
	res, _, err = ScanRange(dir, "HEAD~1")
	if err != nil || len(res) != 0 {
		t.Errorf("clean history flagged: %v, %v", res, err)
	}
	if _, _, err := ScanRange(dir, "nope..HEAD"); err == nil {
		t.Error("bad range accepted")
	}
}
