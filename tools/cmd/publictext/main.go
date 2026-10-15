// Command publictext fails when files or commit messages carry internal
// process text.
//
//	publictext -tree                 every tracked and untracked file
//	publictext -changed main         files changed against a base, plus untracked
//	publictext -commits main..HEAD   commit messages in a range ("HEAD" = all)
//	publictext path...               the named files
//
// Flags combine. The exit status is 1 when anything is found.
//
// The patterns that mark text as internal are read from the file named by the
// environment variable PUBLICTEXT_PATTERNS (format: see package publictext).
// When it is unset the check runs only its generic rules and says so, except
// under CI (CI=true), where a missing file is an error so that a misconfigured
// pipeline cannot pass without the real check. A pipeline that knowingly has no
// patterns (a pull request from a fork gets no secrets) sets
// PUBLICTEXT_ALLOW_MISSING=true to run the generic checks only.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"oneyaml.dev/go/tools/publictext"
)

const patternsEnv = "PUBLICTEXT_PATTERNS"

func main() { os.Exit(run()) }

func run() int {
	tree := flag.Bool("tree", false, "check every tracked and untracked file")
	changed := flag.String("changed", "", "check files changed against this base, plus untracked files")
	commits := flag.String("commits", "", "check commit messages in this revision range")
	flag.Parse()

	root, err := publictext.Toplevel(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "publictext:", err)
		return 2
	}
	var checker *publictext.Checker
	if file := os.Getenv(patternsEnv); file != "" {
		checker, err = publictext.LoadChecker(file)
		if err != nil {
			fmt.Fprintln(os.Stderr, "publictext:", patternsEnv+":", err)
			return 2
		}
		if checker.Empty() && os.Getenv("CI") == "true" {
			fmt.Fprintln(os.Stderr, "publictext:", file, "holds no patterns")
			return 2
		}
	} else if os.Getenv("CI") == "true" && os.Getenv("PUBLICTEXT_ALLOW_MISSING") != "true" {
		fmt.Fprintln(os.Stderr, "publictext:", patternsEnv, "is not set; the check cannot run in CI without its patterns")
		return 2
	} else {
		fmt.Fprintln(os.Stderr, "publictext: skipping the pattern check:", patternsEnv, "is not set (generic checks only)")
	}
	var names []string
	if *tree {
		n, err := publictext.TreeFiles(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "publictext:", err)
			return 2
		}
		names = append(names, n...)
	}
	if *changed != "" {
		n, err := publictext.ChangedFiles(root, *changed)
		if err != nil {
			fmt.Fprintln(os.Stderr, "publictext:", err)
			return 2
		}
		names = append(names, n...)
	}
	for _, a := range flag.Args() {
		abs, err := filepath.Abs(a)
		if err != nil {
			fmt.Fprintln(os.Stderr, "publictext:", err)
			return 2
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			rel = a
		}
		names = append(names, filepath.ToSlash(rel))
	}
	if !*tree && *changed == "" && *commits == "" && flag.NArg() == 0 {
		flag.Usage()
		return 2
	}

	var findings []publictext.Finding
	seen := map[string]bool{}
	files := 0
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(n)))
		if err != nil {
			continue // a directory, a submodule or a vanished file
		}
		files++
		findings = append(findings, checker.CheckFile(n, data)...)
	}
	msgs := 0
	if *commits != "" {
		f, err := checker.CheckCommits(root, *commits)
		if err != nil {
			fmt.Fprintln(os.Stderr, "publictext:", err)
			return 2
		}
		findings = append(findings, f...)
		msgs = 1
	}
	for _, f := range findings {
		fmt.Println(f)
	}
	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "publictext: %d finding(s)\n", len(findings))
		return 1
	}
	fmt.Fprintf(os.Stderr, "publictext: %d files checked, commit messages checked: %t, clean\n", files, msgs == 1)
	return 0
}
