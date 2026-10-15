package oneyaml_test

import (
	"testing"

	"oneyaml.dev/go"
)

// Strings that some common reader takes for a number, timestamp or merge key
// are quoted; near misses stay plain.
func TestMarshalQuotesStringsOtherReadersMisread(t *testing.T) {
	quoted := []string{
		"_1", "+_1", "1e3_", "0_x123", "1_0.5", "_.5",
		"2001-12-14", "2001-1-1", "2001-1-1 1:2:3", "2001-12-14t21:59:43.10-05:00", "2001-12-14 21:59:43.10 -5",
		"<<", "=", "inf", "-Infinity", "NaN", "0x1p-2",
	}
	for _, s := range quoted {
		got := mustMarshal(t, []string{s})
		if got != "- '"+s+"'\n" && got != "- \""+s+"\"\n" {
			t.Errorf("%q written as %q, want it quoted", s, got)
		}
		var back []string
		if err := oneyaml.Unmarshal([]byte(got), &back); err != nil || len(back) != 1 || back[0] != s {
			t.Errorf("%q does not read back: %q, %v", s, back, err)
		}
	}
	for _, s := range []string{"_", "_id", "a_b", "info", "name", "2001-1-1 1:2", "nginx-ingress"} {
		if got := mustMarshal(t, []string{s}); got != "- "+s+"\n" {
			t.Errorf("%q written as %q, want plain", s, got)
		}
	}
}
