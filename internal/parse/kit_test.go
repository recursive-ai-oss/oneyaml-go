package parse_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/encoding"
	"oneyaml.dev/go/internal/kitcase"
	"oneyaml.dev/go/internal/parse"
)

// kitCase is the part of a language-neutral test case that the parser can be
// held to. The cases are read from the directory named by ONEYAML_KIT; the
// test is skipped when it is unset.
type kitCase struct {
	ID        string `json:"id"`
	Input     string `json:"input"`
	Operation struct {
		Type string `json:"type"`
	} `json:"operation"`
	Expect struct {
		Kind     string `json:"kind"`
		File     string `json:"file"`
		Category string `json:"category"`
		Line     int    `json:"line"`
		Column   int    `json:"column"`
		Events   string `json:"events_file"`
	} `json:"expect"`
}

// TestKitParseAndDiagnostics checks every events case in the parse and
// diagnostics categories: accepted streams give exactly the expected events,
// rejected ones give the expected category, position and events before the error.
func TestKitParseAndDiagnostics(t *testing.T) {
	kit := os.Getenv("ONEYAML_KIT")
	if kit == "" {
		t.Skip("ONEYAML_KIT is not set")
	}
	var files []string
	for _, c := range []string{"parse", "diagnostics"} {
		m, _ := filepath.Glob(filepath.Join(kit, "suites", "own", "cases", c, "*", "case.json"))
		files = append(files, m...)
	}
	sort.Strings(files)
	checked, passed := 0, 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var kc kitCase
		if err := json.Unmarshal(raw, &kc); err != nil {
			t.Fatal(f, err)
		}
		if kc.Operation.Type != "events" || (kc.Expect.Kind != "events" && kc.Expect.Kind != "error") {
			continue
		}
		dir := filepath.Dir(f)
		var meta kitcase.Meta
		if err := json.Unmarshal(raw, &meta); err != nil {
			t.Fatal(f, err)
		}
		if meta.Skip() {
			continue
		}
		src, err := meta.Bytes(dir)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		if msg := checkKitCase(&kc, dir, src); msg != "" {
			t.Errorf("%s: %s", kc.ID, msg)
		} else {
			passed++
		}
	}
	t.Logf("kit parse and diagnostics events cases: %d of %d pass", passed, checked)
}

func checkKitCase(kc *kitCase, dir string, src []byte) string {
	// The public entry points transcode UTF-16 and UTF-32 before scanning.
	if utf8, _, err := encoding.Convert(src); err == nil {
		src = utf8
	}
	lines, err := eventLines(src)
	joined := strings.Join(lines, "\n")
	if out, problem := reconstruct(src); problem != "" || string(out) != string(src) {
		return "reconstruction: " + problem
	}
	if kc.Expect.Kind == "events" {
		want, _ := os.ReadFile(filepath.Join(dir, kc.Expect.File))
		if err != nil {
			return "rejected: " + err.Error()
		}
		if joined != strings.TrimRight(string(want), "\n") {
			return fmt.Sprintf("events differ:\nwant\n%s\ngot\n%s", want, joined)
		}
		return ""
	}
	var pe *parse.Error
	if !errors.As(err, &pe) {
		return "accepted"
	}
	if c := kc.Expect.Category; c != "" && c != "any" && c != pe.Category.String() {
		return fmt.Sprintf("category %s, want %s", pe.Category, c)
	}
	if kc.Expect.Line != 0 && (pe.Line != kc.Expect.Line || kc.Expect.Column != 0 && pe.Column != kc.Expect.Column) {
		return fmt.Sprintf("position %d:%d, want %d:%d (%s)", pe.Line, pe.Column, kc.Expect.Line, kc.Expect.Column, pe.Msg)
	}
	if kc.Expect.Events != "" {
		want, _ := os.ReadFile(filepath.Join(dir, kc.Expect.Events))
		if joined != strings.TrimRight(string(want), "\n") {
			return fmt.Sprintf("events before the error differ:\nwant\n%s\ngot\n%s", want, joined)
		}
	}
	return ""
}
