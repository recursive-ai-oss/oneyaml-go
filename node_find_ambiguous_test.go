package oneyaml

import (
	"errors"
	"fmt"
	"testing"
)

// A key written twice has no single value: Find and Lookup agree with Get.
func TestNodeFindRepeatedKeyIsAmbiguous(t *testing.T) {
	big := ""
	for i := 0; i < 200; i++ {
		big += fmt.Sprintf("k%d: %d\n", i, i)
	}
	for name, src := range map[string]string{
		"small": "a: 1\nb: 2\na: 3\n",
		"flow":  "{a: 1, b: 2, a: 3}\n",
		"wide":  big + "k7: again\n",
	} {
		doc, err := Parse([]byte(src))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		key := "a"
		if name == "wide" {
			key = "k7"
		}
		var v any
		if err := doc.Get("/"+key, &v); !errors.Is(err, ErrAmbiguousKey) {
			t.Fatalf("%s: Get: %v", name, err)
		}
		root := doc.Root()
		for i := 0; i < 3; i++ { // repeat: the index of a wide mapping is built on later calls
			if _, err := root.Find(key); !errors.Is(err, ErrAmbiguousKey) {
				t.Errorf("%s: Find: %v", name, err)
			}
			if _, ok := root.Lookup(key); ok {
				t.Errorf("%s: Lookup reported a value", name)
			}
		}
		if n, err := root.Find("b"); name != "wide" && (err != nil || n.Value() != "2") {
			t.Errorf("%s: unique key: %v", name, err)
		}
		if _, err := root.Find("missing"); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: absent key: %v", name, err)
		}
		count := 0
		for k := range root.Entries() {
			if k.Value() == key {
				count++
			}
		}
		if count != 2 {
			t.Errorf("%s: Entries yielded the key %d times", name, count)
		}
	}
}
