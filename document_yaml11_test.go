package oneyaml

import (
	"reflect"
	"strings"
	"testing"
)

var yaml11Ambiguous = []string{"yes", "on", "y", "0755", "12:30", "2001-01-01", "1_000", "~"}

// readsBack checks that out reads as the document want would after the edit, so
// the text of a quoted string is not lost.
func readsBack(t *testing.T, out []byte, path string, want string) {
	t.Helper()
	var m map[string]any
	if err := Unmarshal(out, &m); err != nil {
		t.Fatalf("%q unreadable: %v", out, err)
	}
	if got := m[path]; !reflect.DeepEqual(got, want) {
		t.Errorf("%q: %q reads %#v, want %q", out, path, got, want)
	}
}

func TestYAML11StringsSet(t *testing.T) {
	for _, s := range yaml11Ambiguous {
		d, _ := Parse([]byte("a: 1\n"), YAML11Strings())
		if err := d.Set("/a", s); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(d.String(), "a: "+s+"\n") {
			t.Errorf("%s written plain: %q", s, d.String())
		}
		readsBack(t, d.Bytes(), "a", s)
		// Without the option the text is as before.
		p, _ := Parse([]byte("a: 1\n"))
		_ = p.Set("/a", s)
		readsBack(t, p.Bytes(), "a", s)
	}
	d, _ := Parse([]byte("a: 1\n"), YAML11Strings())
	_ = d.Set("/a", "hello")
	if d.String() != "a: hello\n" {
		t.Errorf("hello: %q", d.String())
	}
	// Not strings: unchanged.
	_ = d.Set("/a", true)
	_ = d.Set("/b", 755)
	if d.String() != "a: true\nb: 755\n" {
		t.Errorf("non-strings: %q", d.String())
	}
}

func TestYAML11StringsDefaultUnchanged(t *testing.T) {
	d, _ := Parse([]byte("a: 1\n"))
	_ = d.Set("/a", "yes")
	_ = d.Set("/y", "n")
	if d.String() != "a: yes\ny: n\n" {
		t.Errorf("default changed: %q", d.String())
	}
}

func TestYAML11StringsKeepExistingStyle(t *testing.T) {
	for _, c := range []struct{ doc, want string }{
		{"a: \"x\"\n", "a: \"yes\"\n"},
		{"a: 'x'\n", "a: 'yes'\n"},
		{"a: x\n", "a: 'yes'\n"},
		{"a: |\n  x\n  y\n", "a: |\n  x\n  yes\n"},
	} {
		d, _ := Parse([]byte(c.doc), YAML11Strings())
		v := "yes"
		if strings.Contains(c.doc, "|") {
			v = "x\nyes\n"
		}
		if err := d.Set("/a", v); err != nil {
			t.Fatal(err)
		}
		if d.String() != c.want {
			t.Errorf("%q: got %q, want %q", c.doc, d.String(), c.want)
		}
	}
}

func TestYAML11StringsPatchMergeRename(t *testing.T) {
	for _, s := range yaml11Ambiguous {
		q := `"` + s + `"`
		for _, c := range []struct {
			name string
			do   func(d *Document) error
			key  string
		}{
			{"add", func(d *Document) error { return d.ApplyPatch([]byte(`[{"op":"add","path":"/z","value":` + q + `}]`)) }, "z"},
			{"replace", func(d *Document) error {
				return d.ApplyPatch([]byte(`[{"op":"replace","path":"/a","value":` + q + `}]`))
			}, "a"},
			{"copy", func(d *Document) error {
				if err := d.Set("/s", s); err != nil {
					return err
				}
				return d.ApplyPatch([]byte(`[{"op":"copy","from":"/s","path":"/z"}]`))
			}, "z"},
			{"merge", func(d *Document) error { return d.ApplyMergePatch([]byte(`{"z":` + q + `}`)) }, "z"},
		} {
			d, _ := Parse([]byte("a: 1\n"), YAML11Strings())
			if err := c.do(d); err != nil {
				t.Fatalf("%s %s: %v", c.name, s, err)
			}
			if strings.Contains(d.String(), c.key+": "+s+"\n") {
				t.Errorf("%s %s written plain: %q", c.name, s, d.String())
			}
			readsBack(t, d.Bytes(), c.key, s)
		}

		// Rename: the new key is quoted, and the entry reads back under it.
		d, _ := Parse([]byte("a: 1\n"), YAML11Strings())
		if err := d.Rename("/a", s); err != nil {
			t.Fatalf("rename %s: %v", s, err)
		}
		if strings.HasPrefix(d.String(), s+":") {
			t.Errorf("rename %s: key plain: %q", s, d.String())
		}
		var m map[string]any
		if err := Unmarshal(d.Bytes(), &m); err != nil || m[s] != 1 {
			t.Errorf("rename %s: %q reads %v (%v)", s, d.String(), m, err)
		}
		// Without the option the key stays plain where Core allows it.
	}
	d, _ := Parse([]byte("a: 1\n"))
	_ = d.Rename("/a", "yes")
	if d.String() != "yes: 1\n" {
		t.Errorf("default rename: %q", d.String())
	}
	// A new key through add is quoted too.
	e, _ := Parse([]byte("a: 1\n"), YAML11Strings())
	_ = e.ApplyPatch([]byte(`[{"op":"add","path":"/on","value":1}]`))
	if e.String() != "a: 1\n'on': 1\n" {
		t.Errorf("add key: %q", e.String())
	}
}

// Views of one stream share the option.
func TestYAML11StringsDocView(t *testing.T) {
	d, err := Parse([]byte("a: 1\n---\nb: 2\n"), YAML11Strings())
	if err != nil {
		t.Fatal(err)
	}
	v := d.Doc(1)
	if err := v.Set("/b", "no"); err != nil {
		t.Fatal(err)
	}
	if d.String() != "a: 1\n---\nb: 'no'\n" {
		t.Errorf("got %q", d.String())
	}
}
