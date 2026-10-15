package oneyaml_test

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// keyForm reports whether the output starts with the explicit "? " form, and
// otherwise the length in characters of the first written implicit key.
func keyForm(out string) (explicit bool, length int) {
	if strings.HasPrefix(out, "? ") {
		return true, 0
	}
	line, _, _ := strings.Cut(out, "\n")
	key, _, _ := strings.Cut(line, ": ")
	return false, utf8.RuneCountInString(key)
}

// An implicit key may be at most 1024 characters as written, quotes and escapes
// included; longer keys must use "? ".
func TestImplicitKeyLimitIsOnWrittenKey(t *testing.T) {
	keys := map[string]string{
		"leading space (single quoted)": " " + strings.Repeat("a", 1022),
		"apostrophes doubled":           strings.Repeat("'", 600),
		"control chars escaped":         strings.Repeat("\x01", 300),
		"plain":                         strings.Repeat("a", 1025),
	}
	for name, k := range keys {
		out := mustMarshal(t, map[string]any{k: "v"})
		if explicit, n := keyForm(out); !explicit {
			t.Errorf("%s: implicit key of %d characters written", name, n)
		}
	}
	// The limit is not applied early: 1024 written characters stay implicit,
	// and the limit counts characters, not bytes.
	for name, k := range map[string]string{
		"plain":       strings.Repeat("a", 1024),
		"quoted":      " " + strings.Repeat("a", 1021),
		"multi-byte":  strings.Repeat("é", 1024),
		"short bytes": strings.Repeat("é", 600),
	} {
		out := mustMarshal(t, map[string]any{k: "v"})
		if explicit, n := keyForm(out); explicit || n > 1024 {
			t.Errorf("%s: explicit=%v length=%d", name, explicit, n)
		}
	}
}

func TestImplicitKeyLimitForFieldNames(t *testing.T) {
	for name, tag := range map[string]string{
		"quoted": " " + strings.Repeat("a", 1022),
		"plain":  strings.Repeat("a", 1025),
	} {
		st := reflect.StructOf([]reflect.StructField{{
			Name: "A", Type: reflect.TypeOf(0), Tag: reflect.StructTag(`yaml:"` + tag + `"`),
		}})
		out := mustMarshal(t, reflect.New(st).Elem().Interface())
		if explicit, n := keyForm(out); !explicit {
			t.Errorf("%s: implicit field key of %d characters written", name, n)
		}
	}
}
