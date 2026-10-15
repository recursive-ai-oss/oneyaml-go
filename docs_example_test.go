package oneyaml_test

import (
	"errors"
	"fmt"
	"log"
	"strings"

	oneyaml "oneyaml.dev/go"
)

// Case A: the new value ends in a line break, so the block literal keeps its
// style, its header and its place. Only the line of text changes.
func Example_blockLiteralKeepsStyle() {
	doc, err := oneyaml.Parse([]byte(`#hello
a: 10

#abc 123
b: |
  this is a test
c: 13
`))
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.Set("/b", "this is a test2\n"); err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// #hello
	// a: 10
	//
	// #abc 123
	// b: |
	//   this is a test2
	// c: 13
}

// Case B: the new value has no final line break. Reading the document back must
// give exactly that value, so the header changes to |- and nothing else does.
func Example_blockLiteralStrip() {
	doc, err := oneyaml.Parse([]byte(`#hello
a: 10

#abc 123
b: |
  this is a test
c: 13
`))
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.Set("/b", "this is a test2"); err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))

	var b string
	if err := doc.Get("/b", &b); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%q\n", b)
	// Output:
	// #hello
	// a: 10
	//
	// #abc 123
	// b: |-
	//   this is a test2
	// c: 13
	// "this is a test2"
}

