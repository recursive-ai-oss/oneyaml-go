package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	goccy "github.com/goccy/go-yaml"
	goccyparser "github.com/goccy/go-yaml/parser"
	yamlv3 "go.yaml.in/yaml/v3"
	oneyaml "oneyaml.dev/go"
)

// Document benchmarks: parse a lossless document, write it back unchanged, and
// apply a small edit script. The competitors are the node-tree APIs of
// yaml.v3 (yaml.Node) and goccy (the AST). Workloads come from the kit checkout
// named by ONEYAML_KIT, plus a small built-in one.

type docWorkload struct {
	name string
	data []byte
	// paths are the places the edit script replaces (tokens of a pointer).
	paths [][]string
}

const referenceDoc = "# deploy settings\nname: api\nreplicas: 2   # raised by the release job\nscript: |\n  make build\n"

var simpleToken = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func loadDocWorkloads(tb testing.TB) []docWorkload {
	out := []docWorkload{{name: "reference-a", data: []byte(referenceDoc), paths: [][]string{{"replicas"}, {"name"}}}}
	root := os.Getenv("ONEYAML_KIT")
	if root == "" {
		return out
	}
	dirs, _ := filepath.Glob(filepath.Join(root, "bench", "workloads", "real", "*", "input.yaml"))
	for _, f := range dirs {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		w := docWorkload{name: filepath.Base(filepath.Dir(f)), data: data}
		doc, err := oneyaml.Parse(data)
		if err != nil || doc.Len() != 1 {
			continue // multi-document or unreadable here
		}
		var leaves [][]string
		collectLeaves(doc.Root(), nil, &leaves)
		if len(leaves) < 3 {
			continue
		}
		w.paths = [][]string{leaves[len(leaves)/3], leaves[2*len(leaves)/3]}
		out = append(out, w)
	}
	return out
}

// collectLeaves finds plain scalars reachable through simple keys and indexes only.
func collectLeaves(n oneyaml.Node, path []string, out *[][]string) {
	if len(*out) > 400 {
		return
	}
	switch n.Kind() {
	case oneyaml.ScalarNode:
		if n.Style() == oneyaml.PlainStyle && len(path) >= 2 && n.Value() != "" {
			*out = append(*out, append([]string(nil), path...))
		}
	case oneyaml.MappingNode:
		for i := 0; i < n.Len(); i++ {
			k, v := n.Entry(i)
			if k.Kind() == oneyaml.ScalarNode && simpleToken.MatchString(k.Value()) {
				collectLeaves(v, append(path, k.Value()), out)
			}
		}
	case oneyaml.SequenceNode:
		for i := 0; i < n.Len(); i++ {
			collectLeaves(n.Item(i), append(path, fmt.Sprint(i)), out)
		}
	}
}

func pointerOf(toks []string) string { return "/" + strings.Join(toks, "/") }

func goccyPath(toks []string) string {
	var b strings.Builder
	b.WriteString("$")
	for _, t := range toks {
		if t[0] >= '0' && t[0] <= '9' {
			b.WriteString("[" + t + "]")
		} else {
			b.WriteString("." + t)
		}
	}
	return b.String()
}

// v3Find walks a yaml.Node document by tokens.
func v3Find(n *yamlv3.Node, toks []string) *yamlv3.Node {
	if n.Kind == yamlv3.DocumentNode {
		n = n.Content[0]
	}
	for _, t := range toks {
		switch n.Kind {
		case yamlv3.MappingNode:
			var next *yamlv3.Node
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == t {
					next = n.Content[i+1]
					break
				}
			}
			if next == nil {
				return nil
			}
			n = next
		case yamlv3.SequenceNode:
			var i int
			fmt.Sscan(t, &i)
			if i >= len(n.Content) {
				return nil
			}
			n = n.Content[i]
		default:
			return nil
		}
		if n.Kind == yamlv3.AliasNode {
			n = n.Alias
		}
	}
	return n
}

var sink []byte

