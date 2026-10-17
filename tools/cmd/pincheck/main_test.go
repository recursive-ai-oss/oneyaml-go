package main

import (
	"os"
	"path/filepath"
	"testing"
)

const workflow = `
      - uses: actions/checkout@x
        with:
          repository: recursive-ai-oss/oneyaml-kit
          ref: 8452eac464a209e3842e3ba6f4001c7ad223d1b0 # kit main
          path: oneyaml-kit
`

func write(t *testing.T, root, name, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tree(t *testing.T, rev string) string {
	root := t.TempDir()
	write(t, root, ".github/workflows/ci.yml", workflow)
	region := "<!-- kit-report:x -->\nMethod: kit revision `" + rev + "`, run.\n<!-- end kit-report:x -->\n"
	write(t, root, "README.md", "outside kit revision `deadbeef0` is ignored\n"+region)
	write(t, root, "docs/conformance.md", region)
	return root
}

func TestPrefixPasses(t *testing.T) {
	if p := run(tree(t, "8452eac464a2")); len(p) != 0 {
		t.Fatalf("unexpected problems: %v", p)
	}
}

func TestMismatchFails(t *testing.T) {
	if p := run(tree(t, "6039fe1c50c3")); len(p) != 2 {
		t.Fatalf("want one problem per file, got %v", p)
	}
}

func TestMissingRevisionFails(t *testing.T) {
	root := tree(t, "8452eac464a2")
	write(t, root, "docs/conformance.md", "no regions here\n")
	if p := run(root); len(p) != 1 {
		t.Fatalf("want one problem, got %v", p)
	}
}

// The README may carry no conformance figures at all.
func TestReadmeWithoutRegionsPasses(t *testing.T) {
	root := tree(t, "8452eac464a2")
	write(t, root, "README.md", "no regions here\n")
	if p := run(root); len(p) != 0 {
		t.Fatalf("unexpected problems: %v", p)
	}
}
