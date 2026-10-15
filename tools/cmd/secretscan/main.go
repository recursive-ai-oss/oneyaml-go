// Command secretscan refuses blobs that carry secrets, executables, core dumps
// or more than 1 MB.
//
//	secretscan -all            every object reachable from any ref
//	secretscan -range a..b     objects a push from a to b would send
//	secretscan -range HEAD     the whole history of HEAD
//
// Findings name the path and kind, never the value. Exit status is 1 when
// anything is found.
package main

import (
	"flag"
	"fmt"
	"os"

	"oneyaml.dev/go/tools/secretscan"
)

func main() { os.Exit(run()) }

func run() int {
	all := flag.Bool("all", false, "scan every object reachable from any ref")
	rng := flag.String("range", "", "revision range to scan, for example old..new")
	flag.Parse()
	var revs []string
	switch {
	case *all:
		revs = []string{"--all"}
	case *rng != "":
		revs = []string{*rng}
	default:
		flag.Usage()
		return 2
	}
	res, blobs, err := secretscan.ScanRange(".", revs...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "secretscan:", err)
		return 2
	}
	for _, f := range res {
		fmt.Println(f)
	}
	if len(res) > 0 {
		fmt.Fprintf(os.Stderr, "secretscan: %d problem(s) in %d blobs\n", len(res), blobs)
		return 1
	}
	fmt.Fprintf(os.Stderr, "secretscan: %d blobs scanned, nothing found\n", blobs)
	return 0
}
