package oneyaml

import "errors"

// ErrStaleNode means a Node was used after the Document it came from was edited.
// Fetch the node again after the edit.
var ErrStaleNode = errors.New("oneyaml: node is stale: the document was edited after it was read")

// ErrNoPointer means a Node has no RFC 6901 pointer: it is a mapping key, lies
// inside a complex key, or its pointer would not lead back to it.
var ErrNoPointer = errors.New("oneyaml: node has no pointer")

// staleAsEdit reports a stale Node as an *EditError so that callers can match it
// like the other edit failures; other errors pass through.
func staleAsEdit(op, ptr string, err error) error {
	if errors.Is(err, ErrStaleNode) {
		return &EditError{Op: op, Pointer: ptr, Kind: err}
	}
	return err
}
