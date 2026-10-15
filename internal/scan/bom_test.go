package scan

import "testing"

// A byte order mark between documents must not hide the "---" that follows it.
func TestBOMBeforeDocumentStart(t *testing.T) {
	src := []byte("a: 1\n...\n\xef\xbb\xbf---\nb: 2\n")
	s := New(src, 0)
	starts := 0
	for {
		tok, err := s.Peek()
		if err != nil {
			t.Fatal(err)
		}
		if tok.Kind == DocumentStart {
			starts++
		}
		if tok.Kind == StreamEnd {
			break
		}
		s.Skip()
	}
	if starts != 1 {
		t.Fatalf("got %d document starts, want 1", starts)
	}
}
