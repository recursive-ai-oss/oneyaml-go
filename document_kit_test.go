package oneyaml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/kitcase"
)

// kitRoot returns the kit checkout named by ONEYAML_KIT (the directory that holds
// suites/), or skips the test.
func kitRoot(t testing.TB) string {
	dir := os.Getenv("ONEYAML_KIT")
	if dir == "" {
		t.Skip("ONEYAML_KIT is not set")
	}
	return dir
}

type kitCase struct {
	ID string `json:"id"`
	kitcase.Meta
	Operation struct {
		Type  string          `json:"type"`
		Patch json.RawMessage `json:"patch"`
		// PatchRecipe stands in for Patch when the patch is too large to store.
		PatchRecipe json.RawMessage `json:"patch_recipe"`
		Format      string          `json:"format"`
		Document    int             `json:"document"`
	} `json:"operation"`
	Expect struct {
		Kind string `json:"kind"`
		kitcase.Source
		Category  string `json:"category"`
		Unchanged bool   `json:"unchanged"`
	} `json:"expect"`
}

type kitOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	From  string          `json:"from"`
	Value json.RawMessage `json:"value"`
}

// orderedValue decodes JSON into an edit.Value, keeping member order.
func orderedValue(raw []byte) (edit.Value, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return readValue(dec)
}

func readValue(dec *json.Decoder) (edit.Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return edit.Value{}, err
	}
	switch t := tok.(type) {
	case nil:
		return edit.Value{Kind: edit.Null, Text: "null"}, nil
	case bool:
		return edit.Value{Kind: edit.Bool, Text: fmt.Sprint(t)}, nil
	case string:
		return edit.Str(t), nil
	case json.Number:
		k := edit.Int
		if strings.ContainsAny(string(t), ".eE") {
			k = edit.Float
		}
		return edit.Value{Kind: k, Text: string(t)}, nil
	case json.Delim:
		v := edit.Value{Kind: edit.Seq}
		if t == '{' {
			v.Kind = edit.Map
		}
		for dec.More() {
			if v.Kind == edit.Map {
				k, err := dec.Token()
				if err != nil {
					return v, err
				}
				v.Items = append(v.Items, edit.Str(k.(string)))
			}
			c, err := readValue(dec)
			if err != nil {
				return v, err
			}
			v.Items = append(v.Items, c)
		}
		_, err := dec.Token()
		return v, err
	}
	return edit.Value{}, fmt.Errorf("unexpected token %v", tok)
}

func errorCategory(err error) string {
	switch {
	case errors.Is(err, ErrAliasWrite), errors.Is(err, ErrAnchorInUse):
		return "refused-alias"
	case errors.Is(err, ErrAmbiguousKey):
		return "refused-ambiguous"
	case errors.Is(err, ErrTestFailed):
		return "patch-test"
	case errors.Is(err, ErrInvalidPointer), errors.Is(err, ErrInvalidPatch), errors.Is(err, io.EOF):
		return "patch-invalid"
	case errors.Is(err, ErrNotFound):
		return "patch-path"
	}
	return "other"
}

var opKinds = map[string]editKindOp{"add": opAdd, "replace": opReplace, "remove": opRemove}

// applyKitOps applies the operations in order on document doc, all or nothing,
// and runs the read-back oracle after each step.
func applyKitOps(d *Document, doc int, ops []kitOp) error {
	snap := d.s.snapshot()
	for _, o := range ops {
		kind := opKinds[o.Op]
		var v *edit.Value
		if kind != opRemove {
			val, err := orderedValue(o.Value)
			if err != nil {
				d.s.restore(snap)
				return err
			}
			v = &val
		}
		before, err := streamValues(d)
		if err != nil {
			return err
		}
		if err := d.s.edit(doc, kind, o.Path, v); err != nil {
			d.s.restore(snap)
			return err
		}
		if err := checkReadBack(before, d, doc, kind, o.Path, v); err != nil {
			d.s.restore(snap)
			return fmt.Errorf("oracle: %s %s: %w", o.Op, o.Path, err)
		}
	}
	return nil
}

