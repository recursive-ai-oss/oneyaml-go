package oneyaml

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/cst"
)

// Streams made of pieces that include documents without content. The pass that
// checks its result with a digest must keep a pass exactly when the one that holds
// two trees does.
func TestBatchDigestAgreesOnStreamsOfManyDocuments(t *testing.T) {
	oldMin, oldExact := batchMin, batchExactBelow
	defer func() { batchMin, batchExactBelow = oldMin, oldExact }()
	batchMin = 1
	var body strings.Builder
	var keys []string
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&body, "p%d: %d\n", i, i)
		if i%2 == 0 {
			keys = append(keys, fmt.Sprintf("p%d", i))
		}
	}
	pieces := []string{"---\n", "...\n", "# c\n", "--- # c\n", "--- !!null\n", "--- |\n  t\n", "--- >-\n", "--- &a\n", "%YAML 1.2\n---\n", "--- 1\n", "--- ''\n", "\n", "...\n---\n"}
	r := rand.New(rand.NewSource(1))
	kept, declined := 0, 0
	n := 400
	if testing.Short() {
		n = 120
	}
	for i := 0; i < n; i++ {
		var sb strings.Builder
		for j := r.Intn(3); j > 0; j-- {
			sb.WriteString(pieces[r.Intn(len(pieces))])
		}
		if r.Intn(4) > 0 {
			sb.WriteString("---\n")
		}
		sb.WriteString(body.String())
		for j := r.Intn(4); j > 0; j-- {
			sb.WriteString(pieces[r.Intn(len(pieces))])
		}
		src := sb.String()
		probe, err := Parse([]byte(src))
		if err != nil {
			continue
		}
		doc := -1
		for k := 0; k < probe.Len(); k++ {
			var m map[string]any
			if probe.Doc(k).Get("", &m) == nil && len(m) == 12 {
				doc = k
			}
		}
		if doc < 0 {
			continue
		}
		merge, rfc := removalPatches("", keys)
		for kind, p := range map[string][]byte{"merge": merge, "6902": rfc} {
			run := func(exact int) (*Document, error) {
				batchExactBelow = exact
				d := mustParse(t, src)
				v := d.Doc(doc)
				if kind == "merge" {
					return d, v.ApplyMergePatch(p)
				}
				return d, v.ApplyPatch(p)
			}
			two, errTwo := run(1 << 30)
			dig, errDig := run(0)
			if (errTwo == nil) != (errDig == nil) || !bytes.Equal(two.Bytes(), dig.Bytes()) {
				t.Fatalf("%q %s: two trees %v %q; digest %v %q", src, kind, errTwo, two.Bytes(), errDig, dig.Bytes())
			}
			b2, bd := 0, 0
			if two.s.ed != nil {
				b2 = two.s.ed.work.batches
			}
			if dig.s.ed != nil {
				bd = dig.s.ed.work.batches
			}
			if b2 != bd {
				t.Errorf("%q %s: the pass ran %d times with two trees and %d with the digest", src, kind, b2, bd)
			}
			if bd > 0 {
				kept++
			} else {
				declined++
			}
		}
	}
	t.Logf("kept %d, declined %d", kept, declined)
	if kept < n/2 {
		t.Fatalf("the pass ran only %d times", kept)
	}
}

// A document without a root node (Root 0) is kept as it is by the digest. The parser
// always gives a document a root, so the tree is built by hand.
func TestBatchDigestKeepsDocumentsWithoutRoot(t *testing.T) {
	d := mustParse(t, "a: 1\nb: 2\nc: 3\n")
	var first int32
	for k := range d.Root().Entries() {
		first = k.i
		break
	}
	root := d.Root().i
	tr := &d.s.tree
	tr.Docs = append(append([]cst.Doc{{}}, tr.Docs...), cst.Doc{})
	_, roots, ok := newKeptDigest().ofOld(tr, d.s.src, root, map[int32]struct{}{first: {}})
	if !ok || len(roots) != 3 || roots[0] != 0 || roots[1] != 1 || roots[2] != 0 {
		t.Fatalf("ok %v roots %v, want true [0 1 0]", ok, roots)
	}
}
