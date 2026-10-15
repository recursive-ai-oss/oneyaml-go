package oneyaml_test

import (
	"testing"

	"oneyaml.dev/go"
)

// DEL, C1 controls and U+FFFE are valid inside quoted scalars (nb-json) and
// keep their value through Unmarshal and the byte-exact Document.
func TestQuotedScalarsKeepNonPrintableCharacters(t *testing.T) {
	for _, c := range []string{"\x7f", "\u0080", "\u009f", "\ufffe"} {
		for _, q := range []string{`"`, `'`} {
			in := "a: " + q + "x" + c + "y" + q + "\n"
			var v map[string]string
			if err := oneyaml.Unmarshal([]byte(in), &v); err != nil || v["a"] != "x"+c+"y" {
				t.Errorf("%q: %q, %v", in, v, err)
			}
			d, err := oneyaml.Parse([]byte(in))
			if err != nil || string(d.Bytes()) != in {
				t.Errorf("%q: Parse: %v", in, err)
			}
		}
		if err := oneyaml.Unmarshal([]byte("a: x"+c+"y\n"), new(any)); err == nil {
			t.Errorf("plain scalar with %q accepted", c)
		}
	}
}
