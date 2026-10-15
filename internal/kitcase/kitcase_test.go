package kitcase

import (
	"encoding/json"
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
