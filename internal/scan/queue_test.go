package scan

import (
	"strings"
	"testing"
)

// The token queue must stay small whatever the input: an implicit key can be
// at most 1024 characters, so nothing may be held back for longer than that.
func TestQueueBounded(t *testing.T) {
	for name, src := range map[string]string{
		"one-line flow mapping":   "{" + strings.Repeat("a: 1, ", 200000) + "}",
		"one-line flow sequence":  "[" + strings.Repeat("a, ", 200000) + "]",
		"deep block ends at once": strings.Repeat("- ", 300000) + "x\n",
		"nested pairs":            "[" + strings.Repeat("{a: [b]}, ", 100000) + "]",
	} {
		s := New([]byte(src), -1)
		maxQueue := 0
		for {
			tok, err := s.Peek()
			if err != nil {
				t.Fatal(name, err)
			}
			if n := len(s.tokens) - s.head; n > maxQueue {
				maxQueue = n
			}
			k := tok.Kind
			s.Skip()
			if k == StreamEnd {
				break
			}
		}
		// 1024 characters hold at most about 1024/3 tokens.
		if maxQueue > 1500 {
			t.Errorf("%s: queue reached %d tokens", name, maxQueue)
		}
	}
}
