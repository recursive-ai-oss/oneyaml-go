package oneyaml

import "testing"

// In "?\n::" the first colon starts the plain scalar ":"; it is not the value
// indicator of the empty key before it. Removing the empty key's entry works
// and leaves the rest byte for byte.
func TestRemoveEmptyKeyBeforeColonScalar(t *testing.T) {
	for _, tc := range []struct{ in, ptr, want string }{
		{"?\n::", "/", "::"},
		{"? \n::", "/", "::"},
		{"a: 1\n?\n::\n", "/", "a: 1\n::\n"},
	} {
		d, err := Parse([]byte(tc.in))
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if err := d.Remove(tc.ptr); err != nil {
			t.Errorf("%q remove %q: %v", tc.in, tc.ptr, err)
			continue
		}
		if got := string(d.Bytes()); got != tc.want {
			t.Errorf("%q remove %q = %q, want %q", tc.in, tc.ptr, got, tc.want)
		}
		if _, err := Parse(d.Bytes()); err != nil {
			t.Errorf("%q: output does not parse: %v", tc.in, err)
		}
	}
}

// Invalid UTF-8 behind DEL or a C1 character in a quoted scalar is refused by
// Parse and Unmarshal alike.
func TestInvalidUTF8BehindDelInQuotes(t *testing.T) {
	for _, in := range []string{"a: '\x7f\x8e'\n", "a: \"\x7f\xc2\"\n", "a: '\u0085\xef'\n", "- \"\ufffe\xff\"\n"} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse accepted %q", in)
		}
		var v any
		if err := Unmarshal([]byte(in), &v); err == nil {
			t.Errorf("Unmarshal accepted %q", in)
		}
	}
}
