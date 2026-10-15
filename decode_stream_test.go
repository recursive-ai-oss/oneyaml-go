package oneyaml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
)

// slowReader returns at most n bytes per call, so line breaks and markers are
// split across reads.
type slowReader struct {
	b []byte
	n int
}

func (r *slowReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	k := min(r.n, len(p), len(r.b))
	copy(p, r.b[:k])
	r.b = r.b[k:]
	return k, nil
}

func decodeAll(r io.Reader) ([]string, error) {
	dec := NewDecoder(r)
	var out []string
	for {
		var v any
		if err := dec.Decode(&v); err != nil {
			if err == io.EOF {
				err = nil
			}
			return out, err
		}
		out = append(out, fmt.Sprintf("%#v", v))
	}
}

// A lone CR is a line break, so a marker line may end with one and the next
// line starts after it. Parse, Unmarshal and the Decoder must agree on how many
// documents there are and what they hold.
func TestStreamsAgreeOnBareCR(t *testing.T) {
	inputs := []string{
		"0\n... \r0", "...\r0\n0", "\r%00\n---", "a\r...\rb", "a\r---\rb", "a\r---\r", "a\r...\r",
		"a\r\n...\r\nb", "a\n---\rb\r---\r\nc", "\r\r...\r\r---\rx", "%YAML 1.2\r---\rx\r...\r%YAML 1.2\r---\ry",
		"- a\r- b\r...\r- c", "k: |\r  ...\r  x\r...\ry",
	}
	for _, in := range inputs {
		d, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("%q: Parse: %v", in, err)
		}
		var want []string
		for i := 0; i < d.Len(); i++ {
			var v any
			if err := d.Doc(i).Decode(&v); err != nil {
				t.Fatalf("%q: doc %d: %v", in, i, err)
			}
			want = append(want, fmt.Sprintf("%#v", v))
		}
		for _, n := range []int{1, 2, 3, 1 << 20} {
			got, err := decodeAll(&slowReader{[]byte(in), n})
			if err != nil || strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("%q (read size %d): Decoder gave %v, %v; Document has %v", in, n, got, err, want)
			}
		}
		var first any
		if err := Unmarshal([]byte(in), &first); err != nil {
			t.Errorf("%q: Unmarshal: %v", in, err)
		} else if len(want) > 0 && fmt.Sprintf("%#v", first) != want[0] && first != nil {
			t.Errorf("%q: Unmarshal gave %#v, first document is %s", in, first, want[0])
		}
	}
}

// Looking for the end of a line must not start over after every read, or one
// very long line costs the square of its length.
func TestDecoderLongLineSearchIsLinear(t *testing.T) {
	work := func(n int) int {
		in := "k: " + strings.Repeat("x", n) + "\n"
		dec := NewDecoder(bytes.NewReader([]byte(in)))
		var v map[string]string
		if err := dec.Decode(&v); err != nil || len(v["k"]) != n {
			t.Fatalf("n=%d: %v", n, err)
		}
		return dec.examined
	}
	prev := 0
	for n := 1 << 18; n <= 1<<22; n *= 2 {
		w := work(n)
		if w > 4*n {
			t.Errorf("n=%d: searched %d bytes", n, w)
		}
		if prev > 0 && w > 2*prev+2*readChunk {
			t.Errorf("n=%d: work %d is more than double the %d of half the size", n, w, prev)
		}
		prev = w
	}
}

type keeper struct{ n Node }

func (k *keeper) UnmarshalYAML(n Node) error { k.n = n; return nil }

// A Node kept from an Unmarshaler must go on reading its own text after the
// Decoder has read the next document into the space the first one used.
func TestDecoderKeptNodeSurvivesNextDecode(t *testing.T) {
	for _, parts := range [][]string{
		{"\"aaaaaaa\"\n...\n", "\"bbbbbbb\"\n"},
		{"\"aaaaaaa\"\n...\n\"bbbbbbb\"\n"},
		{"\"aaaaaaa\"\n...\n", "\"bbbbbbb\"\n...\n", "\"ccccccc\"\n"},
	} {
		dec := NewDecoder(&chunkReader{append([]string(nil), parts...)})
		var k keeper
		if err := dec.Decode(&k); err != nil {
			t.Fatal(err)
		}
		src := k.n.Source()
		for {
			var v any
			if err := dec.Decode(&v); err != nil {
				break
			}
		}
		var x string
		if err := k.n.Decode(&x); err != nil || x != "aaaaaaa" || k.n.Value() != "aaaaaaa" || k.n.Source() != src {
			t.Errorf("%q: kept node reads %q (%v), value %q, source %q (was %q)", parts, x, err, k.n.Value(), k.n.Source(), src)
		}
	}
}

type chunkReader struct{ parts []string }

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.parts) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.parts[0])
	c.parts[0] = c.parts[0][n:]
	if c.parts[0] == "" {
		c.parts = c.parts[1:]
	}
	return n, nil
}

// The document limit stops the build at the first document over it: a stream
// of millions of empty documents must not be built just to be refused.
func TestParseStopsAtDocumentLimit(t *testing.T) {
	in := []byte(strings.Repeat("---\n", 2<<20))
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	_, err := Parse(in, Untrusted())
	runtime.ReadMemStats(&m1)
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != "documents" {
		t.Fatalf("want a documents limit error, got %v", err)
	}
	if grown := m1.TotalAlloc - m0.TotalAlloc; grown > 3*uint64(len(in)) {
		t.Errorf("allocated %d bytes for a %d byte stream", grown, len(in))
	}
	// Exactly at the limit is fine.
	if d, err := Parse([]byte(strings.Repeat("---\n", 10000)), Untrusted()); err != nil || d.Len() != 10000 {
		t.Errorf("10000 documents: %v", err)
	}
}
