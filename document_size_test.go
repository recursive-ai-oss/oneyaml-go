package oneyaml

import (
	"testing"
	"unsafe"
)

// Every parsed Document carries one docStream, so its size is memory per Document:
// it must stay in the 448-byte allocation class.
func TestDocStreamStaysInItsSizeClass(t *testing.T) {
	if n := unsafe.Sizeof(docStream{}); n > 448 {
		t.Fatalf("docStream is %d bytes; keep it at 448 or less", n)
	}
}
