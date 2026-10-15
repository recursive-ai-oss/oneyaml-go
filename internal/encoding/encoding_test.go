package encoding

import (
	"bytes"
	"errors"
	"testing"

	"oneyaml.dev/go/internal/scan"
)

func TestDetect(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		enc  Encoding
		bom  int
		name string
	}{
		{nil, UTF8, 0, "empty"},
		{[]byte("a: 1"), UTF8, 0, "ascii"},
		{[]byte("\xEF\xBB\xBFa"), UTF8, 3, "utf8 bom"},
		{[]byte("\xFE\xFF\x00a"), UTF16BE, 2, "utf16be bom"},
		{[]byte("\xFF\xFEa\x00"), UTF16LE, 2, "utf16le bom"},
		{[]byte("\x00\x00\xFE\xFF\x00\x00\x00a"), UTF32BE, 4, "utf32be bom"},
		{[]byte("\xFF\xFE\x00\x00a\x00\x00\x00"), UTF32LE, 4, "utf32le bom"},
		{[]byte("\x00a\x00:"), UTF16BE, 0, "utf16be bare"},
		{[]byte("a\x00:\x00"), UTF16LE, 0, "utf16le bare"},
		{[]byte("\x00\x00\x00a"), UTF32BE, 0, "utf32be bare"},
		{[]byte("a\x00\x00\x00"), UTF32LE, 0, "utf32le bare"},
		{[]byte("é: 1"), UTF8, 0, "non-ascii utf8"},
	} {
		enc, bom := Detect(tc.in)
		if enc != tc.enc || bom != tc.bom {
			t.Errorf("%s: got %v,%d want %v,%d", tc.name, enc, bom, tc.enc, tc.bom)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	text := []byte("k: \"é€𝄞\"\n# done\n")
	for _, enc := range []Encoding{UTF8, UTF16LE, UTF16BE, UTF32LE, UTF32BE} {
		for _, bom := range []bool{false, true} {
			wire := FromUTF8(nil, text, enc, bom)
			got, gotEnc, err := Convert(wire)
			if err != nil {
				t.Fatalf("%v bom=%v: %v", enc, bom, err)
			}
			if gotEnc != enc {
				t.Errorf("%v bom=%v: detected %v", enc, bom, gotEnc)
			}
			if enc == UTF8 && bom {
				got = got[3:]
			}
			if !bytes.Equal(got, text) {
				t.Errorf("%v bom=%v: got %q", enc, bom, got)
			}
		}
	}
}

func TestMalformed(t *testing.T) {
	for _, tc := range []struct {
		name         string
		in           []byte
		line, column int
	}{
		{"odd trailing byte", []byte("a\x00b"), 1, 2},
		{"unpaired high", []byte("a\x00\x3D\xD8b\x00"), 1, 2},
		{"unpaired low", []byte("a\x00\x00\xDCb\x00"), 1, 2},
		{"reversed pair", []byte("a\x00\x00\xDC\x3D\xD8"), 1, 2},
		{"high at end", []byte("a\x00\x3D\xD8"), 1, 2},
		{"utf32 too high", []byte("a\x00\x00\x00\x00\x00\x11\x00"), 1, 2},
		{"utf32 surrogate", []byte("a\x00\x00\x00\x00\xD8\x00\x00"), 1, 2},
		{"utf32 truncated", []byte("a\x00\x00\x00\x00\x00"), 1, 2},
		{"line counted", []byte("a\x00\n\x00b\x00\x00\xDC"), 2, 2},
	} {
		enc, _ := Detect(tc.in)
		if tc.name[:5] == "utf32" {
			enc = UTF32LE
		}
		_, err := ToUTF8(tc.in, enc)
		var se *scan.Error
		if !errors.As(err, &se) || se.Category != scan.Encoding {
			t.Errorf("%s: err = %v", tc.name, err)
			continue
		}
		if se.Line != tc.line || se.Column != tc.column {
			t.Errorf("%s: position %d:%d, want %d:%d", tc.name, se.Line, se.Column, tc.line, tc.column)
		}
	}
}

func BenchmarkDetectUTF8(b *testing.B) {
	src := []byte("name: api\nreplicas: 2\n")
	b.ReportAllocs()
	for b.Loop() {
		Convert(src)
	}
}
