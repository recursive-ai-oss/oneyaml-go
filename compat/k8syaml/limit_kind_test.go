package yaml_test

import (
	"errors"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
	yaml "oneyaml.dev/go/compat/k8syaml"
)

// A limit error from this package names the limit and does not suggest an
// option, because the package takes none.
func TestLimitErrorsNameNoOption(t *testing.T) {
	deep := strings.Repeat("[", 20000) + strings.Repeat("]", 20000)
	_, err := yaml.YAMLToJSON([]byte(deep))
	var le *oneyaml.LimitError
	if !errors.As(err, &le) || le.Limit != oneyaml.LimitMaxDepth || !errors.Is(err, oneyaml.ErrMaxDepth) {
		t.Fatalf("depth: %v", err)
	}
	if le.Option != "" || strings.Contains(err.Error(), "raise it with") {
		t.Errorf("suggests an option: %q (%v)", le.Option, err)
	}
}
