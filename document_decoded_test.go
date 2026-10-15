package oneyaml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"strconv"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
)

// The decoded oracle reads documents through the public decoder, not through the
// edit engine's own tree conversion, so a mistake the engine makes in reading a
// node (a tag it misjudges, a style it misreads) cannot hide in both the edit and
// its check. The model is the decoded value before the edit with the operation
// applied by tree surgery; the check is the decoded value after.

// decodeValues decodes every document of b and returns it as values. ok is false
// when the stream cannot be compared (a decode error, or a key that is not a
// string).
func decodeValues(b []byte) (out []edit.Value, ok bool) {
	dec := NewDecoder(bytes.NewReader(b))
	for {
		var x any
		err := dec.Decode(&x)
		if errors.Is(err, io.EOF) {
			return out, true
		}
		if err != nil {
			return nil, false
		}
		v, ok := anyToValue(x)
		if !ok {
			return nil, false
		}
		out = append(out, v)
	}
}

func anyToValue(x any) (edit.Value, bool) {
	switch t := x.(type) {
	case nil:
		return edit.Value{Kind: edit.Null, Text: "null"}, true
	case bool:
		return edit.Value{Kind: edit.Bool, Text: strconv.FormatBool(t)}, true
	case int:
		return edit.Value{Kind: edit.Int, Text: strconv.Itoa(t)}, true
	case int64:
		return edit.Value{Kind: edit.Int, Text: strconv.FormatInt(t, 10)}, true
	case uint64:
		return edit.Value{Kind: edit.Int, Text: strconv.FormatUint(t, 10)}, true
	case *big.Int:
		return edit.Value{Kind: edit.Int, Text: t.String()}, true
	case float64:
		switch {
		case math.IsNaN(t):
			return edit.Value{Kind: edit.Float, Text: ".nan"}, true
		case math.IsInf(t, 1):
			return edit.Value{Kind: edit.Float, Text: ".inf"}, true
		case math.IsInf(t, -1):
			return edit.Value{Kind: edit.Float, Text: "-.inf"}, true
		}
		return edit.Value{Kind: edit.Float, Text: strconv.FormatFloat(t, 'g', -1, 64)}, true
	case string:
		return edit.Str(t), true
	case []any:
		v := edit.Value{Kind: edit.Seq}
		for _, e := range t {
			c, ok := anyToValue(e)
			if !ok {
				return v, false
			}
			v.Items = append(v.Items, c)
		}
		return v, true
	case map[string]any:
		v := edit.Value{Kind: edit.Map}
		for k, e := range t {
			c, ok := anyToValue(e)
			if !ok {
				return v, false
			}
			v.Items = append(v.Items, edit.Str(k), c)
		}
		return v, true
	}
	return edit.Value{}, false
}

// writtenTags reports whether v carries a tag the decoder reads differently from
// the engine's value (anything but a binary string, which the decoder returns as
// its base64 text).
func untaggable(v *edit.Value) bool {
	if v.Tag != "" && v.Tag != "!!binary" {
		return true
	}
	for i := range v.Items {
		if untaggable(&v.Items[i]) {
			return true
		}
	}
	return false
}

// checkDecoded compares the decoded output with the decoded input plus the edit.
// It returns nil when the stream cannot be compared.
func checkDecoded(in, out []byte, after *Document, doc int, op editKindOp, ptr string, v *edit.Value) error {
	if v != nil && untaggable(v) {
		return nil
	}
	want, ok := decodeValues(in)
	if !ok || doc >= len(want) {
		return nil
	}
	got, ok := decodeValues(out)
	if !ok {
		return fmt.Errorf("output does not decode")
	}
	// Reading the Document itself must give what the decoder gives for its bytes.
	if before, err := Parse(in); err != nil {
		return fmt.Errorf("input does not parse: %v", err)
	} else if err := sameDecoded(before, want); err != nil {
		return fmt.Errorf("input: %v", err)
	}
	if err := sameDecoded(after, got); err != nil {
		return fmt.Errorf("output: %v", err)
	}
	m, err := modelApply(cloneValue(want[doc]), op, ptr, v)
	if err != nil {
		return nil // the model cannot place it (keys or indexes the engine reads differently)
	}
	want[doc] = m
	if len(got) != len(want) {
		return fmt.Errorf("decoded %d documents, want %d", len(got), len(want))
	}
	for i := range got {
		if !modelEqual(got[i], want[i]) {
			return fmt.Errorf("decoded value differs in document %d\n got: %+v\nwant: %+v", i, canon(got[i]), canon(want[i]))
		}
	}
	return nil
}

// sameAsFreshParse reports whether the kept tree equals a parse of the kept text.
func sameAsFreshParse(d *Document) error {
	var fresh cst.Tree
	if err := cst.Build(&fresh, d.s.src, -1); err != nil {
		return fmt.Errorf("text does not parse: %v", err)
	}
	if !reflect.DeepEqual(fresh.Nodes, d.s.tree.Nodes) || !reflect.DeepEqual(fresh.Props, d.s.tree.Props) || !reflect.DeepEqual(fresh.Docs, d.s.tree.Docs) {
		return fmt.Errorf("the kept tree differs from a fresh parse of %q", d.s.src)
	}
	return nil
}

// sameDecoded checks that Document.Decode of every document of d agrees with the
// values a Decoder read from its bytes.
func sameDecoded(d *Document, want []edit.Value) error {
	if d.Len() != len(want) {
		return fmt.Errorf("Document has %d documents, the decoder read %d", d.Len(), len(want))
	}
	for i := range want {
		var x any
		if err := d.Doc(i).Decode(&x); err != nil {
			return fmt.Errorf("Decode of document %d: %v", i, err)
		}
		v, ok := anyToValue(x)
		if !ok || !modelEqual(v, want[i]) {
			return fmt.Errorf("Decode of document %d differs from the Decoder\n doc: %+v\n dec: %+v", i, canon(v), canon(want[i]))
		}
	}
	return nil
}
