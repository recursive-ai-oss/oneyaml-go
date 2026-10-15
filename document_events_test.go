package oneyaml_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	oneyaml "oneyaml.dev/go"
	"oneyaml.dev/go/internal/kitcase"
)

// eventNotation writes the stream of d in the yaml-test-suite's event notation,
// using only the public API. This is what a test-kit adapter does, and it
// shows that Document and Node expose everything a reader of the stream needs.
func eventNotation(d *oneyaml.Document) []string {
	lines := []string{"+STR"}
	for i := 0; i < d.Len(); i++ {
		v := d.Doc(i)
		l := "+DOC"
		if v.ExplicitStart() {
			l += " ---"
		}
		lines = append(lines, l)
		lines = appendNode(lines, v.Root())
		l = "-DOC"
		if v.ExplicitEnd() {
			l += " ..."
		}
		lines = append(lines, l)
	}
	return append(lines, "-STR")
}

func props(n oneyaml.Node) string {
	s := ""
	if a := n.Anchor(); a != "" {
		s += " &" + a
	}
	if t, ok := n.ExplicitTag(); ok {
		s += " <" + t + ">"
	}
	return s
}

func appendNode(lines []string, n oneyaml.Node) []string {
	switch n.Kind() {
	case oneyaml.AliasNode:
		return append(lines, "=ALI *"+n.AliasName())
	case oneyaml.ScalarNode:
		mark := map[oneyaml.NodeStyle]string{oneyaml.PlainStyle: ":", oneyaml.SingleQuotedStyle: "'",
			oneyaml.DoubleQuotedStyle: "\"", oneyaml.LiteralStyle: "|", oneyaml.FoldedStyle: ">"}[n.Style()]
		return append(lines, "=VAL"+props(n)+" "+mark+escapeNotation(n.Value()))
	case oneyaml.SequenceNode:
		l := "+SEQ"
		if n.Style() == oneyaml.FlowStyle {
			l += " []"
		}
		lines = append(lines, l+props(n))
		for item := range n.Items() {
			lines = appendNode(lines, item)
		}
		return append(lines, "-SEQ")
	case oneyaml.MappingNode:
		l := "+MAP"
		if n.Style() == oneyaml.FlowStyle {
			l += " {}"
		}
		lines = append(lines, l+props(n))
		for k, v := range n.Entries() {
			lines = appendNode(lines, k)
			lines = appendNode(lines, v)
		}
		return append(lines, "-MAP")
	}
	return append(lines, "?invalid")
}

func escapeNotation(s string) string {
	return strings.NewReplacer("\\", `\\`, "\x00", `\0`, "\b", `\b`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(s)
}

type eventCase struct {
	id   string
	in   []byte
	want []string
}

func loadEventCases(t *testing.T) []eventCase {
	var out []eventCase
	if dir := os.Getenv("ONEYAML_YTS_DIR"); dir != "" {
		corr := map[string][]string{}
		path := os.Getenv("ONEYAML_YTS_CORRECTIONS")
		if path == "" {
			path = filepath.Join(dir, "..", "corrections.json")
		}
		if raw, err := os.ReadFile(path); err == nil {
			var doc struct {
				Entries []struct {
					Case   string   `json:"case"`
					Events []string `json:"events"`
				} `json:"entries"`
			}
			if json.Unmarshal(raw, &doc) == nil {
				for _, e := range doc.Entries {
					if e.Events != nil {
						corr[strings.TrimPrefix(e.Case, "yts:")] = e.Events
					}
				}
			}
		}
		add := func(id, d string) {
			in, err := os.ReadFile(filepath.Join(d, "in.yaml"))
			if err != nil {
				return
			}
			if _, err := os.Stat(filepath.Join(d, "error")); err == nil {
				return
			}
			ev, err := os.ReadFile(filepath.Join(d, "test.event"))
			if err != nil {
				return
			}
			want := strings.Split(strings.TrimRight(string(ev), "\n"), "\n")
			if c, ok := corr[id]; ok {
				want = c
			}
			out = append(out, eventCase{"yts:" + id, in, want})
		}
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			d := filepath.Join(dir, e.Name())
			if _, err := os.Stat(filepath.Join(d, "in.yaml")); err == nil {
				add(e.Name(), d)
				continue
			}
			subs, _ := os.ReadDir(d)
			for _, s := range subs {
				if s.IsDir() {
					add(e.Name()+"/"+s.Name(), filepath.Join(d, s.Name()))
				}
			}
		}
	}
	if kit := os.Getenv("ONEYAML_KIT"); kit != "" {
		base := filepath.Join(kit, "suites", "own", "cases")
		cases, _ := filepath.Glob(filepath.Join(base, "*", "*", "case.json"))
		for _, cj := range cases {
			raw, err := os.ReadFile(cj)
			if err != nil {
				continue
			}
			var c struct {
				ID string `json:"id"`
				kitcase.Meta
				Operation struct {
					Type    string `json:"type"`
					Profile string `json:"profile"`
				} `json:"operation"`
				Expect struct {
					Kind string `json:"kind"`
					File string `json:"file"`
				} `json:"expect"`
			}
			if json.Unmarshal(raw, &c) != nil || c.Operation.Type != "events" || c.Expect.Kind != "events" ||
				(c.Operation.Profile != "" && c.Operation.Profile != "core") {
				continue
			}
			if c.Skip() {
				continue
			}
			in, err1 := c.Bytes(filepath.Dir(cj))
			ev, err2 := os.ReadFile(filepath.Join(filepath.Dir(cj), c.Expect.File))
			if err1 != nil || err2 != nil {
				continue
			}
			want := strings.Split(strings.TrimRight(string(ev), "\n"), "\n")
			out = append(out, eventCase{"own:" + c.ID, in, want})
		}
	}
	if len(out) == 0 {
		t.Skip("ONEYAML_YTS_DIR and ONEYAML_KIT are not set")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// Every valid input, read through the public Document and Node API alone,
// gives the event stream the suite expects (with the kit's corrections).
func TestEventNotationFromTheDocumentAPI(t *testing.T) {
	cases := loadEventCases(t)
	pass := 0
	for _, c := range cases {
		d, err := oneyaml.Parse(c.in)
		if err != nil {
			t.Errorf("%s: Parse: %v", c.id, err)
			continue
		}
		got := eventNotation(d)
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s: events differ\n got: %q\nwant: %q", c.id, got, c.want)
			continue
		}
		pass++
	}
	t.Logf("event notation through the public API: %d of %d valid cases", pass, len(cases))
}
