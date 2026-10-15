package oneyaml

import (
	"errors"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/edit"
)

// Patch text that is not UTF-8 is refused whole, with the offset of the bad
// byte, and the document stays as it was. Neither patch kind may replace the
// bytes with U+FFFD, which would rename members and let two names collide.
func TestPatchTextInvalidUTF8(t *testing.T) {
	const src = "a: 1\n"
	cases := []struct{ name, patch string }{
		{"key", "[{\"op\":\"test\",\"path\":\"\",\"value\":{\"\x7f\xff\":1,\"\ufffd\":2}}]"},
		{"value", "[{\"op\":\"add\",\"path\":\"/b\",\"value\":\"\xc3(\"}]"},
		{"path", "[{\"op\":\"remove\",\"path\":\"/\xfa\"}]"},
		{"outside a string", "[\xff]"},
		{"truncated sequence", "[\"\xe2\x82\"]"},
	}
	for _, c := range cases {
		for kind, apply := range map[string]func(*Document, []byte) error{
			"6902": (*Document).ApplyPatch, "7396": (*Document).ApplyMergePatch,
		} {
			d, err := Parse([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			err = apply(d, []byte(c.patch))
			if !errors.Is(err, ErrInvalidPatch) || !strings.Contains(err.Error(), "invalid UTF-8") || !strings.Contains(err.Error(), "offset") {
				t.Errorf("%s/%s: got %v", c.name, kind, err)
			}
			if string(d.Bytes()) != src {
				t.Errorf("%s/%s: document changed: %q", c.name, kind, d.Bytes())
			}
		}
	}
}

// The property generator writes values as JSON, which cannot carry a string that
// is not UTF-8; it must not offer one.
func TestJSONSafeRejectsInvalidUTF8(t *testing.T) {
	if jsonSafe(edit.Str("\x7f\xfa")) {
		t.Error("string with an invalid byte reported safe")
	}
	if jsonSafe(edit.Value{Kind: edit.Map, Items: []edit.Value{edit.Str("\xff"), edit.Str("v")}}) {
		t.Error("member name with an invalid byte reported safe")
	}
	if !jsonSafe(edit.Str("\x7f\ufffd")) {
		t.Error("valid U+FFFD reported unsafe")
	}
}
