package oneyaml_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A kit case's memory_mb is a bound on the peak resident memory of the process
// that runs the operation. Bytes allocated (runtime.MemStats.TotalAlloc) count
// churn, not peak: a patch that frees what it copies can allocate several times
// the bound and stay far inside it. So each bounded case is run in a child
// process, a re-run of this test binary, and the child's peak RSS is compared
// with the bound, as the kit does.

const kitChildEnv = "ONEYAML_KIT_CHILD"

// kitPeakMark precedes the child's own peak, in KiB, in its output.
const kitPeakMark = "KIT_PEAK_KB="

// kitReadAllPath names the multi-document typed read used by the schema test.
const kitReadAllPath = "ReadAll"

// kitChildResult is what the parent learns about one child run.
type kitChildResult struct {
	PeakMB float64       // peak resident set size of the child
	OK     bool          // false where the OS reports no peak RSS
	Wall   time.Duration // wall time of the whole child, start-up included
}

// kitRunChild runs one path of one case in a child process and measures it.
func kitRunChild(t *testing.T, id, path string) kitChildResult {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestKitChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), kitChildEnv+"="+id+"|"+path)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	wall := time.Since(start)
	if err != nil {
		t.Fatalf("child for %s %s: %v\n%.2000s", id, path, err, out)
	}
	// Prefer the child's own report. On Linux the rusage peak of a child also
	// holds the parent's size at the moment of exec, which would count the
	// parent's memory against the case.
	if _, rest, found := strings.Cut(string(out), kitPeakMark); found {
		if kb, err := strconv.ParseFloat(strings.Fields(rest)[0], 64); err == nil {
			return kitChildResult{PeakMB: kb / 1024, OK: true, Wall: wall}
		}
	}
	mb, ok := peakRSSMB(cmd.ProcessState)
	return kitChildResult{PeakMB: mb, OK: ok, Wall: wall}
}

// kitCheckChild runs the case path in a child and checks the hang guard and
// the peak-memory bound. Where the OS gives no peak RSS it falls back to the
// bytes allocated in the parent's own run (allocMB), but not for slow cases,
// whose allocation is far above their peak and would fail falsely.
func kitCheckChild(t *testing.T, c kitCase, path string, allocMB float64) {
	t.Helper()
	r := kitRunChild(t, c.ID, path)
	if r.OK {
		t.Logf("%s: child %v, peak %.0f MB (bound %v MB), %.0f MB allocated in-process", path, r.Wall.Round(time.Millisecond), r.PeakMB, c.Expect.MemoryMB, allocMB)
		if r.PeakMB > c.Expect.MemoryMB {
			t.Errorf("%s: peak memory %.0f MB, bound %v MB", path, r.PeakMB, c.Expect.MemoryMB)
		}
	} else if c.Slow() {
		t.Logf("%s: no peak RSS on this OS; memory bound not checked for a slow case", path)
	} else if allocMB > c.Expect.MemoryMB {
		t.Errorf("%s: allocated %.0f MB, bound %v MB (no peak RSS on this OS)", path, allocMB, c.Expect.MemoryMB)
	}
	// The limit is a hang guard, not a speed check.
	if r.Wall.Seconds() > c.Expect.TimeSeconds {
		t.Errorf("%s: child took %v, hang guard %vs", path, r.Wall, c.Expect.TimeSeconds)
	}
}

// TestKitChild is the child side: it runs only the operation named by the
// environment and exits. It is skipped in a normal run.
func TestKitChild(t *testing.T) {
	spec := os.Getenv(kitChildEnv)
	if spec == "" {
		t.Skip("only run as a child of the bounded kit tests")
	}
	id, path, _ := strings.Cut(spec, "|")
	// Read only this case: loading every case would count in the peak.
	root := os.Getenv("ONEYAML_KIT")
	dir := filepath.Join(root, "suites", "own", "cases", filepath.FromSlash(id))
	raw, err := os.ReadFile(filepath.Join(dir, "case.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c kitCase
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	c.dir = dir
	in, err := c.Bytes(c.dir)
	if err != nil {
		t.Fatal(err)
	}
	if path == kitReadAllPath {
		_, _ = readAll(in)
		reportPeak()
		return
	}
	for _, p := range kitPaths(t, c, in) {
		if p.name == path {
			_ = p.run()
			reportPeak()
			return
		}
	}
	t.Fatalf("case %s has no path %s", id, path)
}

// reportPeak prints this process's own peak resident size, where the OS
// offers it (Linux: VmHWM), for the parent to read.
func reportPeak() {
	if kb, ok := ownPeakKB(); ok {
		fmt.Printf("%s%d\n", kitPeakMark, kb)
	}
}
