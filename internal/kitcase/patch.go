package kitcase

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Limits on a generated patch, as the case format states them. The count is
// worked out before anything is built so a bad case file cannot exhaust memory.
const (
	maxPatchParts = 64
	maxPatchOps   = 1_000_000
)

type patchPart struct {
	Ops    []json.RawMessage `json:"ops"`
	Repeat *int64            `json:"repeat"`
}

func present(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && !bytes.Equal(t, []byte("null"))
}

// PatchBytes returns the patch text of a patch operation: the stored patch, or
// the array that patch_recipe builds. A case that has both, or neither, is an
// error: a missing patch must never be read as an empty one. The format is the
// operation's format ("" means json-patch); only a json-patch may be generated.
func PatchBytes(patch, recipe json.RawMessage, format string) ([]byte, error) {
	switch hasPatch, hasRecipe := present(patch), present(recipe); {
	case hasPatch && hasRecipe:
		return nil, fmt.Errorf("operation has both patch and patch_recipe")
	case !hasPatch && !hasRecipe:
		return nil, fmt.Errorf("operation has neither patch nor patch_recipe")
	case hasPatch:
		return patch, nil
	}
	if format != "" && format != "json-patch" {
		return nil, fmt.Errorf("patch_recipe: only a json-patch can be generated, not %q", format)
	}
	var parts []patchPart
	if err := json.Unmarshal(recipe, &parts); err != nil {
		return nil, fmt.Errorf("patch_recipe: %w", err)
	}
	if len(parts) == 0 || len(parts) > maxPatchParts {
		return nil, fmt.Errorf("patch_recipe: %d parts, want 1 to %d", len(parts), maxPatchParts)
	}
	var total int64
	for i, p := range parts {
		if len(p.Ops) == 0 {
			return nil, fmt.Errorf("patch_recipe: part %d has no ops", i)
		}
		for _, o := range p.Ops {
			var probe struct {
				Op *string `json:"op"`
			}
			if json.Unmarshal(o, &probe) != nil || probe.Op == nil {
				return nil, fmt.Errorf("patch_recipe: part %d: operations are objects with a string op", i)
			}
		}
		n := int64(1)
		if p.Repeat != nil {
			n = *p.Repeat
		}
		if n < 0 || n > maxPatchOps {
			return nil, fmt.Errorf("patch_recipe: part %d: bad repeat %d", i, n)
		}
		total += int64(len(p.Ops)) * n
		if total > maxPatchOps {
			return nil, fmt.Errorf("patch_recipe: builds more than %d operations", maxPatchOps)
		}
	}
	var out bytes.Buffer
	out.WriteByte('[')
	first := true
	for _, p := range parts {
		n := int64(1)
		if p.Repeat != nil {
			n = *p.Repeat
		}
		for ; n > 0; n-- {
			for _, o := range p.Ops {
				if !first {
					out.WriteByte(',')
				}
				first = false
				out.Write(o)
			}
		}
	}
	out.WriteByte(']')
	return out.Bytes(), nil
}
