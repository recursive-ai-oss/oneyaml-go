package oneyaml_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	oneyaml "oneyaml.dev/go"
)

// A flow mapping with many keys, each followed by an empty value, builds its tree
// in time linear in the number of keys.
func TestParseFlowMappingWithManyEmptyValues(t *testing.T) {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	var sb strings.Builder
	sb.WriteString(strings.Repeat("{", 100))
	for i := 0; i < 140608; i++ {
		sb.WriteByte('x')
		for k, v := 0, i; k < 3; k, v = k+1, v/len(letters) {
			sb.WriteByte(letters[v%len(letters)])
		}
		sb.WriteString(",\n")
	}
	src := []byte(sb.String())
	start := time.Now()
	_, err := oneyaml.Parse(src)
	if err == nil {
		t.Fatal("an unclosed flow mapping was accepted")
	}
	// The race detector slows everything several times over, so the time
	// bound only means something without it.
	if d := time.Since(start); !raceEnabled && d > 3*time.Second {
		t.Errorf("Parse took %v for %d bytes", d, len(src))
	}
}

// Editing a flow mapping with many keys costs time proportional to what the edit
// touches, not to the size of the mapping.
func TestEditFlowMappingWithManyKeys(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("{")
	for i := 0; i < 140000; i++ {
		fmt.Fprintf(&sb, "k%d: %d,\n", i, i)
	}
	sb.WriteString("}\n")
	doc, err := oneyaml.Parse([]byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for i := 0; i < 50; i++ {
		if err := doc.Set(fmt.Sprintf("/k%d", 70000+i), 7); err != nil {
			t.Fatal(err)
		}
		if err := doc.Remove(fmt.Sprintf("/k%d", 100000+i)); err != nil {
			t.Fatal(err)
		}
	}
	// The race detector slows everything several times over, so the time
	// bound only means something without it.
	if d := time.Since(start); !raceEnabled && d > 3*time.Second {
		t.Errorf("100 edits took %v", d)
	}
}
