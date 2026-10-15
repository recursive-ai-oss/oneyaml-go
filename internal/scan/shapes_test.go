package scan

import (
	"strings"
	"testing"
)

// names returns n distinct short keys, each followed by a comma and a line break.
func names(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteByte('x')
		for k, v := 0, i; k < 3; k, v = k+1, v/len(letters) {
			sb.WriteByte(letters[v%len(letters)])
		}
		sb.WriteString(",\n")
	}
	return sb.String()
}

// tokenWork scans an unclosed stack of depth flow mappings followed by many
// one-key lines and returns the number of tokens and key slots examined.
func tokenWork(t *testing.T, depth, lines int) (tokens, stale int) {
	t.Helper()
	s := New([]byte(strings.Repeat("{", depth)+names(lines)), -1)
	for {
		tok, err := s.Peek()
		if err != nil {
			return tokens, s.staleWork
		}
		tokens++
		if tok.Kind == StreamEnd {
			return tokens, s.staleWork
		}
		s.Skip()
	}
}

// Work must grow with the number of lines, not with the number of lines times the
// number of open mappings, and must not depend on the depth either.
func TestFlowMappingsThenLinesLinear(t *testing.T) {
	for _, depth := range []int{100, 3000} {
		tok1, w1 := tokenWork(t, depth, 4000)
		tok2, w2 := tokenWork(t, depth, 8000)
		if tok2 > 2*tok1+2*depth || w2 > 2*w1+2*depth {
			t.Errorf("depth %d: doubling the lines took tokens %d -> %d, key slots %d -> %d", depth, tok1, tok2, w1, w2)
		}
		// Each line may examine only the slots saved on it, plus the open mappings once.
		if w2 > 4*8000+2*depth {
			t.Errorf("depth %d: %d key slots examined for 8000 lines", depth, w2)
		}
	}
}
