package yaml_test

import (
	"errors"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
	yaml "oneyaml.dev/go/compat/yamlv3"
)

// A limit error from this package names the limit and does not suggest an
// option, because the package takes none.
func TestLimitErrorsNameNoOption(t *testing.T) {
	deep := strings.Repeat("[", 20000) + strings.Repeat("]", 20000)
	var v any
	err := yaml.Unmarshal([]byte(deep), &v)
	var le *oneyaml.LimitError
	if !errors.As(err, &le) || le.Limit != oneyaml.LimitMaxDepth || !errors.Is(err, oneyaml.ErrMaxDepth) {
		t.Fatalf("Unmarshal: %v", err)
	}
	if le.Option != "" || strings.Contains(err.Error(), "raise it with") {
		t.Errorf("Unmarshal suggests an option: %q (%v)", le.Option, err)
	}

	bomb := "a: &a [x, x, x, x, x, x, x, x, x, x]\nb: &b [*a, *a, *a, *a, *a, *a, *a, *a, *a, *a]\nc: &c [*b, *b, *b, *b, *b, *b, *b, *b, *b, *b]\nd: &d [*c, *c, *c, *c, *c, *c, *c, *c, *c, *c]\ne: &e [*d, *d, *d, *d, *d, *d, *d, *d, *d, *d]\nf: &f [*e, *e, *e, *e, *e, *e, *e, *e, *e, *e]\ng: [*f, *f, *f, *f, *f, *f, *f, *f, *f, *f]\n"
	err = yaml.Unmarshal([]byte(bomb), &v)
	if !errors.As(err, &le) || !errors.Is(err, oneyaml.ErrLimit) {
		t.Fatalf("alias bomb: %v", err)
	}
	if le.Option != "" {
		t.Errorf("alias bomb suggests an option: %q", le.Option)
	}
}
