package oneyaml

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// annotOps are the key and comment edits the region tests apply to an entry.
func annotOps(ptr string) []struct {
	name string
	run  func(d *Document) error
} {
	key := strings.HasSuffix(ptr, "\x00key")
	ptr = strings.TrimSuffix(ptr, "\x00key")
	type op = struct {
		name string
		run  func(d *Document) error
	}
	if key {
		return []op{
			{"rename", func(d *Document) error { return d.Rename(ptr, "zz-new") }},
			{"rename-quoted", func(d *Document) error { return d.Rename(ptr, "needs: quoting #") }},
		}
	}
	return []op{
		{"head", func(d *Document) error { return d.SetHeadComment(ptr, "one\ntwo") }},
		{"head-clear", func(d *Document) error { return d.SetHeadComment(ptr, "") }},
		{"line", func(d *Document) error { return d.SetLineComment(ptr, "note") }},
		{"line-clear", func(d *Document) error { return d.SetLineComment(ptr, "") }},
	}
}

// With the region path off every rename and comment edit parses the whole stream;
// the region path must give the same text, or the same refusal, for every entry of
// every input.
func TestAnnotateRegionAgreesWithWhole(t *testing.T) {
	var inputs [][]byte
	inputs = append(inputs, kitSeeds(t)...)
	own := []string{
		"a: 1\nb:\n  - x\n  - y: 2\n    z: [1, 2]\n# tail\n",
		"# head\na: 1 # one\n\n# gap\nb: 2\n# own b\nc: |\n  lit\n  # not a comment\nd: 4\n",
		"? a\n: 1\n? |\n  b\n: 2\n",
		"- &x a\n- *x\n- {k: v, 'q r': \"s\"}\n- |\n  lit\n- >-\n  fold\n",
		"--- # m\na: 1\n...\n--- !!map\nb: 2 # c\n",
		"a: 1\r\nb: |\r\n  t\r\nc: 3\r\n",
		"a: 'multi\n  line'\nb: plain\n  continued\nc: \"q\n  r\"\nd: 1\n",
		"x:\n  - # c\n    a: 1\n  - b: 2\n    # c2\n    c: 3\ny: [1,\n  2]\n",
		string(annotWideMap(3000)),
		string(annotRecords(3000)),
	}
	for _, s := range own {
		inputs = append(inputs, []byte(s))
	}
	limit := 60
	if testing.Short() {
		limit = 12
	}
	regions, compared := 0, 0
	for ii, in := range inputs {
		d0, err := Parse(in)
		if err != nil {
			continue
		}
		entries := probeEntries(d0)
		if len(entries) > limit {
			// Keep the first few and the last few: the end of a collection is a case of its own.
			entries = append(entries[:limit/2:limit/2], entries[len(entries)-limit/2:]...)
		}
		for _, e := range entries {
			if e.doc != 0 {
				continue
			}
			for _, op := range annotOps(e.ptr) {
				run := func(off bool) (string, string, int) {
					regionOff = off
					defer func() { regionOff = false }()
					d, err := Parse(in)
					if err != nil {
						t.Fatal(err)
					}
					err = op.run(d)
					msg := ""
					if err != nil {
						msg = err.Error()
					}
					// The stream must still read back and agree with a fresh parse.
					if _, perr := Parse(d.Bytes()); perr != nil {
						t.Fatalf("input %d %s %q: the edit left text that does not parse: %v", ii, op.name, e.ptr, perr)
					}
					return string(d.Bytes()), msg, d.s.es().work.regions
				}
				whole, wmsg, _ := run(true)
				got, gmsg, r := run(false)
				regions += r
				compared++
				if got != whole || gmsg != wmsg {
					t.Fatalf("input %d (%q) %s %q:\nregion %q (%q)\nwhole  %q (%q)", ii, in, op.name, e.ptr, got, gmsg, whole, wmsg)
				}
			}
		}
	}
	t.Logf("%d edits compared, %d through a region", compared, regions)
	if regions == 0 {
		t.Fatal("no edit went through the region path")
	}
}

// A rename or a comment in a large document parses the lines around it, not the
// stream, as Set does.
func TestAnnotateParsesOnlyTheRegion(t *testing.T) {
	for name, data := range map[string][]byte{"wide-map": annotWideMap(64 << 10), "records": annotRecords(64 << 10)} {
		n := annotEntries(t, data)
		ptr := fmt.Sprintf("/key%d", n-5)
		if name == "records" {
			ptr = fmt.Sprintf("/%d/email", n-5)
		}
		for _, op := range []struct {
			name string
			run  func(d *Document) error
		}{
			{"rename", func(d *Document) error { return d.Rename(ptr, "renamed") }},
			{"head-comment", func(d *Document) error { return d.SetHeadComment(ptr, "a comment") }},
			{"line-comment", func(d *Document) error { return d.SetLineComment(ptr, "a comment") }},
		} {
			d, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			w := d.s.es().work
			if err := op.run(d); err != nil {
				t.Fatalf("%s %s: %v", name, op.name, err)
			}
			w2 := d.s.es().work
			if w2.wholes != w.wholes || w2.regions != w.regions+1 {
				t.Errorf("%s %s: %d whole parses and %d regions, want 0 and 1", name, op.name, w2.wholes-w.wholes, w2.regions-w.regions)
			}
			if got := w2.bytes - w.bytes; got > len(data)/8 {
				t.Errorf("%s %s: parsed %d bytes of %d", name, op.name, got, len(data))
			}
		}
	}
}

// A rename or a comment in the middle and at both ends of a wide mapping and of a
// long sequence gives the text the whole-stream parse gives.
func TestAnnotateAtEdgesOfLargeCollections(t *testing.T) {
	maps := annotWideMap(40 << 10)
	seqs := annotRecords(40 << 10)
	nm, ns := annotEntries(t, maps), annotEntries(t, seqs)
	var picks []struct {
		data []byte
		ptr  string
	}
	for _, i := range []int{0, 1, 2, 3, nm / 2, nm - 4, nm - 3, nm - 2, nm - 1} {
		picks = append(picks, struct {
			data []byte
			ptr  string
		}{maps, fmt.Sprintf("/key%d", i)})
	}
	for _, i := range []int{0, 1, 2, ns / 2, ns - 3, ns - 2, ns - 1} {
		picks = append(picks, struct {
			data []byte
			ptr  string
		}{seqs, fmt.Sprintf("/%d/email", i)}, struct {
			data []byte
			ptr  string
		}{seqs, fmt.Sprintf("/%d", i)})
	}
	for _, p := range picks {
		for _, op := range annotOps(p.ptr) {
			var outs [2][]byte
			var msgs [2]string
			for k, off := range []bool{true, false} {
				regionOff = off
				d, _ := Parse(p.data)
				if err := op.run(d); err != nil {
					msgs[k] = err.Error()
				}
				outs[k] = append([]byte(nil), d.Bytes()...)
				regionOff = false
			}
			if !bytes.Equal(outs[0], outs[1]) || msgs[0] != msgs[1] {
				t.Fatalf("%s %s: region and whole parse differ (%q vs %q)", op.name, p.ptr, msgs[1], msgs[0])
			}
		}
	}
}
