package oneyaml_test

import (
	"testing"

	oneyaml "oneyaml.dev/go"
)

// nestedRaw keeps the JSON text its UnmarshalJSON method is given.
type nestedRaw struct{ text string }

func (r *nestedRaw) UnmarshalJSON(b []byte) error { r.text = string(b); return nil }

// Numbers inside a collection reach UnmarshalJSON as written, as a number at the
// top does, not rounded through float64.
func TestJSONHookKeepsNestedNumbers(t *testing.T) {
	for _, c := range []struct{ yaml, want string }{
		{"3.14159265358979323846264338327950288", "3.14159265358979323846264338327950288"},
		{"[12345678901234567890123, 1.00000000000000000001, 1e400, 1.50, 1E3, -0]", "[12345678901234567890123,1.00000000000000000001,1e400,1.50,1E3,-0]"},
		{"{big: 12345678901234567890123, pi: 3.14159265358979323846264338327950288, deep: [{x: 0.1000000000000000055511151231257827}]}",
			`{"big":12345678901234567890123,"deep":[{"x":0.1000000000000000055511151231257827}],"pi":3.14159265358979323846264338327950288}`},
		// Forms JSON has no spelling for are still rewritten, and strings stay strings.
		{"[0x1F, .5, +1, '1.50', !!str 2.5, true]", `[31,0.5,1,"1.50","2.5",true]`},
	} {
		var r nestedRaw
		if err := oneyaml.Unmarshal([]byte(c.yaml), &r); err != nil {
			t.Fatalf("%s: %v", c.yaml, err)
		}
		if r.text != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.yaml, r.text, c.want)
		}
		// The same text through a Document.
		var viaDoc nestedRaw
		d, err := oneyaml.Parse([]byte(c.yaml))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Decode(&viaDoc); err != nil || viaDoc.text != c.want {
			t.Errorf("Document %s: got %s, %v", c.yaml, viaDoc.text, err)
		}
	}
}

// A generic read after a hook read does not keep the text of numbers.
func TestJSONHookDoesNotLeakIntoGenericReads(t *testing.T) {
	var pair struct {
		A nestedRaw
		B any
	}
	if err := oneyaml.Unmarshal([]byte("a: [1.5]\nb: [1.5, 2]\n"), &pair); err != nil {
		t.Fatal(err)
	}
	if pair.A.text != "[1.5]" {
		t.Fatalf("hook got %s", pair.A.text)
	}
	if b, ok := pair.B.([]any); !ok || b[0] != 1.5 || b[1] != 2 {
		t.Fatalf("generic value %#v", pair.B)
	}
}
