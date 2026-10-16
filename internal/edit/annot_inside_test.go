package edit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/cst"
)

// insideScalar finds the scalar by search; it must agree with reading every node.
func checkInsideScalar(t *testing.T, name string, src []byte) {
	t.Helper()
	var tree cst.Tree
	if err := cst.Build(&tree, src, -1); err != nil {
		return
	}
	c := NewCtx(&tree, src, 0)
	skips := []int32{0}
	for i := 1; i < len(tree.Nodes); i++ {
		if tree.Nodes[i].Kind == cst.Scalar {
			skips = append(skips, int32(i))
		}
	}
	if len(skips) > 12 {
		step := len(skips) / 12
		var some []int32
		for i := 0; i < len(skips); i += step {
			some = append(some, skips[i])
		}
		skips = some
	}
	for at := 0; at <= len(src); at++ {
		for _, sk := range skips {
			if got, want := c.insideScalar(at, sk), c.insideScalarAll(at, sk); got != want {
				t.Fatalf("%s: insideScalar(%d, %d) = %v, reading every node says %v", name, at, sk, got, want)
			}
		}
	}
}

func TestInsideScalarAgreesWithFullRead(t *testing.T) {
	docs := []string{
		"a: 1\nb: two words\nc: 'q\n  r'\nd: \"x\n  y\"\n",
		"- a\n- b: |\n    lit\n\n    eral\n- >-\n  fold\n  ed\n- [x, 'y z', {k: v}]\n",
		"? a\n: b\n? [c]\n: {d: e}\n",
		"a:\nb:\nc:\n- \n",
		"--- a\n...\n--- b: 1\n---\n- x\n...\n",
		"&a k: *a\n? &b x\n: !!str y\n",
		"top: # c\n  - 1 # d\n  # e\n  - plain\n    continued\n",
		strings.Repeat("- ", 200) + "x\n",
		strings.Repeat("k:\n", 200) + "z: 1\n",
		strings.Repeat("a: [1, 2]\n", 100) + "t: |\n  x\n",
	}
	for i, d := range docs {
		checkInsideScalar(t, "doc"+string(rune('0'+i)), []byte(d))
	}
	var big strings.Builder
	for i := 0; i < 300; i++ {
		big.WriteString("k")
		big.WriteString(strings.Repeat("x", i%7))
		big.WriteString(": v")
		big.WriteString(strings.Repeat("y", i%5))
		big.WriteString("\n")
	}
	checkInsideScalar(t, "big", []byte(big.String()))

	if dir := os.Getenv("ONEYAML_YTS_DIR"); dir != "" && !testing.Short() {
		files, _ := filepath.Glob(filepath.Join(dir, "*", "in.yaml"))
		more, _ := filepath.Glob(filepath.Join(dir, "*", "*", "in.yaml"))
		for _, f := range append(files, more...) {
			src, err := os.ReadFile(f)
			if err == nil && len(src) < 4096 {
				checkInsideScalar(t, f, src)
			}
		}
	}
}
