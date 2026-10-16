package oneyaml

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/pointer"
)

// annotateAgrees applies rename and comment edits to entries of data, once with the
// region path and once with the whole stream parsed, and reports the first
// difference in the text or in the refusal. The stream must also be what a fresh
// parse of its text gives.
func annotateAgrees(data []byte, r *rand.Rand) error {
	d0, err := Parse(data)
	if err != nil {
		return nil
	}
	entries := probeEntries(d0)
	if len(entries) == 0 {
		return nil
	}
	for n := 0; n < 4; n++ {
		e := entries[r.Intn(len(entries))]
		if e.doc != 0 {
			continue
		}
		ops := annotOps(e.ptr)
		op := ops[r.Intn(len(ops))]
		var out [2]string
		var msg [2]string
		for k, off := range []bool{true, false} {
			regionOff = off
			d, err := Parse(data)
			if err != nil {
				regionOff = false
				return err
			}
			if err := op.run(d); err != nil {
				msg[k] = err.Error()
			}
			if err := sameAsFreshParse(d); err != nil {
				regionOff = false
				return fmt.Errorf("%s %q: %v", op.name, e.ptr, err)
			}
			out[k] = string(d.Bytes())
			regionOff = false
		}
		if out[0] != out[1] || msg[0] != msg[1] {
			return fmt.Errorf("%s %q: whole parse gives %q (%q), region gives %q (%q)", op.name, e.ptr, out[0], msg[0], out[1], msg[1])
		}
	}
	return nil
}

// loopAgrees sets the value of each entry of the root collection while ranging over
// it, and again after collecting the keys; the two must give the same text.
func loopAgrees(data []byte, r *rand.Rand) error {
	d0, err := Parse(data)
	if err != nil || d0.Len() != 1 {
		return nil
	}
	root := d0.Root()
	if k := root.Kind(); k != MappingNode && k != SequenceNode {
		return nil
	}
	vals := []any{"changed value", "two\nlines", []any{1, 2}, map[string]any{"a": 1}, 7, nil}
	v := vals[r.Intn(len(vals))]
	loop, _ := Parse(data)
	var ptrs []string
	i := 0
	if loop.Root().Kind() == MappingNode {
		for k := range loop.Root().Entries() {
			if i++; i > 40 {
				break
			}
			p := "/" + pointer.Escape(k.Value())
			ptrs = append(ptrs, p)
			_ = loop.Set(p, v)
		}
	} else {
		for range loop.Root().Items() {
			if i++; i > 40 {
				break
			}
			p := fmt.Sprintf("/%d", i-1)
			ptrs = append(ptrs, p)
			_ = loop.Set(p, v)
		}
	}
	// A loop that notes the keys first is the reference. The loop reads the keys from
	// the tree as edits change it, so the two agree only when every edit keeps the
	// entries where they were, which a replacement does.
	ref, _ := Parse(data)
	for _, p := range ptrs {
		_ = ref.Set(p, v)
	}
	if !bytes.Equal(loop.Bytes(), ref.Bytes()) {
		return fmt.Errorf("a loop that sets %v gives %q, collect-then-set gives %q", v, loop.Bytes(), ref.Bytes())
	}
	return sameAsFreshParse(loop)
}

