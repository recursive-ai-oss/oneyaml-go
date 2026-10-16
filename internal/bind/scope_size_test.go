package bind

import (
	"testing"
	"unsafe"
)

// A Scope is made for every call of a hook. It points at the configuration of
// the read instead of holding a copy, which would add its size to every call.
func TestScopeHoldsNoCopyOfTheConfiguration(t *testing.T) {
	if n := unsafe.Sizeof(Scope{}); n > 16 {
		t.Errorf("a Scope is %d bytes", n)
	}
}
