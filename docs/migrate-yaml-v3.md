# Migrating from yaml.v3

Two ways to move from `gopkg.in/yaml.v3` (or `go.yaml.in/yaml/v3`): keep your code and swap the import to the `compat/yamlv3` adapter, or move to the `oneyaml` API. The first keeps your call sites, and the [checklist](#check-after-the-swap) below covers where its behaviour can differ. The second reads YAML 1.2 as the specification says and gives you minimal-diff edits.

## Swap the import

```sh
# before:  import yaml "gopkg.in/yaml.v3"
# after:   import yaml "oneyaml.dev/go/compat/yamlv3"
go get oneyaml.dev/go@latest
```

The package is named `yaml`, so call sites compile unchanged. It is a compatibility adapter, not a copy of yaml.v3. It reproduces what yaml.v3 documents: plain scalars resolve the yaml.v3 way, so `0755` is an octal number and `yes` is accepted by a bool field, merge keys are on, and a type error does not stop decoding.

<!-- example: compat/yamlv3 Example -->
```go
type Service struct {
	Name  string `yaml:"name"`
	Mode  int    `yaml:"mode"`
	Debug bool   `yaml:"debug"`
}
var s Service
if err := yaml.Unmarshal([]byte("name: api\nmode: 0755\ndebug: yes\n"), &s); err != nil {
	fmt.Println(err)
	return
}
fmt.Printf("%+v\n", s)

out, err := yaml.Marshal(s)
if err != nil {
	log.Fatal(err)
}
fmt.Print(string(out))
// Output:
// {Name:api Mode:493 Debug:true}
// name: api
// mode: 493
// debug: true
```

Type errors are collected, as in yaml.v3:

<!-- example: compat/yamlv3 ExampleTypeError -->
```go
var v struct {
	Port  int
	Name  string
	Count int
}
err := yaml.Unmarshal([]byte("port: eighty\nname: api\ncount: x\n"), &v)
var te *yaml.TypeError
if e, ok := err.(*yaml.TypeError); ok {
	te = e
}
for _, msg := range te.Errors {
	fmt.Println(msg)
}
fmt.Println(v.Name)
// Output:
// line 1: cannot unmarshal !!str `eighty` into int
// line 3: cannot unmarshal !!str `x` into int
// api
```

The package differs from yaml.v3 where yaml.v3 is wrong: it reads valid YAML that yaml.v3's scanner refuses, returns an error where yaml.v3 panics, never turns a comment into content, and keeps the default limits of the `oneyaml` package. The [package documentation](https://pkg.go.dev/oneyaml.dev/go/compat/yamlv3) lists each difference and each gap, including the comment layouts it does not place the way yaml.v3 does. Reading with `yaml.Node` and writing it back works for the common comment layouts; to edit a file, see below.

### Check after the swap

Run your tests, and look at these places in particular:

- **Hooks.** `UnmarshalYAML(*yaml.Node) error`, the older `UnmarshalYAML(func(interface{}) error) error` and `MarshalYAML` work as in yaml.v3, with the adapter's own `yaml.Node`. A method that takes the original package's `*yaml.Node`, for example in a package of yours that still imports `gopkg.in/yaml.v3`, does not match and is not called, as yaml.v3 itself skips a method of the wrong type; change that import too.
- **Node and comment round trips.** Decoding into a `yaml.Node`, changing it and encoding it again keeps line comments, comments directly above a key or item, foot comments and document comments. Other layouts, such as comment lines inside a flow collection, are not placed on the same node or are not written back. If you diff files written this way, check one.
- **Repeated keys.** A key that occurs twice in a mapping the target ignores (a struct with no such field) is not reported, where yaml.v3 reports it.
- **Invalid and unusual input.** Valid YAML that yaml.v3 refuses is read, invalid YAML that yaml.v3 accepts is refused, and inputs that make yaml.v3 panic return an error. Tests that pin yaml.v3's behaviour on such input will change.
- **Numeric conversions.** A plain `3.5` read into an integer field is a type error instead of being truncated to 3; a float with no fraction (`3.0`, `1e3`) is accepted. In a sequence of numbers, a null leaves a zero in its place instead of being dropped.
- **Limits.** Reading runs under the default limits of the `oneyaml` package, which yaml.v3 does not have, and there is no `Untrusted()` mode. Very deep or alias-heavy input that yaml.v3 read may now return an error. For input you do not control, read with the `oneyaml` package and `Untrusted()`.

## Move to the oneyaml API

| yaml.v3 | oneyaml |
|---|---|
| `yaml.Unmarshal(data, &v)` | `oneyaml.Unmarshal(data, &v)` |
| `yaml.Marshal(v)` | `oneyaml.Marshal(v)` |
| `yaml.NewDecoder(r)`, `dec.Decode(&v)`, `io.EOF` at the end | `oneyaml.NewDecoder(r)`, the same |
| `dec.KnownFields(true)` | `oneyaml.KnownFields()` passed to the call |
| `yaml.NewEncoder(w)`, `enc.SetIndent(n)` | `oneyaml.NewEncoder(w, oneyaml.Indent(n))` |
| `yaml.Marshaler`, `MarshalYAML() (interface{}, error)` | `oneyaml.Marshaler`, the same method |
| `yaml.Unmarshaler`, `UnmarshalYAML(*yaml.Node) error` | `oneyaml.Unmarshaler`, `UnmarshalYAML(oneyaml.Node) error`; the old form is an error, see [Hooks](#hooks) |
| `UnmarshalYAML(func(interface{}) error) error` | the same method, unchanged |
| `yaml.Node` for reading | `Document.Root()` and `oneyaml.Node`, a read-only view |
| `yaml.Node` as a field, to keep part of a document unparsed | `Document.Get(pointer, &node)`; a `oneyaml.Node` is not a decode target |
| `yaml.Node` to change a file | `Document.Set`, `Remove`, `ApplyPatch` |
| `yaml.TypeError` with `Errors []string` | `*oneyaml.TypeError`, one error with the path and position of the first value that does not fit |
| struct tag `omitempty` | the same: zero values, empty collections and zero structs are left out, and an `IsZero` method decides when a type has one. One difference: a struct counts as zero only when all its fields are zero, unexported ones too, where yaml.v3 looks at exported fields only |
| struct tags `flow` and `inline` (a struct or a map) | the same |
| no `omitzero` | `omitzero` leaves out a zero value, or one whose `IsZero` returns true, as `encoding/json` does; an empty but non-nil slice is kept |
| a field with no `yaml` tag is named by its lower-cased Go name | a `json` tag is used first, then the lower-cased Go name |

`omitempty` and `omitzero` side by side:

<!-- example: ExampleMarshal_omitempty -->
```go
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
```

## Behaviour that differs

The `oneyaml` package follows YAML 1.2 with the Core schema. yaml.v3 follows a mix of 1.1 and 1.2. The differences you are likely to meet:

- **`yes`, `no`, `on`, `off` are strings.** A bool field refuses them with a `*TypeError` that names the path and position. Write `true` and `false`, or use the compat package.

<!-- example: ExampleUnmarshal_yesIsAString -->
```go
var cfg struct {
	Debug bool `yaml:"debug"`
}
err := oneyaml.Unmarshal([]byte("debug: yes\n"), &cfg)
var te *oneyaml.TypeError
if errors.As(err, &te) {
	fmt.Println(te.Path, te.Line, te.Column)
}
// Output: /debug 1 8
```

- **`0755` is the integer 755**, not octal. `0o755` is octal.
- **A date is a string** in a generic value; there is no implicit `time.Time`.
- **The `<<` key is an ordinary key** unless you pass `MergeKeys()`.
- **A nil slice is written as `null`**, not `[]`. The default indentation is two spaces, not four.
- **A type error stops at the first** and the error says where.
- **A duplicate key is an error**, as in yaml.v3.

<!-- example: Example_fromYAMLv3Forms -->
```go
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
```

## Hooks

When it reads a value, the `oneyaml` package tries a type's methods in this order: `UnmarshalYAML(oneyaml.Node) error`, then `UnmarshalYAML(func(any) error) error`, then `UnmarshalText` for a scalar, then `UnmarshalJSON`. When it writes, the order is `MarshalYAML() (any, error)`, `MarshalText`, then `MarshalJSON`. For a type written for yaml.v3:

| Method on your type | With the `oneyaml` package |
|---|---|
| `UnmarshalYAML(value *yaml.Node) error` | a `*TypeError` that names the type and the accepted forms. Change the parameter to `oneyaml.Node`, which has the same `Decode` method |
| `UnmarshalYAML(unmarshal func(interface{}) error) error` | called as yaml.v3 calls it |
| `MarshalYAML() (interface{}, error)` | called as yaml.v3 calls it |
| `MarshalYAML` or `UnmarshalYAML` with any other signature | a `*TypeError` |
| `UnmarshalText` and `MarshalText` | used for scalars, as in yaml.v3 |
| `UnmarshalJSON` and `MarshalJSON`, and no YAML or text method | used, with the value passed as JSON text; yaml.v3 ignores them |

A method with a signature the package does not call is reported, not skipped, so a type is never quietly filled field by field when you meant it to read itself. The compat package keeps yaml.v3's own form: with the import swapped, `UnmarshalYAML(*yaml.Node) error` works unchanged.

In the example, `legacySize` has yaml.v3's `UnmarshalYAML(n *yaml.Node) error` and `Size` is the same type moved over, with `UnmarshalYAML(n oneyaml.Node) error`. Both bodies are `return n.Decode(&s.Bytes)`.

<!-- example: ExampleUnmarshaler_fromYAMLv3 -->
```go
src := []byte("limit: 512\n")

var before struct {
	Limit legacySize `yaml:"limit"`
}
err := oneyaml.Unmarshal(src, &before)
fmt.Println(err)

var after struct {
	Limit Size `yaml:"limit"`
}
if err := oneyaml.Unmarshal(src, &after); err != nil {
	log.Fatal(err)
}
fmt.Println(after.Limit.Bytes)
// Output:
// oneyaml: line 1, column 8: /limit: oneyaml_test.legacySize has an UnmarshalYAML method that is neither UnmarshalYAML(oneyaml.Node) error nor UnmarshalYAML(func(any) error) error; change its signature, or remove it to fill the value by its fields
// 512
```

The older form needs no change. `Tags` here has `UnmarshalYAML(unmarshal func(any) error) error` and reads either a sequence or a comma-separated string:

<!-- example: ExampleUnmarshal_funcHook -->
```go
var doc struct {
	A Tags `yaml:"a"`
	B Tags `yaml:"b"`
}
if err := oneyaml.Unmarshal([]byte("a: [x, y]\nb: \"p,q,r\"\n"), &doc); err != nil {
	log.Fatal(err)
}
fmt.Println(doc.A, doc.B)
// Output:
// [x y] [p q r]
```

## Editing a file

yaml.v3 can keep comments by loading into a `yaml.Node`, changing it and encoding the whole tree again, so the encoder decides the layout. With `Document` you set the value you mean and everything else stays as it was:

<!-- example: Example_fromYAMLv3Node -->
```go
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
```

The rules for the style of the new value, for adding and removing keys and for anchors are in [Editing](editing.md). For how the libraries compare, see [Comparison](comparison.md).

The supported Go versions are listed under [Stability](../README.md#stability) in the README.
