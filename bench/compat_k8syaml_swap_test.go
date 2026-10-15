package bench

import (
	"os"
	"strings"
	"testing"

	oneprog "oneyaml.dev/go/bench/compat_k8syaml_swap/oneyaml"
	sigsprog "oneyaml.dev/go/bench/compat_k8syaml_swap/sigs"
)

// The program in compat_k8syaml_swap/sigs is written against sigs.k8s.io/yaml.
// The copy next to it has the import path swapped and nothing else changed. Both
// must compile, and both must say the same.
func TestK8sImportSwap(t *testing.T) {
	a, err := os.ReadFile("compat_k8syaml_swap/sigs/prog.go")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("compat_k8syaml_swap/oneyaml/prog.go")
	if err != nil {
		t.Fatal(err)
	}
	swapped := strings.Replace(string(a), `"sigs.k8s.io/yaml"`, `"oneyaml.dev/go/compat/k8syaml"`, 1)
	if swapped != string(b) {
		t.Fatal("the two programs differ in more than the import path")
	}
	want, err := sigsprog.Run()
	if err != nil {
		t.Fatalf("sigs: %v", err)
	}
	got, err := oneprog.Run()
	if err != nil {
		t.Fatalf("oneyaml: %v", err)
	}
	if got != want {
		t.Fatalf("transcripts differ\n--- sigs\n%s\n--- oneyaml\n%s", want, got)
	}
	t.Logf("transcript:\n%s", got)
}
