package oneyaml_test

import (
	"testing"

	"oneyaml.dev/go"
)

// A byte order mark may start any document, not only the first; it is not
// indentation (kit case edit/add-to-bare-document-after-bom).
func TestAddAfterBOMOfLaterDocument(t *testing.T) {
	for _, tc := range []struct{ in, ptr, want string }{
		{"a: 1\n...\n\ufeffa: |\n  t\n", "/n", "a: 1\n...\n\ufeffa: |\n  t\nn: 1\n"},
		{"a: 1\n...\n\ufeffa: 2\n", "/n", "a: 1\n...\n\ufeffa: 2\nn: 1\n"},
		{"a: 1\n...\n\ufeffx:\n  y: 2\n", "/x/n", "a: 1\n...\n\ufeffx:\n  y: 2\n  n: 1\n"},
	} {
		d, err := oneyaml.Parse([]byte(tc.in))
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		doc := d.Doc(1)
		if err := doc.Set(tc.ptr, 1); err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got := string(d.Bytes()); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The colon of an outer mapping's entry, further out than an explicit key, is
// not the value colon of that key: setting the key's null value writes a new
// value line at the key's own indentation.
func TestSetNullValueOfExplicitKeyBeforeOuterColon(t *testing.T) {
	in := "c:\n  ? >\n   a\n: 1\n"
	d, err := oneyaml.Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Set("/c/a\n", 773.5); err != nil {
		t.Fatalf("set: %v", err)
	}
	out := string(d.Bytes())
	var got struct{ C map[string]float64 }
	if err := oneyaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%q does not parse: %v", out, err)
	}
	if got.C["a\n"] != 773.5 {
		t.Errorf("%q reads back as %v", out, got.C)
	}
	var root map[string]any
	if err := oneyaml.Unmarshal([]byte(out), &root); err != nil || root[""] != 1 {
		t.Errorf("the outer entry changed: %v %v", root, err)
	}
	t.Logf("%q", out)
}

// A head comment on the first entry of a document that starts with a byte order
// mark goes after the mark, which stays at the start of the line.
func TestHeadCommentAfterBOMOfLaterDocument(t *testing.T) {
	d, err := oneyaml.Parse([]byte("a: 1\n...\n\ufeffa: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Doc(1).SetHeadComment("/a", "note"); err != nil {
		t.Fatal(err)
	}
	out := string(d.Bytes())
	if _, err := oneyaml.Parse([]byte(out)); err != nil {
		t.Fatalf("%q does not parse: %v", out, err)
	}
	if want := "a: 1\n...\n\ufeff# note\na: 2\n"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}