// batchAgrees removes a random subset of the keys of the root mapping with a patch,
// one at a time, in one pass over the tree held twice, and in one pass checked by
// digest. A pass may decline, but when it succeeds it gives the text of the removals
// one by one, and it never succeeds where they fail.
func batchAgrees(data []byte, r *rand.Rand) error {
	d0, err := Parse(data)
	if err != nil || d0.Len() != 1 || d0.Root().Kind() != MappingNode {
		return nil
	}
	var keys []string
	for k := range d0.Root().Entries() {
		keys = append(keys, k.Value())
		if len(keys) >= 24 {
			break
		}
	}
	var pick []string
	for _, k := range keys {
		if r.Intn(2) == 0 {
			pick = append(pick, k)
		}
	}
	if len(pick) == 0 {
		return nil
	}
	var ops []string
	for _, k := range pick {
		ops = append(ops, fmt.Sprintf(`{"op":"remove","path":"/%s"}`, pointer.Escape(k)))
	}
	patch := []byte("[" + strings.Join(ops, ",") + "]")
	oldMin, oldExact := batchMin, batchExactBelow
	defer func() { batchMin, batchExactBelow = oldMin, oldExact }()
	run := func(min, exact int) (*Document, error) {
		batchMin, batchExactBelow = min, exact
		d, err := Parse(data)
		if err != nil {
			return nil, err
		}
		return d, d.ApplyPatch(patch)
	}
	one, errOne := run(1<<30, 1<<30)
	var first *Document
	for _, exact := range []int{1 << 30, 0} {
		all, errAll := run(1, exact)
		if errAll != nil && errOne == nil {
			return fmt.Errorf("one by one succeeded, the pass (exact below %d) failed: %v; patch %s", exact, errAll, patch)
		}
		// Named mode: a line of blanks that a removal leaves is kept when the entries
		// go one by one and dropped by the pass; the pass is compared with one by one
		// without such lines, and with the other check of the pass byte for byte.
		if errOne == nil && !bytes.Equal(dropBlankLines(one.Bytes()), dropBlankLines(all.Bytes())) {
			return fmt.Errorf("pass (exact below %d) gives %q, one by one %q; patch %s", exact, all.Bytes(), one.Bytes(), patch)
		}
		if first != nil && !bytes.Equal(first.Bytes(), all.Bytes()) {
			return fmt.Errorf("the digest pass gives %q, the pass that holds two trees %q; patch %s", all.Bytes(), first.Bytes(), patch)
		}
		first = all
		if err := sameAsFreshParse(all); err != nil {
			return err
		}
	}
	return nil
}

// dropBlankLines removes the lines that hold blanks and nothing else. A bare CR
// ends a line as LF does, so "\r  \n" is a line break, a blank line and a line break.
func dropBlankLines(b []byte) []byte {
	var out []byte
	start := 0
	for start < len(b) {
		end := start
		for end < len(b) && b[end] != '\n' && b[end] != '\r' {
			end++
		}
		brk := end
		if brk < len(b) {
			brk++
			if b[brk-1] == '\r' && brk < len(b) && b[brk] == '\n' {
				brk++
			}
		}
		if t := b[start:end]; len(t) > 0 && len(bytes.Trim(t, " \t")) == 0 {
			// Blank line: keep its break out too, so the lines around it join as before.
			start = brk
			continue
		}
		out = append(out, b[start:brk]...)
		start = brk
	}
	return out
}

// editPathsAgree runs the three checks above on one input.
func editPathsAgree(data []byte, seed int64) error {
	r := rand.New(rand.NewSource(seed))
	if err := annotateAgrees(data, r); err != nil {
		return fmt.Errorf("annotate: %w", err)
	}
	if err := loopAgrees(data, r); err != nil {
		return fmt.Errorf("loop: %w", err)
	}
	if err := batchAgrees(data, r); err != nil {
		return fmt.Errorf("batch: %w", err)
	}
	return nil
}

// The kit inputs and the probe's own, through the checks the fuzzer makes.
func TestEditPathsAgreeOnSeeds(t *testing.T) {
	seeds := append([][]byte(nil), kitSeeds(t)...)
	for _, s := range propSeeds {
		seeds = append(seeds, []byte(s))
	}
	seeds = append(seeds, annotWideMap(3000), annotRecords(3000))
	rounds := 3
	if testing.Short() {
		rounds = 1
	}
	for i, s := range seeds {
		for k := 0; k < rounds; k++ {
			if err := editPathsAgree(s, int64(i*10+k)); err != nil {
				t.Fatalf("seed %d (%q): %v", i, s, err)
			}
		}
	}
}
