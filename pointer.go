package oneyaml

import "oneyaml.dev/go/internal/pointer"

// Pointer builds an RFC 6901 pointer from keys and indices, writing "~" as "~0"
// and "/" as "~1" inside each one. Use it whenever a key comes from data, since
// keys such as app.kubernetes.io/name hold a slash:
//
//	Pointer("metadata", "labels", "app.kubernetes.io/name")
//	// /metadata/labels/app.kubernetes.io~1name
//
// Pointer() with no tokens is the empty pointer, which addresses the whole
// document. An index is written as its digits ("0"); "-" names the place after
// the last item of a sequence, for Set. The result is what Document.Get, Set,
// Remove, Rename and the patch operations take, and what Node.Pointer returns for
// the same place.
//
// To get the pointer of a node you have read, call Node.Pointer instead of
// building it again from its keys.
func Pointer(tokens ...string) string { return pointer.Join(tokens) }