func TestKitEditCases(t *testing.T) {
	root := kitRoot(t)
	base := filepath.Join(root, "suites", "own", "cases", "edit")
	dirs, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	var pass, skip int
	var failed, skipped []string
	for _, de := range dirs {
		dir := filepath.Join(base, de.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "case.json"))
		if err != nil {
			continue
		}
		var kc kitCase
		if err := json.Unmarshal(raw, &kc); err != nil {
			t.Fatalf("%s: %v", de.Name(), err)
		}
		if kc.Skip() {
			skip++
			continue
		}
		// A case without a patch is a broken case, never an empty patch.
		patch, err := kitcase.PatchBytes(kc.Operation.Patch, kc.Operation.PatchRecipe, kc.Operation.Format)
		if err != nil {
			t.Fatalf("%s: %v", de.Name(), err)
		}
		merge := kc.Operation.Format == "merge-patch"
		var ops []kitOp
		basic := !merge
		if !merge {
			if err := json.Unmarshal(patch, &ops); err != nil {
				// Not an array of operations: the library must say so.
				ops = nil
				basic = false
			}
			for _, o := range ops {
				if _, ok := opKinds[o.Op]; !ok {
					basic = false
				}
			}
		}
		input, err := kc.Bytes(dir)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Parse(input)
		if err != nil {
			failed = append(failed, de.Name()+": parse: "+err.Error())
			continue
		}
		var opErr error
		switch {
		case merge:
			opErr = d.Doc(kc.Operation.Document).ApplyMergePatch(patch)
		case basic:
			// add, remove and replace also run through the read-back oracle, and
			// the public API must agree with it.
			d2, _ := Parse(input)
			opErr = applyKitOps(d, kc.Operation.Document, ops)
			pubErr := d2.Doc(kc.Operation.Document).ApplyPatch(patch)
			if (opErr == nil) != (pubErr == nil) || !bytes.Equal(d.Bytes(), d2.Bytes()) {
				failed = append(failed, fmt.Sprintf("%s: ApplyPatch disagrees with the oracle path: %v / %v", de.Name(), pubErr, opErr))
				continue
			}
		default:
			opErr = d.Doc(kc.Operation.Document).ApplyPatch(patch)
		}
		var problem string
		switch kc.Expect.Kind {
		case "bytes":
			want, wantErr := kc.Expect.Source.Read(dir, false)
			switch {
			case wantErr != nil:
				problem = "bad expectation: " + wantErr.Error()
			case opErr != nil:
				problem = "unexpected error: " + opErr.Error()
			case !bytes.Equal(d.Bytes(), want):
				problem = fmt.Sprintf("bytes differ\n got: %q\nwant: %q", d.Bytes(), want)
			}
		case "error":
			switch {
			case opErr == nil:
				problem = "expected an error"
			case kc.Expect.Category != "" && kc.Expect.Category != "any" && errorCategory(opErr) != kc.Expect.Category:
				problem = fmt.Sprintf("error category %s, want %s (%v)", errorCategory(opErr), kc.Expect.Category, opErr)
			case kc.Expect.Unchanged && !bytes.Equal(d.Bytes(), input):
				problem = "document changed after the error"
			}
		default:
			skip++
			skipped = append(skipped, de.Name())
			continue
		}
		if problem != "" {
			failed = append(failed, de.Name()+": "+problem)
			continue
		}
		pass++
	}
	sort.Strings(skipped)
	t.Logf("kit edit cases: %d passed, %d failed, %d skipped", pass, len(failed), skip)
	t.Logf("skipped: %s", strings.Join(skipped, ", "))
	for _, f := range failed {
		t.Errorf("%s", f)
	}
}

// TestRoundTripInputs checks Parse followed by Bytes for every kit round trip and
// edit input and every valid yaml-test-suite input.
func TestRoundTripInputs(t *testing.T) {
	var files []string
	if root := os.Getenv("ONEYAML_KIT"); root != "" {
		for _, cat := range []string{"roundtrip", "edit"} {
			m, _ := filepath.Glob(filepath.Join(root, "suites", "own", "cases", cat, "*", "input*"))
			files = append(files, m...)
			m, _ = filepath.Glob(filepath.Join(root, "suites", "own", "cases", cat, "*", "expected*"))
			files = append(files, m...)
		}
	}
	kitN := len(files)
	if dir := os.Getenv("ONEYAML_YTS_DIR"); dir != "" {
		m, _ := filepath.Glob(filepath.Join(dir, "*", "in.yaml"))
		n, _ := filepath.Glob(filepath.Join(dir, "*", "*", "in.yaml"))
		files = append(files, m...)
		files = append(files, n...)
	}
	if len(files) == 0 {
		t.Skip("ONEYAML_KIT and ONEYAML_YTS_DIR are not set")
	}
	var ok, rejected, mismatch, suiteValid, suiteInvalid int
	for i, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		// A suite input is expected to be rejected when its case has an "error" file.
		invalid := false
		if i >= kitN {
			if _, err := os.Stat(filepath.Join(filepath.Dir(f), "error")); err == nil {
				invalid = true
			}
		}
		d, err := Parse(data)
		if err != nil {
			rejected++
			switch {
			case invalid:
				suiteInvalid++
			default:
				t.Errorf("%s: %v", f, err)
			}
			continue
		}
		if invalid {
			// The scanner accepted an input the suite marks invalid; that is the
			// parser's conformance to judge, not a round trip problem.
			continue
		}
		if !bytes.Equal(d.Bytes(), data) {
			mismatch++
			t.Errorf("%s: Bytes differs from the input", f)
			continue
		}
		ok++
		if i >= kitN {
			suiteValid++
		}
	}
	t.Logf("round trip: %d identical (%d of them valid suite inputs), %d rejected (all invalid suite inputs), %d differ; kit files %d, suite inputs %d", ok, suiteValid, rejected, mismatch, kitN, len(files)-kitN)
}