func BenchmarkDocumentParse(b *testing.B) {
	for _, w := range loadDocWorkloads(b) {
		b.Run(w.name+"/oneyaml", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(w.data)))
			for b.Loop() {
				if _, err := oneyaml.Parse(w.data); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(w.name+"/yaml.v3", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(w.data)))
			for b.Loop() {
				var n yamlv3.Node
				if err := yamlv3.Unmarshal(w.data, &n); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(w.name+"/goccy", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(w.data)))
			for b.Loop() {
				if _, err := goccyparser.ParseBytes(w.data, goccyparser.ParseComments); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkUnchangedEmit(b *testing.B) {
	for _, w := range loadDocWorkloads(b) {
		b.Run(w.name+"/oneyaml", func(b *testing.B) {
			doc, err := oneyaml.Parse(w.data)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(w.data)))
			for b.Loop() {
				sink = doc.Bytes()
			}
		})
		b.Run(w.name+"/yaml.v3", func(b *testing.B) {
			var n yamlv3.Node
			if err := yamlv3.Unmarshal(w.data, &n); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(w.data)))
			for b.Loop() {
				out, err := yamlv3.Marshal(&n)
				if err != nil {
					b.Fatal(err)
				}
				sink = out
			}
		})
		b.Run(w.name+"/goccy", func(b *testing.B) {
			f, err := goccyparser.ParseBytes(w.data, goccyparser.ParseComments)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(w.data)))
			for b.Loop() {
				sink = []byte(f.String())
			}
		})
	}
}

// BenchmarkEditScript replaces two scalar values per iteration and writes the
// document. The new values alternate so that every edit changes the text.
func BenchmarkEditScript(b *testing.B) {
	vals := [2]string{"edited-one", "edited-two"}
	for _, w := range loadDocWorkloads(b) {
		b.Run(w.name+"/oneyaml", func(b *testing.B) {
			doc, err := oneyaml.Parse(w.data)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				v := vals[i&1]
				i++
				for _, p := range w.paths {
					if err := doc.Set(pointerOf(p), v); err != nil {
						b.Fatal(err)
					}
				}
				sink = doc.Bytes()
			}
		})
		b.Run(w.name+"/yaml.v3", func(b *testing.B) {
			var n yamlv3.Node
			if err := yamlv3.Unmarshal(w.data, &n); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				v := vals[i&1]
				i++
				for _, p := range w.paths {
					t := v3Find(&n, p)
					if t == nil {
						b.Fatal("path not found")
					}
					t.Value, t.Tag, t.Style = v, "!!str", 0
				}
				out, err := yamlv3.Marshal(&n)
				if err != nil {
					b.Fatal(err)
				}
				sink = out
			}
		})
		b.Run(w.name+"/goccy", func(b *testing.B) {
			f, err := goccyparser.ParseBytes(w.data, goccyparser.ParseComments)
			if err != nil {
				b.Fatal(err)
			}
			var paths []*goccy.Path
			for _, p := range w.paths {
				gp, err := goccy.PathString(goccyPath(p))
				if err != nil {
					b.Fatal(err)
				}
				paths = append(paths, gp)
			}
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				v := vals[i&1]
				i++
				for _, gp := range paths {
					if err := gp.ReplaceWithReader(f, strings.NewReader(v)); err != nil {
						b.Fatal(err)
					}
				}
				sink = []byte(f.String())
			}
		})
	}
}

// BenchmarkEditAddRemove measures an add and a remove at the end of the root
// mapping or sequence; no competitor offers these on its lossless tree.
func BenchmarkEditAddRemove(b *testing.B) {
	for _, w := range loadDocWorkloads(b) {
		b.Run(w.name+"/oneyaml", func(b *testing.B) {
			doc, err := oneyaml.Parse(w.data)
			if err != nil {
				b.Fatal(err)
			}
			add := "/zz-added"
			if doc.Root().Kind() == oneyaml.SequenceNode {
				add = "/-"
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := doc.Set(add, 1); err != nil {
					b.Fatal(err)
				}
				last := "/zz-added"
				if add == "/-" {
					last = fmt.Sprintf("/%d", doc.Root().Len()-1)
				}
				if err := doc.Remove(last); err != nil {
					b.Fatal(err)
				}
			}
			if string(doc.Bytes()) != string(w.data) {
				b.Fatal("add then remove did not restore the text")
			}
		})
	}
}
