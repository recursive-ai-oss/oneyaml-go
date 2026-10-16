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

// compatCommentLines counts the non-blank comment lines of a tree.
func compatCommentLines(n *compat.Node) int {
	c := 0
	for _, s := range []string{n.HeadComment, n.LineComment, n.FootComment} {
		for _, l := range strings.Split(s, "\n") {
			if strings.TrimSpace(l) != "" {
				c++
			}
		}
	}
	for _, k := range n.Content {
		c += compatCommentLines(k)
	}
	return c
}

// sourceCommentLines counts the lines of a generated text that hold a comment.
// The generator writes "#" only to start one.
func sourceCommentLines(in string) int {
	c := 0
	for _, l := range strings.Split(in, "\n") {
		if strings.Contains(l, "#") {
			c++
		}
	}
	return c
}

// commentShapeCeiling is how many of the generated inputs may still differ from
// yaml.v3. The count only goes down: a change that raises it fails, and a
// change that lowers it should lower this number too.
const (
	commentShapeReadCeiling  = 997
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
		if got, src := compatCommentLines(&c), sourceCommentLines(in); got > src {
			t.Errorf("%q: the shim reads %d comment lines from a text with %d", in, got, src)
		}
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

// numberComments gives every comment of a generated text its own number
// ("# n1", "# n2", ...), so a line that is lost or read twice can be told from
// another line with the same words.
func numberComments(in string) (string, int) {
	var sb strings.Builder
	n := 0
	for i := 0; i < len(in); i++ {
		if in[i] != '#' {
			sb.WriteByte(in[i])
			continue
		}
		n++
		fmt.Fprintf(&sb, "# n%d", n)
		// Skip the original words up to the end of the line.
		for i+1 < len(in) && in[i+1] != '\n' {
			i++
		}
	}
	return sb.String(), n
}

// commentIDs lists the numbers of the comment lines of a tree, in tree order.
func commentIDs(n *compat.Node, out *[]int) {
	for _, s := range []string{n.HeadComment, n.LineComment, n.FootComment} {
		for _, l := range strings.Split(s, "\n") {
			if _, id, ok := strings.Cut(strings.TrimSpace(l), "# n"); ok {
				var v int
				if _, err := fmt.Sscanf(id, "%d", &v); err == nil {
					*out = append(*out, v)
				}
			}
		}
	}
	for _, k := range n.Content {
		commentIDs(k, out)
	}
}

func yv3IDs(n *yv3.Node, out *[]int) {
	for _, s := range []string{n.HeadComment, n.LineComment, n.FootComment} {
		for _, l := range strings.Split(s, "\n") {
			if _, id, ok := strings.Cut(strings.TrimSpace(l), "# n"); ok {
				var v int
				if _, err := fmt.Sscanf(id, "%d", &v); err == nil {
					*out = append(*out, v)
				}
			}
		}
	}
	for _, k := range n.Content {
		yv3IDs(k, out)
	}
}

// Ceilings for the numbered-comment check, over all the seeds together. They
// only go down.
const (
	numberedTwiceCeiling   = 3
	numberedDroppedCeiling = 2784
	numberedTextCeiling    = 0
)

// TestCompatV3CommentsKeepEveryLine numbers the comments of the generated
// shapes and checks that the shim neither reads a line twice nor loses a line
// that yaml.v3 keeps. It also reads each text with its original words, which
// repeat ("# c" in many places), and counts the inputs where that gives other
// comment lines than the numbered text: one line must never stand in for
// another because they say the same.
func TestCompatV3CommentsKeepEveryLine(t *testing.T) {
	n := 4000
	if testing.Short() {
		n = 600
	}
	parsed, twice, dropped, textDep := 0, 0, 0, 0
	var ex []string
	for _, seed := range []int64{8787, 1, 2, 3, 4, 5} {
		r := rand.New(rand.NewSource(seed))
		for i := 0; i < n; i++ {
			orig := commentShapeInput(r)
			in, _ := numberComments(orig)
			var y yv3.Node
			var c, co compat.Node
			if yv3.Unmarshal([]byte(in), &y) != nil || compat.Unmarshal([]byte(in), &c) != nil || compat.Unmarshal([]byte(orig), &co) != nil {
				continue
			}
			parsed++
			var got, want []int
			commentIDs(&c, &got)
			yv3IDs(&y, &want)
			seen := map[int]int{}
			for _, id := range got {
				seen[id]++
			}
			dup, lost := false, false
			for _, c := range seen {
				if c > 1 {
					dup = true
				}
			}
			for _, id := range want {
				if seen[id] == 0 {
					lost = true
				}
			}
			dep := compatCommentLines(&co) != compatCommentLines(&c)
			if dup {
				twice++
			}
			if lost {
				dropped++
			}
			if dep {
				textDep++
			}
			if dup && len(ex) < 5 {
				ex = append(ex, fmt.Sprintf("%q twice=%v text-dependent=%v", in, dup, dep))
			}
		}
	}
	t.Logf("inputs=%d read-twice=%d dropped-vs-yaml.v3=%d text-dependent=%d", parsed, twice, dropped, textDep)
	for _, e := range ex {
		t.Logf("example: %s", e)
	}
	if !testing.Short() && (twice > numberedTwiceCeiling || dropped > numberedDroppedCeiling || textDep > numberedTextCeiling) {
		t.Errorf("numbered comments: read twice %d (max %d), dropped %d (max %d), text-dependent %d (max %d)", twice, numberedTwiceCeiling, dropped, numberedDroppedCeiling, textDep, numberedTextCeiling)
	}
}

// TestCompatV3RepeatedCommentTextKeepsEveryLine is the shape where the same
// words stand on three lines of one stretch: none of them may be lost.
func TestCompatV3RepeatedCommentTextKeepsEveryLine(t *testing.T) {
	for _, in := range []string{
		"k0:\n  # c\n  - # c\n    - # c\n      k0: v\n",
		"k0:\n  #\n  - #\n    - #\n      k0: v\n",
	} {
		var y yv3.Node
		var c compat.Node
		if err := yv3.Unmarshal([]byte(in), &y); err != nil {
			t.Fatal(err)
		}
		if err := compat.Unmarshal([]byte(in), &c); err != nil {
			t.Fatal(err)
		}
		if got, want := compatCommentLines(&c), strings.Count(in, "#"); got != want {
			t.Errorf("%q: the shim reads %d comment lines, the text has %d", in, got, want)
		}
		if a, b := dumpV3(&y, nodeFields{comments: true}), dumpCompat(&c, nodeFields{comments: true}); a != b {
			t.Errorf("%q: read differs from yaml.v3:\n%s", in, firstLineDiff(a, b))
		}
		yb, _ := yv3.Marshal(&y)
		cb, err := compat.Marshal(toCompat(&y, map[*yv3.Node]*compat.Node{}))
		if err != nil {
			t.Fatal(err)
		}
		if string(yb) != string(cb) {
			t.Errorf("%q: write differs:\nyaml.v3: %q\nshim:    %q", in, yb, cb)
		}
	}
}
