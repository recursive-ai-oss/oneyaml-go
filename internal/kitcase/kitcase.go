// Package kitcase reads the parts of a shared conformance case directory that
// every test runner needs in the same way: the input bytes, which may be
// stored in a file or described by a recipe, and the "slow" tag.
//
// It is used by tests only, but lives in a normal file so that several
// packages' tests can import it.
package kitcase

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// maxRecipe bounds what a recipe may build, so a bad case file cannot exhaust memory.
const maxRecipe = 64 << 20

// Meta is the part of case.json that locates the input.
type Meta struct {
	// Input is the input file name; empty means input.yaml.
	Input string `json:"input"`
	// Recipe describes an input too large to store.
	Recipe json.RawMessage `json:"input_recipe"`
	// Tags are free labels; "slow" marks a case kept out of the fast tier.
	Tags []string `json:"tags"`
}

// Slow reports whether the case is tagged slow.
func (m *Meta) Slow() bool {
	for _, t := range m.Tags {
		if t == "slow" {
			return true
		}
	}
	return false
}

// Skip reports whether the case should be skipped: slow cases run only when
// KIT_SLOW=1.
func (m *Meta) Skip() bool {
	return m.Slow() && os.Getenv("KIT_SLOW") != "1"
}

// Bytes returns the case input: the recipe output, or the input file in dir.
func (m *Meta) Bytes(dir string) ([]byte, error) {
	if len(m.Recipe) > 0 && !bytes.Equal(bytes.TrimSpace(m.Recipe), []byte("null")) {
		return Build(m.Recipe)
	}
	name := m.Input
	if name == "" {
		name = "input.yaml"
	}
	return os.ReadFile(filepath.Join(dir, name))
}

// ReadMeta reads the locating fields of dir/case.json.
func ReadMeta(dir string) (*Meta, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "case.json"))
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	return &m, nil
}

type part struct {
	Text    *string `json:"text"`
	Repeat  *int64  `json:"repeat"`
	Numbers *struct {
		Count  int64  `json:"count"`
		Start  int64  `json:"start"`
		Prefix string `json:"prefix"`
		Suffix string `json:"suffix"`
		Width  int    `json:"width"`
	} `json:"numbers"`
	// Product writes one string from each list of choices, for every
	// combination, the first list varying slowest.
	Product *struct {
		Prefix  string     `json:"prefix"`
		Suffix  string     `json:"suffix"`
		Choices [][]string `json:"choices"`
	} `json:"product"`
}

// Build turns a recipe (a JSON list of parts) into bytes. The size is worked
// out before anything is built.
func Build(recipe json.RawMessage) ([]byte, error) {
	var parts []part
	if err := json.Unmarshal(recipe, &parts); err != nil {
		return nil, fmt.Errorf("recipe: %w", err)
	}
	if len(parts) > 64 {
		return nil, fmt.Errorf("recipe: %d parts, at most 64", len(parts))
	}
	var size int64
	for _, p := range parts {
		switch {
		case p.Product != nil && p.Text == nil && p.Numbers == nil:
			q := p.Product
			if len(q.Choices) == 0 {
				return nil, fmt.Errorf("recipe: a product needs choices")
			}
			// Count the combinations and the exact length before building.
			n := int64(1)
			for _, c := range q.Choices {
				if len(c) == 0 {
					return nil, fmt.Errorf("recipe: an empty list of choices")
				}
				n *= int64(len(c))
				if n > maxRecipe {
					return nil, fmt.Errorf("recipe: too large")
				}
			}
			size += n * int64(len(q.Prefix)+len(q.Suffix))
			for _, c := range q.Choices {
				var sum int64
				for _, x := range c {
					sum += int64(len(x))
				}
				size += n / int64(len(c)) * sum
			}
		case p.Text != nil && p.Numbers == nil && p.Product == nil:
			n := int64(1)
			if p.Repeat != nil {
				n = *p.Repeat
			}
			if n < 0 {
				return nil, fmt.Errorf("recipe: negative repeat")
			}
			if l := int64(len(*p.Text)); l > 0 && n > maxRecipe/l+1 {
				return nil, fmt.Errorf("recipe: too large")
			}
			size += n * int64(len(*p.Text))
		case p.Numbers != nil && p.Text == nil && p.Product == nil:
			c := p.Numbers
			if c.Count < 0 || c.Start < 0 || c.Width < 0 || c.Width > 64 {
				return nil, fmt.Errorf("recipe: bad numbers part")
			}
			if c.Count > maxRecipe {
				return nil, fmt.Errorf("recipe: too large")
			}
			size += c.Count * int64(len(c.Prefix)+len(c.Suffix)+max(c.Width, 1))
		default:
			return nil, fmt.Errorf("recipe: a part has exactly one of text, numbers or product")
		}
		if size > maxRecipe {
			return nil, fmt.Errorf("recipe: builds more than %d bytes", maxRecipe)
		}
	}
	// Numbers can run past the estimate (more digits than the width), so the
	// buffer grows if needed but the limit above already caps the estimate.
	out := make([]byte, 0, size)
	for _, p := range parts {
		if q := p.Product; q != nil {
			idx := make([]int, len(q.Choices))
			for {
				out = append(out, q.Prefix...)
				for i, c := range q.Choices {
					out = append(out, c[idx[i]]...)
				}
				out = append(out, q.Suffix...)
				k := len(idx) - 1
				for ; k >= 0; k-- {
					if idx[k]++; idx[k] < len(q.Choices[k]) {
						break
					}
					idx[k] = 0
				}
				if k < 0 {
					break
				}
			}
			continue
		}
		if p.Text != nil {
			n := int64(1)
			if p.Repeat != nil {
				n = *p.Repeat
			}
			for i := int64(0); i < n; i++ {
				out = append(out, *p.Text...)
			}
			continue
		}
		c := p.Numbers
		for k := c.Start; k < c.Start+c.Count; k++ {
			out = append(out, c.Prefix...)
			d := strconv.FormatInt(k, 10)
			for i := len(d); i < c.Width; i++ {
				out = append(out, '0')
			}
			out = append(out, d...)
			out = append(out, c.Suffix...)
		}
	}
	if len(out) > maxRecipe {
		return nil, fmt.Errorf("recipe: builds more than %d bytes", maxRecipe)
	}
	return out, nil
}
