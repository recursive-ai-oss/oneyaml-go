package patch

import (
	"errors"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
)

// ErrBudget means alias expansion went past the allowed number of nodes.
var ErrBudget = errors.New("alias expansion budget exceeded")

// hardCap bounds alias expansion when the caller sets no bound, so that a
// self-referencing anchor cannot loop forever.
const hardCap = 1 << 24

// Equal reports whether node n of the document in c reads as v under the data
// model of the Core schema:
//   - aliases are followed;
//   - mappings are compared without regard to member order;
//   - 1 and 1.0 differ, and 0x1F equals 31;
//   - a quoted scalar is a string whatever it looks like, and an explicit core tag
//     (!!str, !!int, !!float, !!bool, !!null) decides the type of the scalar it is on.
//
// maxNodes bounds the nodes visited below aliases (0 means a large fixed bound).
func Equal(c *edit.Ctx, n int32, v *edit.Value, maxNodes int) (bool, error) {
	if maxNodes <= 0 {
		maxNodes = hardCap
	}
	type pair struct {
		n     int32
		v     *edit.Value
		alias bool // reached through an alias
	}
	t, src := c.T, c.Src
	var scratch []byte
	work := []pair{{n, v, false}}
	visited := 0
	for len(work) > 0 {
		p := work[len(work)-1]
		work = work[:len(work)-1]
		cur := p.n
		for hops := 0; t.Nodes[cur].Kind == cst.Alias; hops++ {
			a := c.AnchorOf(cur)
			if a == 0 {
				return false, nil
			}
			cur = a
			p.alias = true
			if hops > maxNodes {
				return false, ErrBudget
			}
		}
		if p.alias {
			visited++
			if visited > maxNodes {
				return false, ErrBudget
			}
		}
		nd := &t.Nodes[cur]
		switch nd.Kind {
		case cst.Scalar:
			scratch = edit.DecodeScalar(scratch[:0], t, src, cur)
			k := edit.ScalarKind(t, src, cur, scratch)
			if k != p.v.Kind {
				return false, nil
			}
			switch k {
			case edit.String:
				if string(scratch) != p.v.Text {
					return false, nil
				}
			case edit.Bool, edit.Int, edit.Float:
				if !edit.SameNumber(scratch, []byte(p.v.Text), k) {
					return false, nil
				}
			}
		case cst.Sequence:
			if p.v.Kind != edit.Seq || t.Count(cur) != len(p.v.Items) {
				return false, nil
			}
			i := 0
			for ch := t.First(cur); ch != 0; ch = t.Nodes[ch].Next {
				work = append(work, pair{ch, &p.v.Items[i], p.alias})
				i++
			}
		case cst.Mapping:
			if p.v.Kind != edit.Map || t.Count(cur)/2 != len(p.v.Items)/2 {
				return false, nil
			}
			m := len(p.v.Items) / 2
			var index map[string]int
			if m > 8 {
				index = make(map[string]int, m)
				for j := 0; j < m; j++ {
					index[p.v.Items[2*j].Text] = j
				}
			}
			used := make([]bool, m)
			for k := t.First(cur); k != 0; {
				val := t.Nodes[k].Next
				kn := k
				for hops := 0; t.Nodes[kn].Kind == cst.Alias; hops++ {
					a := c.AnchorOf(kn)
					if a == 0 || hops > maxNodes {
						return false, nil
					}
					kn = a
				}
				if t.Nodes[kn].Kind != cst.Scalar {
					return false, nil
				}
				scratch = edit.DecodeScalar(scratch[:0], t, src, kn)
				if edit.ScalarKind(t, src, kn, scratch) != edit.String {
					return false, nil
				}
				j := -1
				if index != nil {
					if x, ok := index[string(scratch)]; ok {
						j = x
					}
				} else {
					for x := 0; x < m; x++ {
						if p.v.Items[2*x].Text == string(scratch) {
							j = x
							break
						}
					}
				}
				if j < 0 || used[j] {
					return false, nil
				}
				used[j] = true
				work = append(work, pair{val, &p.v.Items[2*j+1], p.alias})
				k = t.Nodes[val].Next
			}
		}
	}
	return true, nil
}
