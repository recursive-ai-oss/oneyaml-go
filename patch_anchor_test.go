package oneyaml

import (
	"errors"
	"testing"
)

// An anchor on a mapping key goes with the entry, so an alias to it blocks the
// removal like any other anchor in use.
func TestRemoveRefusesAnchoredKeyInUse(t *testing.T) {
	const src = "m:\n  &k key: 1\n  other: 2\nuse: *k\n"
	runs := map[string]func(*Document) error{
		"remove": func(d *Document) error { return d.Remove("/m/key") },
		"move": func(d *Document) error {
			return d.ApplyPatch([]byte(`[{"op":"move","from":"/m/key","path":"/z"}]`))
		},
	}
	for name, run := range runs {
		d := mustParse(t, src)
		if err := run(d); !errors.Is(err, ErrAnchorInUse) {
			t.Errorf("%s: got %v", name, err)
		}
		if string(d.Bytes()) != src {
			t.Errorf("%s: document changed", name)
		}
	}
}
