package oneyaml

import (
	"fmt"

	"oneyaml.dev/go/internal/cst"
	"oneyaml.dev/go/internal/edit"
	"oneyaml.dev/go/internal/pointer"
)

// explainEdit re-plans an edit and describes what each attempt wrote, for
// diagnosing an edit the pipeline rejected.
func explainEdit(d *Document, doc int, op editKindOp, ptr string, v *edit.Value) string {
	s := d.s
	toks, _ := pointer.Parse(ptr)
	out := ""
	for attempt := 0; attempt < 2; attempt++ {
		c := edit.NewCtx(&s.tree, s.src, doc)
		pl, err := s.plan(c, op, toks, v, edit.Options{Safe: attempt == 1})
		repls, check := pl.repls, pl.check
		if err != nil {
			out += fmt.Sprintf("attempt %d: plan error %v\n", attempt, err)
			continue
		}
		res, err := edit.Apply(nil, s.src, append([]edit.Repl(nil), repls...))
		if err != nil {
			out += fmt.Sprintf("attempt %d: apply error %v\n", attempt, err)
			continue
		}
		var nt cst.Tree
		berr := cst.Build(&nt, res, -1)
		ok := berr == nil && check(&nt, res, nil)
		out += fmt.Sprintf("attempt %d: repls %+v\n  result %q\n  parse err %v, readback ok %v\n", attempt, reprs(repls), res, berr, ok)
	}
	return out
}

func reprs(rs []edit.Repl) []string {
	var o []string
	for _, r := range rs {
		o = append(o, fmt.Sprintf("[%d,%d)=%q", r.Start, r.End, r.Text))
	}
	return o
}
