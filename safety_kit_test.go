package oneyaml_test

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"oneyaml.dev/go"
)

// TestKitBoundedBothPaths runs every bounded safety case through the typed
// reader (Unmarshal into any) and through Parse, the Document path, and holds
// both to the case's time and memory bounds. A refusal must be in the allowed
// categories. Slow cases (the large generated inputs) run only with KIT_SLOW=1.
// KIT_CASE narrows the run to case ids containing the given text, which lets
// a caller measure one case's peak memory in its own process.
func TestKitBoundedBothPaths(t *testing.T) {
	cases := loadKitCases(t, "safety")
	only := os.Getenv("KIT_CASE")
	ran := 0
	for _, c := range cases {
		if c.Expect.Kind != "bounded" || (only != "" && !strings.Contains(c.ID, only)) {
			continue
		}
		if c.Skip() {
			continue
		}
		ran++
		t.Run(c.ID, func(t *testing.T) {
			in, err := c.Bytes(c.dir)
			if err != nil {
				t.Fatal(err)
			}
			paths := []struct {
				name string
				run  func() error
			}{
				{"Unmarshal", func() error { var v any; return oneyaml.Unmarshal(in, &v) }},
				{"Parse", func() error { _, err := oneyaml.Parse(in); return err }},
			}
			switch c.Operation.Type {
			case "patch":
				// A patch case is about the patch: parse the input, apply the
				// patch, and judge the whole. The format picks the entry point, since
				// a merge patch is an object, not an array of operations.
				var apply func(*oneyaml.Document, []byte) error
				switch c.Operation.Format {
				case "", "json-patch":
					apply = (*oneyaml.Document).ApplyPatch
				case "merge-patch":
					apply = (*oneyaml.Document).ApplyMergePatch
				default:
					t.Fatalf("unknown patch format %q", c.Operation.Format)
				}
				name := "ApplyPatch"
				if c.Operation.Format == "merge-patch" {
					name = "ApplyMergePatch"
				}
				paths = paths[:0]
				paths = append(paths, struct {
					name string
					run  func() error
				}{name, func() error {
					d, err := oneyaml.Parse(in)
					if err != nil {
						return err
					}
					return apply(d, c.Operation.Patch)
				}})
			case "value", "events":
				// Read-only cases: the two reader paths above.
			default:
				t.Fatalf("unknown operation type %q", c.Operation.Type)
			}
			for _, p := range paths {
				var ms0, ms1 runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&ms0)
				start := time.Now()
				err := p.run()
				elapsed := time.Since(start)
				runtime.ReadMemStats(&ms1)
				// Bytes allocated is an upper bound on the growth; the case bound is
				// on peak resident memory, which is measured by the caller.
				allocMB := float64(ms1.TotalAlloc-ms0.TotalAlloc) / (1 << 20)
				t.Logf("%s: %v, %.0f MB allocated, err=%v", p.name, elapsed.Round(time.Millisecond), allocMB, err != nil)
				// The race detector slows everything several times over, so the time
				// bound is stretched under it.
				bound := c.Expect.TimeSeconds
				if raceEnabled {
					bound *= 8
				}
				if elapsed.Seconds() > bound {
					t.Errorf("%s took %v, bound %vs", p.name, elapsed, bound)
				}
				if allocMB > c.Expect.MemoryMB {
					t.Errorf("%s allocated %.0f MB, bound %v MB", p.name, allocMB, c.Expect.MemoryMB)
				}
				// An events case allows any outcome inside the bounds, since a generic
				// value is not an event stream. The lossless Document keeps aliases and
				// duplicate keys as written, so for it too only the cost and the kind of
				// refusal are held; the typed reader must also give an allowed outcome.
				if c.Operation.Type == "events" {
					continue
				}
				if err != nil && (c.Expect.Error == nil || !categoryAllowed(c.Expect.Error, kitCategory(err))) {
					t.Errorf("%s: error %s not allowed (%v): %.200v", p.name, kitCategory(err), c.Expect.Error, err)
				}
				if err == nil && c.Expect.Accept == nil && p.name != "Parse" {
					t.Errorf("%s answered, but only an error is allowed", p.name)
				}
			}
		})
	}
	if ran == 0 && only == "" {
		t.Fatal("no bounded cases found")
	}
}