// When the existing style cannot hold the new value, only that value moves to
// the nearest style that can. A plain scalar stays plain when the Core schema
// reads it back as the same string, and becomes quoted when it would not.
func ExampleDocument_Set_styleFallback() {
	doc, err := oneyaml.Parse([]byte("a: plain\nb: plain\nc: plain\nd: \"quoted\"\n"))
	if err != nil {
		log.Fatal(err)
	}
	for _, edit := range []struct {
		ptr string
		v   any
	}{
		{"/a", "yes"},         // Core reads this as a string: it stays plain
		{"/b", "true"},        // Core reads this as a boolean: it is quoted
		{"/c", "two\nlines"},  // a line break needs an escape: double quotes
		{"/d", "now: plain?"}, // a double-quoted scalar stays double-quoted
	} {
		if err := doc.Set(edit.ptr, edit.v); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// a: yes
	// b: 'true'
	// c: "two\nlines"
	// d: "now: plain?"
}

// A new key goes at the end of its mapping and copies the style of its
// siblings. A null that is already there keeps its spelling.
func ExampleDocument_Set_newKey() {
	doc, err := oneyaml.Parse([]byte("# ports\nname: 'api'\nport: 80\nnote: ~\n"))
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.Set("/owner", "ops"); err != nil {
		log.Fatal(err)
	}
	if err := doc.Set("/note", nil); err != nil {
		log.Fatal(err)
	}
	if err := doc.Set("/port", 8080); err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// # ports
	// name: 'api'
	// port: 8080
	// note: ~
	// owner: 'ops'
}

// An edit that would leave an alias without its anchor is refused, and the
// document is unchanged.
func ExampleDocument_Remove_anchorInUse() {
	doc, err := oneyaml.Parse([]byte("base: &b\n  x: 1\ncopy: *b\n"))
	if err != nil {
		log.Fatal(err)
	}
	err = doc.Remove("/base")
	fmt.Println(errors.Is(err, oneyaml.ErrAnchorInUse))
	fmt.Print(string(doc.Bytes()))
	// Output:
	// true
	// base: &b
	//   x: 1
	// copy: *b
}

// An edit that fails returns an *EditError that says what was attempted.
func ExampleEditError() {
	doc, err := oneyaml.Parse([]byte("a: 1\n"))
	if err != nil {
		log.Fatal(err)
	}
	err = doc.Set("/a/b/c", 2)
	var ee *oneyaml.EditError
	if errors.As(err, &ee) {
		fmt.Println(ee.Op, ee.Pointer, errors.Is(err, oneyaml.ErrNotFound))
	}
	// Output:
	// set /a/b/c true
}

// A pointer token matches a key by its text, so /1 addresses the integer key 1.
// Complex keys cannot be addressed.
func ExampleDocument_ApplyPatch_pointerText() {
	doc, err := oneyaml.Parse([]byte("1: one\n\"2\": two\n"))
	if err != nil {
		log.Fatal(err)
	}
	err = doc.ApplyPatch([]byte(`[
		{"op": "replace", "path": "/1", "value": "uno"},
		{"op": "replace", "path": "/2", "value": "dos"}
	]`))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// 1: uno
	// "2": dos
}

// A patch never writes through an alias, and a refused patch changes nothing,
// not even the operations before the refused one.
func ExampleDocument_ApplyPatch_aliasRefused() {
	doc, err := oneyaml.Parse([]byte("base: &b\n  x: 1\ncopy: *b\n"))
	if err != nil {
		log.Fatal(err)
	}
	err = doc.ApplyPatch([]byte(`[
		{"op": "replace", "path": "/base/x", "value": 2},
		{"op": "replace", "path": "/copy/x", "value": 3}
	]`))
	fmt.Println(errors.Is(err, oneyaml.ErrAliasWrite))
	fmt.Print(string(doc.Bytes()))
	// Output:
	// true
	// base: &b
	//   x: 1
	// copy: *b
}

// A patch applies to one document of a stream: the one the Document addresses.
func ExampleDocument_ApplyPatch_stream() {
	doc, err := oneyaml.Parse([]byte("a: 1\n---\na: 1\n"))
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.Doc(1).ApplyPatch([]byte(`[{"op": "replace", "path": "/a", "value": 2}]`)); err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// a: 1
	// ---
	// a: 2
}

// A "test" operation compares by the YAML data model: 1 is an integer and 1.0
// is a float, so they are different.
func ExampleDocument_Apply_testByValue() {
	doc, err := oneyaml.Parse([]byte("n: 1\n"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(doc.Apply(oneyaml.PatchTest("/n", 1)))
	err = doc.Apply(oneyaml.PatchTest("/n", 1.0))
	fmt.Println(errors.Is(err, oneyaml.ErrTestFailed))
	// Output:
	// <nil>
	// true
}

// Merge patches replace arrays whole and refuse to merge into an alias.
func ExampleDocument_ApplyMergePatch_alias() {
	doc, err := oneyaml.Parse([]byte("base: &b {x: 1}\ncopy: *b\n"))
	if err != nil {
		log.Fatal(err)
	}
	err = doc.ApplyMergePatch([]byte(`{"copy": {"x": 2}}`))
	fmt.Println(errors.Is(err, oneyaml.ErrAliasWrite))
	// Output:
	// true
}

// DefaultLimits applies when no limit option is given. UntrustedLimits is what
// the Untrusted option applies. A zero field means the bound is not applied.
func ExampleDefaultLimits() {
	fmt.Printf("%+v\n", oneyaml.DefaultLimits())
	fmt.Printf("%+v\n", oneyaml.UntrustedLimits())
	// Output:
	// {MaxDepth:10000 MaxAliasNodes:1000000 MaxAliasBytes:67108864 MaxInputBytes:0 MaxScalarBytes:0 MaxDocuments:0 MaxNumberDigits:100000 MaxPatchWorkRatio:256}
	// {MaxDepth:1000 MaxAliasNodes:100000 MaxAliasBytes:8388608 MaxInputBytes:67108864 MaxScalarBytes:16777216 MaxDocuments:10000 MaxNumberDigits:4300 MaxPatchWorkRatio:32}
}

// Untrusted tightens every limit, for input from a source you do not control. A
// limit that is hit is a *LimitError that names the option which raises it.
func ExampleUntrusted() {
	deep := strings.Repeat("[", 2000) + strings.Repeat("]", 2000)
	var v any

	// The default depth limit lets this through; Untrusted does not.
	fmt.Println(oneyaml.Unmarshal([]byte(deep), &v))
	err := oneyaml.Unmarshal([]byte(deep), &v, oneyaml.Untrusted())
	var le *oneyaml.LimitError
	if errors.As(err, &le) {
		fmt.Println(le.Limit, le.Max, le.Option)
	}
	// Output:
	// <nil>
	// depth 1000 Limits
}

// NoLimits removes every bound, including the depth limit. Use it only for
// input you trust.
func ExampleNoLimits() {
	deep := strings.Repeat("[", 10001) + strings.Repeat("]", 10001)
	var v any

	err := oneyaml.Unmarshal([]byte(deep), &v)
	var le *oneyaml.LimitError
	fmt.Println(errors.As(err, &le))
	fmt.Println(oneyaml.Unmarshal([]byte(deep), &v, oneyaml.NoLimits()))
	// Output:
	// true
	// <nil>
}

// A *SyntaxError carries the position and a stable category.
func ExampleSyntaxError() {
	err := oneyaml.Unmarshal([]byte("a: 1\na: 2\n"), new(any))
	var se *oneyaml.SyntaxError
	if errors.As(err, &se) {
		fmt.Println(se.Category, se.Line, se.Column)
	}
	// Output:
	// duplicate-key 2 1
}

// Types that carry only json tags, as Kubernetes types do, read without a yaml
// tag; any yaml tag on a field takes the place of its json tag.
func ExampleUnmarshal_jsonTags() {
	var c struct {
		Name  string `json:"name"`
		Image string `json:"image,omitempty"`
	}
	if err := oneyaml.Unmarshal([]byte("name: web\nimage: nginx:1.25\n"), &c); err != nil {
		log.Fatal(err)
	}
	fmt.Println(c.Name, c.Image)
	// Output: web nginx:1.25
}

// The primary API reads YAML 1.2: yes is a string, not a boolean, so a bool
// field refuses it. The compat packages read the way their targets do.
func ExampleUnmarshal_yesIsAString() {
	var cfg struct {
		Debug bool `yaml:"debug"`
	}
	err := oneyaml.Unmarshal([]byte("debug: yes\n"), &cfg)
	var te *oneyaml.TypeError
	if errors.As(err, &te) {
		fmt.Println(te.Path, te.Line, te.Column)
	}
	// Output: /debug 1 8
}

// Document writes the bytes it holds to any io.Writer.
func ExampleDocument_WriteTo() {
	doc, err := oneyaml.Parse([]byte("# kept\na: 1\n"))
	if err != nil {
		log.Fatal(err)
	}
	var sb strings.Builder
	n, err := doc.WriteTo(&sb)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(n, " ", sb.String())
	// Output: 12 # kept
	// a: 1
}

// The yaml.v3 way to change a value and keep the comments is to walk a
// yaml.Node tree. A Document does it with a pointer.
func Example_fromYAMLv3Node() {
	src := "# service settings\nname: api # public name\nreplicas: 2\n"
	doc, err := oneyaml.Parse([]byte(src))
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.Set("/replicas", 3); err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// # service settings
	// name: api # public name
	// replicas: 3
}

// An anchor stays on a replaced value, so every alias reads the new value. A
// tag stays when the new value reads back as exactly that value under it, and
// replacing an alias itself writes the new value in its place.
func ExampleDocument_Set_anchorsAndTags() {
	doc, err := oneyaml.Parse([]byte("a: &x 1\nb: *x\nt: !custom foo\n"))
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.Set("/a", 2); err != nil {
		log.Fatal(err)
	}
	if err := doc.Set("/t", "bar"); err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))

	var b int
	if err := doc.Get("/b", &b); err != nil {
		log.Fatal(err)
	}
	fmt.Println(b)
	if err := doc.Set("/b", 9); err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(doc.Bytes()))
	// Output:
	// a: &x 2
	// b: *x
	// t: !custom bar
	// 2
	// a: &x 2
	// b: 9
	// t: !custom bar
}

