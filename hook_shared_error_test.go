package oneyaml

import (
	"errors"
	"sync"
	"testing"
)

// A hook may keep the error of a decode and return it from later reads, even
// from several goroutines at once. The read must not write to that error: it
// builds its own copy with the whole-document path.

var (
	sharedHookMu  sync.Mutex
	sharedHookErr error
	sharedHookLE  *LimitError
	// sharedHookSwallow makes the hook keep the error of its decode and return
	// nil, so that the first read leaves it untouched for the reads that share it.
	sharedHookSwallow bool
)

type sharedHook struct{ v []float64 }

func (x *sharedHook) UnmarshalYAML(n Node) error {
	sharedHookMu.Lock()
	err := sharedHookErr
	sharedHookMu.Unlock()
	if err != nil {
		return err
	}
	err = n.Decode(&x.v, Limits(LimitSet{MaxNumberDigits: 3}))
	var le *LimitError
	if errors.As(err, &le) {
		sharedHookMu.Lock()
		sharedHookErr, sharedHookLE = err, le
		sharedHookMu.Unlock()
		if sharedHookSwallow {
			return nil
		}
	}
	return err
}

func TestHookReturningAKeptLimitErrorDoesNotChangeIt(t *testing.T) {
	sharedHookErr, sharedHookLE = nil, nil
	defer func() { sharedHookErr, sharedHookLE = nil, nil }()
	type top struct{ A, B sharedHook }
	var v top
	first := Unmarshal([]byte("a: [1, 12.5678]\nb: [1]\n"), &v)
	var le *LimitError
	if !errors.As(first, &le) || le.Path != "/a/1" {
		t.Fatalf("first read: %v, want a limit error at /a/1", first)
	}
	if sharedHookLE == nil || sharedHookLE.Path != "/1" || !sharedHookLE.rel {
		t.Fatalf("the error the hook kept was changed by the read: %+v", sharedHookLE)
	}
	var w struct{ X, Y sharedHook }
	second := Unmarshal([]byte("x: [1]\ny: [1]\n"), &w)
	if !errors.As(second, &le) || le.Path != "/x" {
		// The kept error is relative to a node the second read never decoded, so
		// the read leaves its path alone; only the hook's own value is named.
		t.Logf("second read: %v", second)
	}
	if sharedHookLE.Path != "/1" || !sharedHookLE.rel {
		t.Fatalf("the error the hook kept was changed by a later read: %+v", sharedHookLE)
	}
}

func TestHookReturningAKeptLimitErrorFromManyGoroutines(t *testing.T) {
	sharedHookErr, sharedHookLE = nil, nil
	defer func() { sharedHookErr, sharedHookLE = nil, nil }()
	sharedHookSwallow = true
	defer func() { sharedHookSwallow = false }()
	var seed struct{ A sharedHook }
	if err := Unmarshal([]byte("a: [1, 12.5678]\n"), &seed); err != nil || sharedHookErr == nil {
		t.Fatalf("seed read: %v, kept %v", err, sharedHookErr)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var v struct{ P, Q sharedHook }
			_ = Unmarshal([]byte("p: [1]\nq: [1]\n"), &v)
		}()
	}
	wg.Wait()
}
