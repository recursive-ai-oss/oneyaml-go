package oneyaml

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/edit"
)

// genYAML writes a block mapping or sequence with the layouts edits meet: nested
// collections, comments above and after entries, blank lines, block scalars that
// keep or strip their last line breaks, flow collections, quoted scalars,
// anchors and aliases, and entries that share their line with a dash.
func genYAML(r *rand.Rand, b *strings.Builder, indent, depth int, seq bool) {
	pad := strings.Repeat(" ", indent)
	n := 2 + r.Intn(6)
	for i := 0; i < n; i++ {
		switch r.Intn(7) {
		case 0:
			fmt.Fprintf(b, "%s# note %d\n", pad, i)
		case 1:
			if r.Intn(3) == 0 {
				b.WriteString("\n")
			}
		}
		key := fmt.Sprintf("k%d", i)
		lead := pad
		if seq {
			lead = pad + "- "
		} else {
			lead = pad + key + ":"
		}
		nested := func(isSeq bool) {
			if seq {
				// "- " then a mapping on the same line, or a nested sequence below.
				if isSeq {
					b.WriteString(pad + "-\n")
					genYAML(r, b, indent+2, depth-1, true)
					return
				}
				var inner strings.Builder
				genYAML(r, &inner, indent+2, depth-1, false)
				s := inner.String()
				// Put the first entry on the dash's line when it starts with the key.
				if t := strings.TrimLeft(s, " \n"); r.Intn(2) == 0 && strings.HasPrefix(s, strings.Repeat(" ", indent+2)+"k") {
					_ = t
					b.WriteString(pad + "- " + s[indent+2:])
				} else {
					b.WriteString(pad + "-\n" + s)
				}
				return
			}
			b.WriteString(lead + "\n")
			genYAML(r, b, indent+2, depth-1, isSeq)
		}
		switch k := r.Intn(12); {
		case k < 3 || depth <= 0:
			vals := []string{"plain text", "42", "true", "null", "'single'", `"dou\"ble"`, "[1, 2, [3]]", "{a: 1, b: [x]}", "&anc val", "*anc", "!!str 7", "1.5"}
			if seq {
				fmt.Fprintf(b, "%s%s\n", lead, vals[r.Intn(len(vals))])
			} else {
				fmt.Fprintf(b, "%s %s\n", lead, vals[r.Intn(len(vals))])
			}
		case k == 3:
			chomp := []string{"", "-", "+"}[r.Intn(3)]
			style := []string{"|", ">"}[r.Intn(2)]
			if seq {
				fmt.Fprintf(b, "%s%s%s\n", lead, style, chomp)
			} else {
				fmt.Fprintf(b, "%s %s%s\n", lead, style, chomp)
			}
			for j := 0; j < 1+r.Intn(3); j++ {
				fmt.Fprintf(b, "%s  line %d\n", pad, j)
			}
			if r.Intn(2) == 0 {
				b.WriteString("\n")
			}
		case k == 4 || k == 5:
			nested(false)
		case k == 6:
			nested(true)
		case k == 7:
			if seq {
				fmt.Fprintf(b, "%s&a%d {x: 1}\n", lead, i)
			} else {
				fmt.Fprintf(b, "%s &a%d {x: 1}\n", lead, i)
			}
		case k == 8:
			if seq {
				fmt.Fprintf(b, "%s'quoted # not a comment' # but this is\n", lead)
			} else {
				fmt.Fprintf(b, "%s 'quoted # not a comment' # but this is\n", lead)
			}
		case k == 9:
			if !seq {
				fmt.Fprintf(b, "%s\n", lead) // an empty value
			} else {
				fmt.Fprintf(b, "%s\n", strings.TrimRight(lead, " "))
			}
		default:
			if seq {
				fmt.Fprintf(b, "%smulti word\n%s  continued\n", lead, pad)
			} else {
				fmt.Fprintf(b, "%s multi word\n%s  continued\n", lead, pad)
			}
		}
	}
}

// Edits written through a region must give the same text and the same verdict as
// edits that parse the whole stream.
func TestRegionEditsMatchWholeParse(t *testing.T) {
	runs := 400
	if testing.Short() {
		runs = 60
	}
	var diff, regions int
	for run := 0; run < runs; run++ {
		r := rand.New(rand.NewSource(int64(run)))
		var b strings.Builder
		genYAML(r, &b, 0, 3, run%4 == 3)
		text := b.String()
		a, err := Parse([]byte(text))
		if err != nil {
			continue // the generator can write something invalid; the interesting inputs parse
		}
		whole, _ := Parse([]byte(text))
		for step := 0; step < 14; step++ {
			vals, err := streamValues(a)
			if err != nil || len(vals) == 0 {
				break
			}
			var ptrs []string
			pointers(&vals[0], "", &ptrs)
			ptr := ptrs[r.Intn(len(ptrs))]
			switch r.Intn(6) {
			case 0:
				ptr += "/new" + fmt.Sprint(r.Intn(3))
			case 1:
				ptr += "/-"
			}
			op := []editKindOp{opRemove, opAdd, opReplace, opSet, opSet}[r.Intn(5)]
			var v *edit.Value
			if op != opRemove {
				val := randValue(r, 2)
				v = &val
			}
			before := a.s.es().work.regions
			errA := a.s.edit(0, op, ptr, v)
			regions += a.s.es().work.regions - before
			regionOff = true
			errB := whole.s.edit(0, op, ptr, v)
			regionOff = false
			if fmt.Sprint(errA) != fmt.Sprint(errB) || string(a.Bytes()) != string(whole.Bytes()) {
				diff++
				t.Errorf("run %d step %d: %s %q on\n%s\nregion: %v\n%q\nwhole:  %v\n%q", run, step, op.name(), ptr, text, errA, a.Bytes(), errB, whole.Bytes())
				break
			}
		}
	}
	t.Logf("%d edits went through a region; %d differences", regions, diff)
	if regions == 0 {
		t.Error("no edit went through a region")
	}
}

// A text handed out by Bytes is never changed by a later edit, although the stream
// edits its own buffer in place when nobody holds it.
func TestBytesSurvivesLaterEdits(t *testing.T) {
	d := mustParse(t, wideDoc(100))
	for round := 0; round < 3; round++ {
		out := d.Bytes()
		keep := string(out)
		for i := 0; i < 6; i++ {
			if err := d.Set(fmt.Sprintf("/key%d", 30*round+3*i+1), []string{"a", "b: c", "x\ny\n"}[i%3]); err != nil {
				t.Fatal(err)
			}
			if err := d.Set("/added", i); err != nil {
				t.Fatal(err)
			}
			if err := d.Remove("/key" + fmt.Sprint(30*round+3*i)); err != nil {
				t.Fatal(err)
			}
			if err := d.Remove("/added"); err != nil {
				t.Fatal(err)
			}
		}
		if string(out) != keep {
			t.Fatalf("round %d: a text returned by Bytes changed after edits", round)
		}
		if string(d.Bytes()) == keep {
			t.Fatalf("round %d: the edits did nothing", round)
		}
		d = mustParse(t, string(d.Bytes()))
	}
}
