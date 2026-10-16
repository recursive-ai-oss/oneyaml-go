// Command pincheck fails when the kit revision named in the generated
// conformance regions of the documentation is not the kit revision the CI
// conformance job checks out.
//
//	pincheck [-root dir]
//
// The regions (between kit-report markers in README.md and docs/conformance.md)
// state "kit revision `<short hash>`"; the workflow pins the kit with a full
// commit hash in a "repository: .../oneyaml-kit" step. The short hash must be a
// prefix of the pin, otherwise the published figures were measured on a
// different kit than the one CI runs.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	beginRE    = regexp.MustCompile(`^<!-- kit-report:[a-z-]+ -->$`)
	endRE      = regexp.MustCompile(`^<!-- end kit-report:[a-z-]+ -->$`)
	revisionRE = regexp.MustCompile("kit revision `([0-9a-f]{7,40})`")
	pinRE      = regexp.MustCompile(`(?m)^\s*ref:\s*([0-9a-f]{40})\b`)
	kitRepoRE  = regexp.MustCompile(`repository:\s*\S*oneyaml-kit\s*$`)
)

// docRevisions returns the kit revisions named inside kit-report regions.
func docRevisions(text string) []string {
	var revs []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case beginRE.MatchString(line):
			in = true
		case endRE.MatchString(line):
			in = false
		case in:
			for _, m := range revisionRE.FindAllStringSubmatch(line, -1) {
				revs = append(revs, m[1])
			}
		}
	}
	return revs
}

// ciPin returns the commit hash of the step that checks out the kit.
func ciPin(workflow string) (string, bool) {
	lines := strings.Split(workflow, "\n")
	for i, line := range lines {
		if !kitRepoRE.MatchString(strings.TrimRight(line, "\r")) {
			continue
		}
		// The ref sits in the same "with:" block, a few lines away.
		for j := i + 1; j < len(lines) && j <= i+4; j++ {
			if m := pinRE.FindStringSubmatch(lines[j]); m != nil {
				return m[1], true
			}
		}
	}
	return "", false
}

func run(root string) []string {
	var problems []string
	wf, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		return []string{err.Error()}
	}
	pin, ok := ciPin(string(wf))
	if !ok {
		return []string{"ci.yml: no pinned kit checkout (repository oneyaml-kit with a 40-digit ref) found"}
	}
	for _, name := range []string{"README.md", "docs/conformance.md"} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		revs := docRevisions(string(b))
		if len(revs) == 0 {
			problems = append(problems, name+": no kit revision found inside a kit-report region")
		}
		for _, r := range revs {
			if !strings.HasPrefix(pin, r) {
				problems = append(problems, fmt.Sprintf("%s: figures name kit revision %s, but CI pins %s", name, r, pin))
			}
		}
	}
	return problems
}

func main() {
	root := ".."
	if len(os.Args) == 3 && os.Args[1] == "-root" {
		root = os.Args[2]
	} else if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: pincheck [-root dir]")
		os.Exit(2)
	}
	problems := run(root)
	for _, p := range problems {
		fmt.Println("pincheck:", p)
	}
	if len(problems) > 0 {
		fmt.Println("pincheck: regenerate the regions from a run on the pinned kit, or move the pin to the kit the figures came from")
		os.Exit(1)
	}
	fmt.Println("pincheck: ok")
}
