package oneyaml_test

import (
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
)

// A flow mapping with many keys, each followed by an empty value, is refused without
// a quadratic scan; the work it costs is counted in the scanner's own scaling test.
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
	_, err := oneyaml.Parse(src)
	if err == nil {
		t.Fatal("an unclosed flow mapping was accepted")
	}
}
