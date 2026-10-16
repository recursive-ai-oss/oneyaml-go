package oneyaml

import (
	"fmt"
	"strings"
	"testing"
)

// annotWideMap writes one flat mapping of mixed scalars of about size bytes.
func annotWideMap(size int) []byte {
	var b strings.Builder
	for i := 0; b.Len() < size; i++ {
		switch i % 4 {
		case 0:
			fmt.Fprintf(&b, "key%d: %d\n", i, i)
		case 1:
			fmt.Fprintf(&b, "key%d: plain text %d\n", i, i)
		case 2:
			fmt.Fprintf(&b, "key%d: \"quoted: %d\"\n", i, i)
		default:
			fmt.Fprintf(&b, "key%d: null\n", i)
		}
	}
	return []byte(b.String())
}

// annotRecords writes a sequence of nested records of about size bytes.
func annotRecords(size int) []byte {
	var b strings.Builder
	for i := 0; b.Len() < size || i == 0; i++ {
		fmt.Fprintf(&b, "- id: %d\n  name: Name %d\n  email: n%d@example.com\n  tags:\n    - a\n    - b\n  address:\n    city: Town%d\n    zip: '%05d'\n  note: ~\n", i, i, i, i, i)
	}
	return []byte(b.String())
}

// annotEntries is the number of top-level entries of a document written by the
// generators above.
func annotEntries(b testing.TB, data []byte) int {
	d, err := Parse(data)
	if err != nil {
		b.Fatal(err)
	}
	return d.Root().Len()
}

// BenchmarkAnnotate renames a key, sets a head comment and sets a line comment near
// the end of a large document. An iteration makes two edits that undo each other
// (a name and back, a comment and none), so the text keeps its size and the document
// is parsed once; the figure is for the pair.
func BenchmarkAnnotate(b *testing.B) {
	noSpliceCheck(b)
	type shape struct {
		name string
		data []byte
		ptr  func(n int) (ptr, parent, key string)
	}
	shapes := []shape{
		{"wide-map-64k", annotWideMap(64 << 10), func(n int) (string, string, string) {
			// the third entry from the end
			return fmt.Sprintf("/key%d", n-3), "", fmt.Sprintf("key%d", n-3)
		}},
		{"records-64k", annotRecords(64 << 10), func(n int) (string, string, string) {
			return fmt.Sprintf("/%d/email", n-3), fmt.Sprintf("/%d", n-3), "email"
		}},
	}
	for _, sh := range shapes {
		ptr, parent, key := sh.ptr(annotEntries(b, sh.data))
		run := func(name string, edit func(d *Document) error) {
			b.Run(sh.name+"/"+name, func(b *testing.B) {
				d, err := Parse(sh.data)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				for b.Loop() {
					if err := edit(d); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
		run("rename", func(d *Document) error {
			if err := d.Rename(ptr, "renamed"); err != nil {
				return err
			}
			return d.Rename(parent+"/renamed", key)
		})
		run("head-comment", func(d *Document) error {
			if err := d.SetHeadComment(ptr, "first\nsecond"); err != nil {
				return err
			}
			return d.SetHeadComment(ptr, "")
		})
		run("line-comment", func(d *Document) error {
			if err := d.SetLineComment(ptr, "noted"); err != nil {
				return err
			}
			return d.SetLineComment(ptr, "")
		})
	}
}

// BenchmarkSetInRange sets the value of each entry of a mapping while ranging over
// it, and compares with collecting the keys first. The cost per entry must not
// grow with the size of the mapping.
func BenchmarkSetInRange(b *testing.B) {
	noSpliceCheck(b)
	for _, n := range []int{4000, 16000} {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&sb, "k%d: %d\n", i, i%10)
		}
		src := []byte(sb.String())
		b.Run(fmt.Sprintf("in-loop/n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				d, err := Parse(src)
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				for k := range d.Root().Entries() {
					if err := d.Set("/"+k.Value(), "changed value"); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
		b.Run(fmt.Sprintf("collect-then-set/n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				d, err := Parse(src)
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				var keys []string
				for k := range d.Root().Entries() {
					keys = append(keys, k.Value())
				}
				for _, k := range keys {
					if err := d.Set("/"+k, "changed value"); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
