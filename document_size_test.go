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

// The first edit of a Document allocates one editState; past 416 bytes it moves
// to the next allocation class and every edited Document pays for it.
func TestEditStateStaysInItsSizeClass(t *testing.T) {
	if n := unsafe.Sizeof(editState{}); n > 416 {
		t.Fatalf("editState is %d bytes; keep it at 416 or less", n)
	}
}
