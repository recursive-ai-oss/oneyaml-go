package edit

import (
	"math/rand"
	"testing"

	"oneyaml.dev/go/internal/cst"
)

// Taking the children of the root as the run, SameRegionShape must give the answer
// SameShape gives for the whole tree, for any splice.
func TestSameRegionShapeAgreesWithSameShape(t *testing.T) {
	docs := []string{
		"a: 1\nb: two\nc:\n  - x\n  - y: 'q'\n    z: [1, 2]\nd: |\n  lit\ne: &an v\nf: *an\n",
		"- a\n- b: 1\n  c: \"s\"\n- [p, q]\n- !!str t\n- |\n  block\n- ? k\n  : v\n",
		"k1: v1\nk2: {a: 1, b: [x, y]}\nk3: 'multi\n  line'\nk4: plain\n  more\n",
	}
	toks := []string{"", "x", " ", "\n", ": ", "'q'", "\"d\"", "#c\n", "- z\n", "  ", "k: v\n", "[", "]", "&a ", "!t "}
	r := rand.New(rand.NewSource(7))
	agree, same := 0, 0
	for _, d := range docs {
		src := []byte(d)
		var old cst.Tree
		if err := cst.Build(&old, src, -1); err != nil {
			t.Fatal(err)
		}
		if old.Nodes[1].Prop != 0 || len(old.Docs) != 1 {
			t.Fatal("bad fixture")
		}
		var scalars []int32
		for i := 2; i < len(old.Nodes); i++ {
			if old.Nodes[i].Kind == cst.Scalar {
				scalars = append(scalars, int32(i))
			}
		}
		for n := 0; n < 4000; n++ {
			a := r.Intn(len(src) + 1)
			b := min(a+r.Intn(6), len(src))
			rep := toks[r.Intn(len(toks))]
			out := append(append(append([]byte(nil), src[:a]...), rep...), src[b:]...)
			var nt cst.Tree
			if err := cst.Build(&nt, out, -1); err != nil || len(nt.Docs) != 1 || nt.Nodes[1].Kind != old.Nodes[1].Kind {
				continue
			}
			sp := Splice{At: a, End: b, NewLen: len(rep)}
			skip := int32(0)
			if n%2 == 1 {
				skip = scalars[r.Intn(len(scalars))]
			}
			// The root stands for the container, which a region edit does not rebuild:
			// the whole-tree check is made with the offsets of the root taken out.
			got := SameRegionShape(&old, 2, int32(len(old.Nodes)-1), 1, &nt, 0, sp, skip)
			oc, nc := old, nt
			oc.Nodes = append([]cst.Node(nil), old.Nodes...)
			nc.Nodes = append([]cst.Node(nil), nt.Nodes...)
			oc.Nodes[1].Start, oc.Nodes[1].End, nc.Nodes[1].Start, nc.Nodes[1].End = 0, 0, 0, 0
			want := SameShape(&oc, &nc, sp, skip)
			if got != want {
				t.Fatalf("doc %q splice %v %q skip %d: SameRegionShape %v, SameShape %v", d, sp, rep, skip, got, want)
			}
			agree++
			if want {
				same++
			}
		}
	}
	if same == 0 || agree == same {
		t.Fatalf("the splices must include some that keep the shape and some that do not: %d of %d keep it", same, agree)
	}
}
