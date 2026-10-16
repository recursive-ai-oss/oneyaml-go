package oneyaml_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
)

// A key that does not fit the key type is a mismatch like a value that does not
// fit: its entry is skipped, the others are read and all mismatches are reported.
func TestMapKeyMismatchIsCollected(t *testing.T) {
	var m map[int]string
	err := oneyaml.Unmarshal([]byte("1: a\nx: b\n2: c\ny: [d]\n3: e\n"), &m)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) {
		t.Fatalf("got %T %v", err, err)
	}
	if fmt.Sprint(m) != "map[1:a 2:c 3:e]" {
		t.Errorf("map %v", m)
	}
	all := te.All()
	if len(all) != 2 || all[0].Path != "/x" || all[0].Line != 2 || all[1].Path != "/y" || all[1].Line != 4 {
		t.Fatalf("errors %+v", all)
	}
	// The message has the shape of a value mismatch.
	var v map[string]int
	verr := oneyaml.Unmarshal([]byte("k: z\n"), &v)
	var ve *oneyaml.TypeError
	if !errors.As(verr, &ve) || !strings.Contains(te.Error(), "cannot unmarshal") || !strings.Contains(ve.Error(), "cannot unmarshal") {
		t.Errorf("messages: %v / %v", err, verr)
	}
}

// Keys nested in a typed value are collected too, with the full path, and a
// struct field of the same map still decodes.
func TestMapKeyMismatchNested(t *testing.T) {
	var c struct {
		By   map[int]int
		Name string
	}
	err := oneyaml.Unmarshal([]byte("by: {1: 1, a: 2, 2: b}\nname: ok\n"), &c)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) {
		t.Fatalf("got %v", err)
	}
	if fmt.Sprint(c.By) != "map[1:1]" || c.Name != "ok" {
		t.Errorf("got %v %q", c.By, c.Name)
	}
	all := te.All()
	if len(all) != 2 || all[0].Path != "/by/a" || all[1].Path != "/by/2" {
		t.Fatalf("errors %+v", all)
	}
}

// The bound of 100 mismatches applies to keys as well, and a document that is
// not valid still fails with its syntax error.
func TestMapKeyMismatchBoundAndSyntax(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&sb, "k%d: 1\n", i)
	}
	var m map[int]int
	err := oneyaml.Unmarshal([]byte(sb.String()), &m)
	var te *oneyaml.TypeError
	if !errors.As(err, &te) || len(te.All()) != 100 {
		t.Fatalf("got %v", err)
	}
	err = oneyaml.Unmarshal([]byte("a: 1\nb: [\n"), &m)
	var se *oneyaml.SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("syntax error lost: %T %v", err, err)
	}
}

// A later duplicate of a key that was read is still a duplicate-key error.
func TestMapKeyMismatchKeepsDuplicateCheck(t *testing.T) {
	var m map[int]int
	err := oneyaml.Unmarshal([]byte("1: 1\nx: 1\n1: 2\n"), &m)
	var se *oneyaml.SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("got %T %v", err, err)
	}
}
