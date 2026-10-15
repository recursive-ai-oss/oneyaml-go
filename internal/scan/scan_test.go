package scan_test

import (
	"testing"

	"oneyaml.dev/go/internal/scan"
)

func kinds(t *testing.T, src string) []scan.Kind {
	t.Helper()
	s := scan.New([]byte(src), 0)
	var out []scan.Kind
	for {
		tok, err := s.Peek()
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		out = append(out, tok.Kind)
		s.Skip()
		if tok.Kind == scan.StreamEnd {
			return out
		}
	}
}

func TestTokens(t *testing.T) {
	tests := []struct {
		in   string
		want []scan.Kind
	}{
		{"a: b\n", []scan.Kind{scan.StreamStart, scan.BlockMappingStart, scan.Key, scan.Scalar, scan.Value, scan.Scalar, scan.BlockEnd, scan.StreamEnd}},
		{"- a\n- b\n", []scan.Kind{scan.StreamStart, scan.BlockSequenceStart, scan.BlockEntry, scan.Scalar, scan.BlockEntry, scan.Scalar, scan.BlockEnd, scan.StreamEnd}},
		{"[a, b]", []scan.Kind{scan.StreamStart, scan.FlowSequenceStart, scan.Scalar, scan.FlowEntry, scan.Scalar, scan.FlowSequenceEnd, scan.StreamEnd}},
		{"--- &x !t *y\n...\n", []scan.Kind{scan.StreamStart, scan.DocumentStart, scan.Anchor, scan.Tag, scan.Alias, scan.DocumentEnd, scan.StreamEnd}},
	}
	for _, tc := range tests {
		got := kinds(t, tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("%q: got %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q: got %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

func TestBlockScalarToken(t *testing.T) {
	src := "k: |2\n    a\n   b\nnext: 1\n"
	s := scan.New([]byte(src), 0)
	for {
		tok, err := s.Peek()
		if err != nil {
			t.Fatal(err)
		}
		if tok.Kind == scan.Scalar && tok.Style == scan.Literal {
			if got := src[tok.Start:tok.End]; got != "|2\n    a\n   b\n" {
				t.Errorf("span %q", got)
			}
			if tok.Indent != 2 {
				t.Errorf("indent %d", tok.Indent)
			}
			return
		}
		if tok.Kind == scan.StreamEnd {
			t.Fatal("no block scalar")
		}
		s.Skip()
	}
}
