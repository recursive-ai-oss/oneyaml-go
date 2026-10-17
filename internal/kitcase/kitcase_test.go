package kitcase

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	got, err := Build(json.RawMessage(`[{"text":"[","repeat":3},{"numbers":{"count":3,"start":8,"prefix":"k","suffix":",","width":3}},{"text":"]"}]`))
	if err != nil || string(got) != "[[[k008,k009,k010,]" {
		t.Fatalf("%q, %v", got, err)
	}
}

func TestBuildRefusals(t *testing.T) {
	for _, r := range []string{
		`[{"text":"x","repeat":100000000}]`,
		`[{"text":"x","repeat":-1}]`,
		`[{"text":"x","numbers":{"count":1}}]`,
		`[{}]`,
		`{`,
	} {
		if _, err := Build(json.RawMessage(r)); err == nil {
			t.Errorf("%s: accepted", r)
		}
	}
}

func TestSkipSlow(t *testing.T) {
	m := Meta{Tags: []string{"slow"}}
	t.Setenv("KIT_SLOW", "")
	if !m.Skip() {
		t.Error("slow case not skipped")
	}
	t.Setenv("KIT_SLOW", "1")
	if m.Skip() {
		t.Error("slow case skipped with KIT_SLOW=1")
	}
}

func TestPatchBytes(t *testing.T) {
	recipe := json.RawMessage(`[{"ops":[{"op":"add","path":"/f","value":[]}]},{"ops":[{"op":"copy","from":"/v","path":"/f/-"},{"op":"remove","path":"/x"}],"repeat":2}]`)
	got, err := PatchBytes(nil, recipe, "json-patch")
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"op":"add","path":"/f","value":[]},` +
		`{"op":"copy","from":"/v","path":"/f/-"},{"op":"remove","path":"/x"},` +
		`{"op":"copy","from":"/v","path":"/f/-"},{"op":"remove","path":"/x"}]`
	if string(got) != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	// A stored patch is returned as it is.
	if got, err := PatchBytes(json.RawMessage(`[]`), nil, ""); err != nil || string(got) != "[]" {
		t.Fatalf("stored: %s, %v", got, err)
	}
}

func TestPatchBytesRefusals(t *testing.T) {
	ok := json.RawMessage(`[{"ops":[{"op":"remove","path":"/a"}]}]`)
	for name, c := range map[string]struct {
		patch, recipe json.RawMessage
		format        string
	}{
		"both":           {json.RawMessage(`[]`), ok, ""},
		"neither":        {nil, nil, ""},
		"null both":      {json.RawMessage(`null`), json.RawMessage(`null`), ""},
		"merge patch":    {nil, ok, "merge-patch"},
		"empty recipe":   {nil, json.RawMessage(`[]`), ""},
		"empty ops":      {nil, json.RawMessage(`[{"ops":[]}]`), ""},
		"no op name":     {nil, json.RawMessage(`[{"ops":[{"path":"/a"}]}]`), ""},
		"negative":       {nil, json.RawMessage(`[{"ops":[{"op":"remove"}],"repeat":-1}]`), ""},
		"too many ops":   {nil, json.RawMessage(`[{"ops":[{"op":"remove"}],"repeat":1000001}]`), ""},
		"too many parts": {nil, json.RawMessage("[" + strings.Repeat(`{"ops":[{"op":"remove"}]},`, 64) + `{"ops":[{"op":"remove"}]}]`), ""},
	} {
		if got, err := PatchBytes(c.patch, c.recipe, c.format); err == nil {
			t.Errorf("%s: accepted: %.60s", name, got)
		}
	}
}

func TestBuildProduct(t *testing.T) {
	got, err := Build(json.RawMessage(`[{"product":{"prefix":"<","suffix":">","choices":[["a","b"],["x","yy","z"]]}}]`))
	if err != nil || string(got) != "<ax><ayy><az><bx><byy><bz>" {
		t.Fatalf("%q, %v", got, err)
	}
	for _, r := range []string{`[{"product":{}}]`, `[{"product":{"choices":[[]]}}]`, `[{"product":{"choices":[["a"]]},"text":"x"}]`} {
		if _, err := Build(json.RawMessage(r)); err == nil {
			t.Errorf("%s: accepted", r)
		}
	}
}
