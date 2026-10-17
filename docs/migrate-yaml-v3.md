# Migrating from yaml.v3

How to move from `gopkg.in/yaml.v3` (or `go.yaml.in/yaml/v3`): swap the import to the `compat/yamlv3` adapter and keep your code, or move to the `oneyaml` API for YAML 1.2 and edits that keep the file.

## Migration audit

Search your code and data for these before you move:

- **Types that use gopkg.in/yaml.v3's own `Node`**, often in modules you do not own ([check after the swap](#check-after-the-swap)).
- **YAML 1.1 values** such as `yes`, `on` and `0755`, and **`<<` merge keys**, if you move to the `oneyaml` API ([behaviour that differs](#behaviour-that-differs)).
- **Fields with only a `json` tag**, and types with `UnmarshalJSON` or `MarshalJSON`, which the `oneyaml` API uses and yaml.v3 ignores ([hooks](#hooks)).
- **Other programs that read the files you edit** with YAML 1.1 rules: parse with `YAML11Strings()` ([Editing](editing.md#when-the-style-cannot-hold-the-value)).

## Swap the import

```sh
# before:  import yaml "gopkg.in/yaml.v3"
# after:   import yaml "oneyaml.dev/go/compat/yamlv3"
go get oneyaml.dev/go
```

The package is named `yaml` and has yaml.v3's API: `Unmarshal`, `Marshal`, `NewDecoder`, `NewEncoder`, `Node` with its `Kind` and `Style` constants, `Marshaler`, `Unmarshaler`, the function-style unmarshaler, `IsZeroer`, `TypeError`, and the tag options `omitempty`, `flow` and `inline`. It reads scalars the yaml.v3 way: `0755` is octal, a bool field accepts `yes`, merge keys are on, and type errors are collected.

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

The [package documentation](https://pkg.go.dev/oneyaml.dev/go/compat/yamlv3) lists every difference from yaml.v3.

### Check after the swap

Run your tests, and look at these places:

- **Hooks you do not own.** A type in another module whose `UnmarshalYAML` takes gopkg.in/yaml.v3's `*yaml.Node` keeps that signature after you swap your import. The adapter cannot call it, so reading into that type returns an error naming the type and method, where yaml.v3 would skip the method silently. The same goes for a `MarshalYAML` that returns gopkg.in/yaml.v3's `*yaml.Node`, and for fields of that type. Read such a type through a wrapper type of your own with a hook the adapter can call, or keep yaml.v3 for it. In your own code, change the import and the hooks work unchanged.
- **Hooks of any other form.** A `MarshalYAML` or `UnmarshalYAML` the adapter cannot call is an error naming the type, where yaml.v3 skips it.
- **Comment round trips through `yaml.Node`.** Comments land on the same nodes as in yaml.v3, and are written back where yaml.v3 writes them, in the common layouts. Where yaml.v3 writes text that does not read back as the same value, the adapter writes text that does. Three layouts differ: a comment after an empty last value of a flow mapping (`{a: ,` then `# note` then `}`) is written inside the braces, not after them; a comment block above a blank line at the start of a sequence under a key (`k:`, `# a`, a blank line, `# b`, `- x`) becomes the key's foot comment; and a foot comment set on a collection that is an item of a block sequence is written after that item, not at the end of the document.
- **Invalid and unusual input.** Valid YAML that yaml.v3 refuses is read, invalid YAML that yaml.v3 accepts is refused, and input that makes yaml.v3 panic returns an error.
- **Numbers.** A plain `3.5` read into an integer field is a type error instead of being truncated; `3.0` and `1e3` are accepted. A null in a sequence of numbers leaves a zero instead of being dropped.
- **Limits.** Reads run under the default limits, and there is no `Untrusted()` mode. For input you do not control, read with the `oneyaml` package ([Limits](limits.md#loading-a-configuration-file-strictly)).

## Move to the oneyaml API

| yaml.v3 | oneyaml |
|---|---|
| `yaml.Unmarshal(data, &v)` | `oneyaml.Unmarshal(data, &v)` |
| `yaml.Marshal(v)` | `oneyaml.Marshal(v)` |
| `yaml.NewDecoder(r)`, `dec.Decode(&v)`, `io.EOF` at the end | `oneyaml.NewDecoder(r)`, the same |
| `dec.KnownFields(true)` | `oneyaml.KnownFields()` passed to the call; `errors.Is(err, oneyaml.ErrUnknownField)` matches |
| `yaml.NewEncoder(w)`, `enc.SetIndent(n)` | `oneyaml.NewEncoder(w, oneyaml.Indent(n))` |
| `MarshalYAML() (interface{}, error)` | the same method |
| `UnmarshalYAML(*yaml.Node) error` | `UnmarshalYAML(oneyaml.Node) error`, see [Hooks](#hooks) |
| `UnmarshalYAML(func(interface{}) error) error` | the same method |
| `yaml.Node` for reading | `Document.Root()` and `oneyaml.Node`, a read-only view |
| `yaml.Node` as a field, to keep part of a document unparsed | `Document.Get(pointer, &node)` |
| `yaml.Node` to change a file | `Document.Set`, `Remove`, `Rename`, `SetHeadComment`, `SetLineComment`, `ApplyPatch` ([Editing a file](#editing-a-file)) |
| `yaml.TypeError` with `Errors []string` | `*oneyaml.TypeError`; `All()` returns every mismatch with its path and position |
| `omitempty` | the same, except that a struct is zero only when all its fields are, unexported ones included |
| `flow` and `inline` | the same |
| no `omitzero` | `omitzero`, as in `encoding/json` |
| a field with no `yaml` tag uses its lower-cased Go name | a `json` tag is used first |

## Behaviour that differs

The `oneyaml` package reads YAML 1.2 with the Core schema; yaml.v3 reads a mix of 1.1 and 1.2.

- **`yes`, `no`, `on` and `off` are strings**, and a bool field refuses them with a `*TypeError`.
- **`0755` is the integer 755**; `0o755` is octal.
- **A date is a string** in a generic value.
- **`<<` is an ordinary key** unless you pass `MergeKeys()`.
- **A nil slice is written as `null`**, and the default indent is two spaces, not four.
- **Duplicate keys are compared by value**, so `1` and `0x1` clash, and keys inside values the target ignores are checked too.

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

Reading tries `UnmarshalYAML(oneyaml.Node) error`, then `UnmarshalYAML(func(any) error) error`, then `UnmarshalText` for a scalar, then `UnmarshalJSON`. Writing tries `MarshalYAML() (any, error)`, `MarshalText`, then `MarshalJSON`. yaml.v3's `UnmarshalYAML(*yaml.Node) error` is reported as a `*TypeError`: change the parameter to `oneyaml.Node`, which has the same `Decode` method. Here `legacySize` has the yaml.v3 form and `Size` the moved one; both bodies are `return n.Decode(&s.Bytes)`:

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

## Editing a file

yaml.v3 edits by changing a `yaml.Node` tree and encoding all of it again, so the encoder decides the layout. With `Document` you set the value you mean and the rest of the file stays:

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

Instead of setting a key node's `Value`, call `Rename`; instead of setting `HeadComment` or `LineComment`, call `SetHeadComment` or `SetLineComment`. [Editing](editing.md) has the rules.
