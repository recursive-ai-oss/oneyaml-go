package kitcase

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceRecipeBuildsValue(t *testing.T) {
	var s Source
	doc := `{"recipe":[{"text":"[{"},{"numbers":{"count":2,"prefix":"\"x","suffix":"\": 1, "}},{"text":"\"x2\": 1}]"}]}`
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"x0": 1, "x1": 1, "x2": 1}]`; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSourceFileAndInline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "want.json"), []byte(`[1]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := (Source{File: "want.json"}).Read(dir, true); err != nil || string(got) != "[1]" {
		t.Fatalf("file: %q, %v", got, err)
	}
	if got, err := (Source{Value: json.RawMessage(`[2]`)}).Read(dir, true); err != nil || string(got) != "[2]" {
		t.Fatalf("inline: %q, %v", got, err)
	}
}

func TestSourceRefusals(t *testing.T) {
	dir := t.TempDir()
	good := json.RawMessage(`[{"text":"[]"}]`)
	cases := map[string]struct {
		s      Source
		inline bool
	}{
		"nothing at all":           {Source{}, true},
		"recipe and literal":       {Source{Value: json.RawMessage(`[]`), Recipe: good}, true},
		"recipe and file":          {Source{File: "x", Recipe: good}, true},
		"inline where not allowed": {Source{Value: json.RawMessage(`[]`)}, false},
		"missing file":             {Source{File: "absent.json"}, true},
		"recipe not a list":        {Source{Recipe: json.RawMessage(`{"text":"x"}`)}, true},
		"part with two kinds":      {Source{Recipe: json.RawMessage(`[{"text":"x","numbers":{"count":1}}]`)}, true},
		"part with no kind":        {Source{Recipe: json.RawMessage(`[{}]`)}, true},
		"negative repeat":          {Source{Recipe: json.RawMessage(`[{"text":"x","repeat":-1}]`)}, true},
		"too large":                {Source{Recipe: json.RawMessage(`[{"text":"xxxxxxxx","repeat":100000000}]`)}, true},
	}
	for name, c := range cases {
		if got, err := c.s.Read(dir, c.inline); err == nil {
			t.Errorf("%s: accepted, built %q", name, got)
		}
	}
}
