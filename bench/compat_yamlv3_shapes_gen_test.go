package bench

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	yv3 "go.yaml.in/yaml/v3"
	compat "oneyaml.dev/go/compat/yamlv3"
)

// commentShapeInput builds a block document with comments, blank lines, flow
// collections, explicit keys and block scalars at random places. Many of the
// inputs are not what a person would write; the point is to cover the
// neighbourhood of every comment position.
func commentShapeInput(r *rand.Rand) string {
	var b strings.Builder
	pick := func(xs ...string) string { return xs[r.Intn(len(xs))] }
	// filler is what may stand between two entries.
	filler := func(ind string) {
		switch r.Intn(9) {
		case 0:
			b.WriteString(ind + "# c\n")
		case 1:
			b.WriteString("\n")
		case 2:
			b.WriteString(ind + "# c\n# d\n")
		case 3:
			b.WriteString(ind + "# c\n\n")
		case 4:
			b.WriteString("\n" + ind + "# c\n")
		}
	}
	line := func() string { return pick("", "", " # l") }
	var node func(ind string, d int)
	node = func(ind string, d int) {
		n := 1 + r.Intn(3)
		kind := r.Intn(3)
		for i := 0; i < n; i++ {
			filler(pick(ind, ind, ""))
			kc := r.Intn(8)
			if d >= 3 && kc >= 4 {
				kc = 0
			}
			switch kind {
			case 0, 1:
				key := fmt.Sprintf("k%d", i)
				lead := key + ":"
				if kind == 1 && r.Intn(4) == 0 {
					lead = "? " + key + line() + "\n" + ind + ":"
				}
				switch kc {
				case 0, 1, 2:
					b.WriteString(ind + lead + " v" + line() + "\n")
				case 3:
					b.WriteString(ind + lead + " [a, b]" + line() + "\n")
				case 4, 5:
					b.WriteString(ind + lead + line() + "\n")
					node(ind+"  ", d+1)
				case 6:
					b.WriteString(ind + lead + " |" + line() + "\n" + ind + "  text\n" + ind + "  more\n")
				default:
					b.WriteString(ind + lead + " >" + line() + "\n" + ind + "  text\n" + ind + "\n" + ind + "  more\n")
				}
			default:
				switch kc {
				case 0, 1, 2:
					b.WriteString(ind + "- v" + line() + "\n")
				case 3:
					b.WriteString(ind + "- {a: 1}" + line() + "\n")
				case 4:
					b.WriteString(ind + "- " + strings.TrimSpace(pick("# c", "# c", "")) + "\n")
					filler(ind + "  ")
					node(ind+"  ", d+1)
				case 5:
					b.WriteString(ind + "- k: v" + line() + "\n")
					b.WriteString(ind + "  j: w\n")
				case 6:
					b.WriteString(ind + "- |" + line() + "\n" + ind + "  text\n")
				default:
					b.WriteString(ind + "- - x" + line() + "\n")
				}
			}
		}
	}
	switch r.Intn(6) {
	case 0:
		b.WriteString("# top\n\n")
	case 1:
		b.WriteString("--- " + pick("# c", "") + "\n")
	case 2:
		b.WriteString("# top\n")
	}
	node("", 0)
	filler("")
	return b.String()
}

// commentShapeCeiling is how many of the generated inputs may still differ from
// yaml.v3. The count only goes down: a change that raises it fails, and a
// change that lowers it should lower this number too.
const (
	commentShapeReadCeiling  = 1136
	commentShapeWriteCeiling = 83
)

// TestCompatV3CommentShapesGenerated compares reading and writing on generated
// block documents with comments. Differences that the intended-difference
// rules explain are not counted.
func TestCompatV3CommentShapesGenerated(t *testing.T) {
	n := 4000
	if testing.Short() {
		n = 600
	}
	r := rand.New(rand.NewSource(8787))
	parsed, readDiff, writeDiff := 0, 0, 0
	var readEx, writeEx []string
	for i := 0; i < n; i++ {
		in := commentShapeInput(r)
		var y yv3.Node
		var c compat.Node
		if yv3.Unmarshal([]byte(in), &y) != nil {
			continue
		}
		if err := compat.Unmarshal([]byte(in), &c); err != nil {
			t.Errorf("%q: shim: %v", in, err)
			continue
		}
		parsed++
		if a, b := dumpV3(&y, nodeFields{comments: true}), dumpCompat(&c, nodeFields{comments: true}); a != b {
			readDiff++
			if len(readEx) < 5 {
				readEx = append(readEx, fmt.Sprintf("%q\n%s", in, firstLineDiff(a, b)))
			}
		}
		yb, yerr := yv3.Marshal(&y)
		cb, cerr := compat.Marshal(toCompat(&y, map[*yv3.Node]*compat.Node{}))
		if yerr == nil && cerr == nil && string(yb) != string(cb) && writeDifference(&y, yb, cb, yerr, cerr) == "" {
			writeDiff++
			if len(writeEx) < 5 {
				writeEx = append(writeEx, fmt.Sprintf("%q\nyaml.v3: %q\nshim:    %q", in, yb, cb))
			}
		}
	}
	t.Logf("inputs=%d read-differ=%d write-differ=%d", parsed, readDiff, writeDiff)
	for _, e := range readEx {
		t.Logf("read: %s", e)
	}
	for _, e := range writeEx {
		t.Logf("write: %s", e)
	}
	if !testing.Short() && (readDiff > commentShapeReadCeiling || writeDiff > commentShapeWriteCeiling) {
		t.Errorf("more generated comment shapes differ from yaml.v3 than before: read %d (max %d), write %d (max %d)", readDiff, commentShapeReadCeiling, writeDiff, commentShapeWriteCeiling)
	}
}
