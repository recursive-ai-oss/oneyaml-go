package bind

import (
	"fmt"
	"testing"
)

// A keySet finds a repeat whether it still compares a few keys one by one or has
// moved to a hash set, and never takes distinct keys for repeats.
func TestKeySetFindsRepeats(t *testing.T) {
	var u keySet
	for round := 0; round < 2; round++ { // the second round reuses what the first grew
		u.reset()
		n := 3 * unknownLinear
		for i := 0; i < n; i++ {
			k := []byte(fmt.Sprintf("key%d", i))
			if u.add(k) {
				t.Fatalf("round %d: %s taken for a repeat", round, k)
			}
		}
		for i := 0; i < n; i++ {
			k := []byte(fmt.Sprintf("key%d", i))
			if !u.add(k) {
				t.Fatalf("round %d: repeat of %s missed", round, k)
			}
		}
		// A key that is a prefix of another is a different key.
		u.reset()
		for _, k := range []string{"ab", "a", "abc", ""} {
			if u.add([]byte(k)) {
				t.Fatalf("round %d: %q taken for a repeat", round, k)
			}
		}
	}
}
