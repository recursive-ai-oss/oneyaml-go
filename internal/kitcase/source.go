package kitcase

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Source locates an expected result. It is embedded in a case's expectation
// (and in its accept block) so the JSON fields value, file and recipe land here.
// Exactly one must be given: a missing result must fail loudly, never read as
// an empty or null one.
type Source struct {
	// Value holds the result inline (value expectations only).
	Value json.RawMessage `json:"value"`
	// File names a stored result in the case directory.
	File string `json:"file"`
	// Recipe describes a result too large to store; see Build.
	Recipe json.RawMessage `json:"recipe"`
}

// Read returns the expected result's bytes. With inline true an inline Value is
// allowed (its JSON text is returned); otherwise it is refused. A recipe builds
// the text of the result in the notation of a stored file.
func (s Source) Read(dir string, inline bool) ([]byte, error) {
	n := 0
	if present(s.Value) {
		n++
		if !inline {
			return nil, fmt.Errorf("expectation: an inline value is not allowed here")
		}
	}
	if s.File != "" {
		n++
	}
	if present(s.Recipe) {
		n++
	}
	if n != 1 {
		return nil, fmt.Errorf("expectation: exactly one of value, file and recipe is needed, got %d", n)
	}
	switch {
	case present(s.Value):
		return []byte(s.Value), nil
	case s.File != "":
		return os.ReadFile(filepath.Join(dir, s.File))
	}
	return Build(s.Recipe)
}
