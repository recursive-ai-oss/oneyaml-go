package bench

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	yv3 "go.yaml.in/yaml/v3"
	compat "oneyaml.dev/go/compat/yamlv3"
)

// ---- acceptance: change a Node and write it again ----

// mutateV3 and mutateCompat make the same edits to the same tree: every scalar
// that is a mapping value or sequence item gets "-edited" appended, and the first
// mapping or sequence gets a new last entry.
func mutateV3(n *yv3.Node) {
	var walk func(n *yv3.Node, isKey bool)
	walk = func(n *yv3.Node, isKey bool) {
		switch n.Kind {
		case yv3.ScalarNode:
			if !isKey && n.Style&(yv3.LiteralStyle|yv3.FoldedStyle) == 0 && n.Tag == "!!str" {
				n.Value += "-edited"
			}
		case yv3.MappingNode:
			for i, c := range n.Content {
				walk(c, i%2 == 0)
			}
		default:
			for _, c := range n.Content {
				walk(c, false)
			}
		}
	}
	walk(n, false)
	if len(n.Content) > 0 {
		top := n.Content[0]
		switch top.Kind {
		case yv3.SequenceNode:
			top.Content = append(top.Content, &yv3.Node{Kind: yv3.ScalarNode, Tag: "!!str", Value: "added"})
		case yv3.MappingNode:
			top.Content = append(top.Content, &yv3.Node{Kind: yv3.ScalarNode, Tag: "!!str", Value: "added"}, &yv3.Node{Kind: yv3.ScalarNode, Tag: "!!int", Value: "1"})
		}
	}
}

func mutateCompat(n *compat.Node) {
	var walk func(n *compat.Node, isKey bool)
	walk = func(n *compat.Node, isKey bool) {
		switch n.Kind {
		case compat.ScalarNode:
			if !isKey && n.Style&(compat.LiteralStyle|compat.FoldedStyle) == 0 && n.Tag == "!!str" {
				n.Value += "-edited"
			}
		case compat.MappingNode:
			for i, c := range n.Content {
				walk(c, i%2 == 0)
			}
		default:
			for _, c := range n.Content {
				walk(c, false)
			}
		}
	}
	walk(n, false)
	if len(n.Content) > 0 {
		top := n.Content[0]
		switch top.Kind {
		case compat.SequenceNode:
			top.Content = append(top.Content, &compat.Node{Kind: compat.ScalarNode, Tag: "!!str", Value: "added"})
		case compat.MappingNode:
			top.Content = append(top.Content, &compat.Node{Kind: compat.ScalarNode, Tag: "!!str", Value: "added"}, &compat.Node{Kind: compat.ScalarNode, Tag: "!!int", Value: "1"})
		}
	}
}

// TestCompatV3NodeEdit reads each corpus input into a Node with both libraries,
// edits it, and compares what Marshal writes. The kit's edit inputs are the main
// users of comments, so they are counted separately.
func TestCompatV3NodeEdit(t *testing.T) {
	for _, only := range []string{"edit-inputs", "all-inputs"} {
		tally := newTally("node-edit/" + only)
		for _, it := range compatCorpus(t) {
			isEdit := strings.Contains(it.name, "own/cases/edit/") || strings.Contains(it.name, "own/cases/roundtrip/")
			if only == "edit-inputs" && !isEdit {
				continue
			}
			var yn yv3.Node
			var cn compat.Node
			if yv3.Unmarshal(it.data, &yn) != nil || compat.Unmarshal(it.data, &cn) != nil {
				continue
			}
			mutateV3(&yn)
			mutateCompat(&cn)
			yb, yerr := yv3.Marshal(&yn)
			cb, cerr := compat.Marshal(&cn)
			switch {
			case yerr != nil && cerr != nil:
				tally.bothErr++
			case yerr == nil && cerr == nil && string(yb) == string(cb):
				tally.same++
			default:
				if r := writeDifference(&yn, yb, cb, yerr, cerr); r != "" {
					tally.note(r, it.name)
				} else if r := nodeDifference(it, nil, nil); r != "" {
					tally.note(r, it.name)
				} else if hasComments(&yn) {
					tally.note("GAP: the comment placement rules differ on this input", it.name)
					if os.Getenv("ONEYAML_SHOWGAPS") != "" {
						fmt.Printf("GAP %s\n%s\n--v3\n%s--me\n%s", it.name, it.data, yb, cb)
					}
				} else {
					tally.unexplained = append(tally.unexplained, fmt.Sprintf("%s: v3 err=%v me err=%v\n%s", it.name, yerr, cerr, firstDiff(yb, cb)))
				}
			}
		}
		tally.report(t)
	}
}

func hasComments(n *yv3.Node) bool {
	if n.HeadComment != "" || n.LineComment != "" || n.FootComment != "" {
		return true
	}
	for _, c := range n.Content {
		if hasComments(c) {
			return true
		}
	}
	return false
}

// ---- acceptance: swap the import path of a program written for yaml.v3 ----

// TestImportSwap takes a small program written against yaml.v3
// (importswap/app), changes only its import path to the shim's, and checks that
// the copy compiles and passes the same tests.
func TestImportSwap(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a package")
	}
	src, err := os.ReadFile("importswap/app/app.go")
	if err != nil {
		t.Fatal(err)
	}
	test, err := os.ReadFile("importswap/app/app_test.go")
	if err != nil {
		t.Fatal(err)
	}
	const from, to = `"go.yaml.in/yaml/v3"`, `"oneyaml.dev/go/compat/yamlv3"`
	if !strings.Contains(string(src), from) {
		t.Fatal("the program does not import yaml.v3")
	}
	dir := filepath.Join("importswap", "swapped")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	swapped := strings.Replace(string(src), from, to, 1)
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte(swapped), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app_test.go"), []byte(strings.Replace(string(test), from, to, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("go", "test", "-count=1", "./importswap/swapped").CombinedOutput()
	if err != nil {
		t.Fatalf("the swapped program fails:\n%s", out)
	}
	t.Logf("swapped program: %s", strings.TrimSpace(string(out)))
}
