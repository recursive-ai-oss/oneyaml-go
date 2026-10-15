package oneyaml

import (
	"bytes"
	"testing"
	"unicode/utf16"
)

// UTF-16 text without a byte order mark is recognised by an ASCII first character.
// An edit that puts other text first must write the mark, or the output could not
// be read back as UTF-16.
func TestEditedUTF16WithoutMarkStaysReadable(t *testing.T) {
	var in []byte
	for _, u := range utf16.Encode([]rune("a: 1\nb: 2")) {
		in = append(in, byte(u>>8), byte(u))
	}
	d, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d.Bytes(), in) {
		t.Fatal("unedited bytes changed")
	}
	if err := d.Set("", "日本語"); err != nil {
		t.Fatal(err)
	}
	var s string
	if err := Unmarshal(d.Bytes(), &s); err != nil || s != "日本語" {
		t.Fatalf("output reads as %q, %v (% x)", s, err, d.Bytes())
	}
	// An edit that keeps an ASCII start needs no mark.
	d, _ = Parse(in)
	if err := d.Set("/a", 2); err != nil {
		t.Fatal(err)
	}
	if b := d.Bytes(); b[0] != 0 || b[1] != 'a' {
		t.Fatalf("a mark was added: % x", b)
	}
}
