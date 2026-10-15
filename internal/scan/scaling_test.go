package scan

import (
	"strings"
	"testing"
)

// work scans a nested flow mapping with many short lines and returns how many
// simple-key slots were examined.
func staleWork(t *testing.T, lines int) int {
	t.Helper()
	src := []byte(strings.Repeat("{", 2000) + strings.Repeat("a,\n", lines) + strings.Repeat("}", 2000))
	s := New(src, -1)
	for {
		tok, err := s.Peek()
		if err != nil {
			t.Fatal(err)
		}
		k := tok.Kind
		s.Skip()
		if k == StreamEnd {
			return s.staleWork
		}
	}
}

// Dropping stale simple keys at a line break must cost what was saved on the
// finished line, not the depth of the nesting.
func TestStaleKeysLinear(t *testing.T) {
	a, b := staleWork(t, 500), staleWork(t, 2000)
	if b > 5*a+1000 {
		t.Errorf("work grows faster than the input: %d lines -> %d, %d lines -> %d", 500, a, 2000, b)
	}
}
