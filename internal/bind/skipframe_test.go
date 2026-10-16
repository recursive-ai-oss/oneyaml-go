package bind

import (
	"testing"
	"unsafe"
)

// A frame of the skip stack is paid for at every level of an ignored value, so
// it must stay small: the keys of a mapping live on a shared stack, and only a
// mapping with many keys has a set of its own.
func TestSkipFrameStaysSmall(t *testing.T) {
	if n := unsafe.Sizeof(skipFrame{}); n > 48 {
		t.Errorf("skipFrame is %d bytes, want at most 48", n)
	}
}