// Generic values hold what Go maps can hold. A collection used as a mapping key
// cannot be a Go map key, so Unmarshal into any refuses it; a Document reads it.
func ExampleUnmarshal_collectionKey() {
	src := []byte("[a, b]: c\n")
	var v any
	fmt.Println(oneyaml.Unmarshal(src, &v) != nil)

	doc, err := oneyaml.Parse(src)
	if err != nil {
		log.Fatal(err)
	}
	key, value := doc.Root().Entry(0)
	fmt.Println(key.Kind(), key.Len(), value.Value())
	// Output:
	// true
	// sequence 2 c
}

// Three yaml.v3 habits that differ. 0755 is the decimal integer 755, a date is a
// string in a generic value, and the "<<" key is an ordinary key unless
// MergeKeys asks for the merge.
func Example_fromYAMLv3Forms() {
	src := []byte("mode: 0755\nwhen: 2001-02-03\nbase: &b {a: 1}\nuse:\n  <<: *b\n")
	var plain, merged map[string]any
	if err := oneyaml.Unmarshal(src, &plain); err != nil {
		log.Fatal(err)
	}
	if err := oneyaml.Unmarshal(src, &merged, oneyaml.MergeKeys()); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%v %q\n", plain["mode"], plain["when"])
	fmt.Println(plain["use"])
	fmt.Println(merged["use"])

	out, err := oneyaml.Marshal(struct{ Tags []string }{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(out))
	// Output:
	// 755 "2001-02-03"
	// map[<<:map[a:1]]
	// map[a:1]
	// tags: null
}

// Marshal honours json tags when a field has no yaml tag, and writes struct
// fields in declaration order, where a round trip through JSON sorts them.
func ExampleMarshal_jsonTags() {
	type Container struct {
		Name  string   `json:"name"`
		Image string   `json:"image"`
		Args  []string `json:"args,omitempty"`
	}
	out, err := oneyaml.Marshal(Container{Name: "web", Image: "nginx:1.25", Args: []string{"--port", "8080"}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(out))
	// Output:
	// name: web
	// image: nginx:1.25
	// args:
	//   - --port
	//   - '8080'
}

// JSON is YAML, so a JSON document reads with Unmarshal as it is, and integers
// above 2^53 stay exact.
func ExampleUnmarshal_json() {
	var v map[string]any
	if err := oneyaml.Unmarshal([]byte(`{"id": 12345678901234567890, "ok": true}`), &v); err != nil {
		log.Fatal(err)
	}
	fmt.Println(v["id"], v["ok"])
	// Output: 12345678901234567890 true
}

// A LimitSet passed to Limits changes only the fields that are set: here the
// input size gains a bound, and the default depth bound stays.
func ExampleLimits_addBound() {
	opt := oneyaml.Limits(oneyaml.LimitSet{MaxInputBytes: 1 << 20})
	large := []byte("a: " + strings.Repeat("x", 2<<20) + "\n")
	deep := []byte(strings.Repeat("[", 10001) + strings.Repeat("]", 10001))
	var v any
	var le *oneyaml.LimitError

	err := oneyaml.Unmarshal(large, &v, opt)
	if errors.As(err, &le) {
		fmt.Println(le.Limit, le.Max)
	}
	err = oneyaml.Unmarshal(deep, &v, opt)
	if errors.As(err, &le) {
		fmt.Println(le.Limit, le.Max)
	}
	// Output:
	// input size 1048576
	// depth 10000
}

// NoBound lifts one bound and keeps the others at their defaults.
func ExampleNoBound() {
	deep := []byte(strings.Repeat("[", 10001) + strings.Repeat("]", 10001))
	var v any

	err := oneyaml.Unmarshal(deep, &v, oneyaml.Limits(oneyaml.LimitSet{MaxDepth: oneyaml.NoBound}))
	fmt.Println(err)
	// Output: <nil>
}

// A Limits option after Untrusted changes only the bounds it names: here depth
// is tightened, and the untrusted bound on the digits of a number still holds.
func ExampleUntrusted_tighten() {
	opts := []oneyaml.Option{oneyaml.Untrusted(), oneyaml.Limits(oneyaml.LimitSet{MaxDepth: 50})}
	deep := strings.Repeat("[", 100) + strings.Repeat("]", 100)
	long := strings.Repeat("9", 5000)
	var v any
	var le *oneyaml.LimitError

	for _, in := range []string{deep, long} {
		err := oneyaml.Unmarshal([]byte(in), &v, opts...)
		if errors.As(err, &le) {
			fmt.Println(le.Limit, le.Max)
		}
	}
	// Output:
	// depth 50
	// number digits 4300
}

// A Node is a view into a Document, so it is read with Document.Get, never
// decoded into. Marshal writes a Node as the value it holds.
func ExampleDocument_Get_node() {
	src := []byte("name: api\nspec: {replicas: 2, tier: web}\n")

	var n oneyaml.Node
	err := oneyaml.Unmarshal(src, &n)
	var te *oneyaml.TypeError
	fmt.Println(errors.As(err, &te))

	doc, err := oneyaml.Parse(src)
	if err != nil {
		log.Fatal(err)
	}
	if err := doc.Get("/spec", &n); err != nil {
		log.Fatal(err)
	}
	fmt.Println(n.Kind(), n.Len())

	out, err := oneyaml.Marshal(struct {
		Spec oneyaml.Node `yaml:"spec"`
	}{n})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(out))
	// Output:
	// true
	// mapping 2
	// spec:
	//   replicas: 2
	//   tier: web
}

// omitempty leaves out zero values, empty slices, maps and strings, and a
// struct whose fields are all zero. omitzero leaves out only zero values, so an
// empty but non-nil slice is kept.
func ExampleMarshal_omitempty() {
	type Resources struct {
		CPU    string `yaml:"cpu"`
		Memory string `yaml:"memory"`
	}
	out, err := oneyaml.Marshal(struct {
		Name      string    `yaml:"name"`
		Resources Resources `yaml:"resources,omitempty"`
		Args      []string  `yaml:"args,omitempty"`
		Env       []string  `yaml:"env,omitzero"`
	}{Name: "api", Args: []string{}, Env: []string{}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(string(out))
	// Output:
	// name: api
	// env: []
}
