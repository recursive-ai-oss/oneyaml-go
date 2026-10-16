# Migrating from sigs.k8s.io/yaml

Two ways to move from `sigs.k8s.io/yaml` (and its parent `ghodss/yaml`): keep your code and swap the import to the `compat/k8syaml` adapter, or move to the `oneyaml` API. sigs.k8s.io/yaml converts YAML to JSON and lets `encoding/json` bind it, so what it reads is what the JSON round trip can carry. The `oneyaml` package reads YAML directly.

## Swap the import

```sh
# before:  import "sigs.k8s.io/yaml"
# after:   import "oneyaml.dev/go/compat/k8syaml"
go get oneyaml.dev/go@latest
```

The package is named `yaml`, as the original is, so call sites compile unchanged. It is a compatibility adapter with listed differences, not a copy. `Marshal`, `Unmarshal`, `UnmarshalStrict`, `YAMLToJSON`, `YAMLToJSONStrict`, `JSONToYAML`, `JSONOpt` and `DisallowUnknownFields` keep their names and signatures. The adapter does not provide `JSONObjectToYAMLObject`, which returns a type of the yaml.v2 module, or the `kyaml`, `yamlfmt`, `goyaml.v2` and `goyaml.v3` subpackages of sigs.k8s.io/yaml; code that uses them keeps its sigs.k8s.io/yaml import for those calls. Like sigs.k8s.io/yaml, it reads YAML 1.1 scalars (`yes`, `on` and `0755`), expands merge keys, reads the first document only, and binds through the `json` tags of your types:

<!-- example: compat/k8syaml ExampleUnmarshal -->
```go
var c Container
err := yaml.Unmarshal([]byte(`
name: web
image: nginx:1.25
args: [--port, 8080, --verbose]
env:
  DEBUG: yes
  RETRIES: 3
unknown: ignored
`), &c)
fmt.Println(err)
fmt.Printf("%s %s %q %v\n", c.Name, c.Image, c.Args, c.Env)
// Output:
// <nil>
// web nginx:1.25 ["--port" "8080" "--verbose"] map[DEBUG:true RETRIES:3]
```

`UnmarshalStrict` and `YAMLToJSONStrict` also reject duplicate keys and unknown fields:

<!-- example: compat/k8syaml ExampleUnmarshalStrict -->
```go
var c Container
err := yaml.UnmarshalStrict([]byte("name: web\nimage: nginx\nreplicas: 3\n"), &c)
fmt.Println(err != nil)
// Output:
// true
```

Options for the JSON decoder work as before; this one keeps integers above 2^53 exact:

<!-- example: compat/k8syaml ExampleUnmarshal_useNumber -->
```go
var v map[string]any
useNumber := func(d *json.Decoder) *json.Decoder { d.UseNumber(); return d }
if err := yaml.Unmarshal([]byte("id: 12345678901234567890\n"), &v, useNumber); err != nil {
	fmt.Println(err)
}
fmt.Println(v["id"])
// Output:
// 12345678901234567890
```

Marshal writes what sigs.k8s.io/yaml writes, with keys sorted:

<!-- example: compat/k8syaml ExampleMarshal -->
```go
out, err := yaml.Marshal(Container{
	Name:  "web",
	Image: "nginx:1.25",
	Args:  []string{"--port", "8080"},
	Env:   map[string]string{"DEBUG": "yes", "RETRIES": "3"},
})
fmt.Println(err)
fmt.Print(string(out))
// Output:
// <nil>
// args:
// - --port
// - "8080"
// env:
//   DEBUG: "yes"
//   RETRIES: "3"
// image: nginx:1.25
// name: web
```

It differs where sigs.k8s.io/yaml loses or changes data: a merge key never overrides a key written in the same mapping, a float read into a string field keeps its precision, `1` and `"1"` as keys are one JSON key with the last one winning, and YAML 1.2 syntax the reader behind sigs.k8s.io/yaml rejects is read. It returns an error where sigs.k8s.io/yaml panics on a nil target. The [package documentation](https://pkg.go.dev/oneyaml.dev/go/compat/k8syaml) lists every difference; check them against what your manifests rely on, in particular numbers read into string fields and keys that print the same. The limits are the default limits of the `oneyaml` package and cannot be changed, and there is no `Untrusted()` mode: for input you do not control, read with the `oneyaml` package.

## Move to the oneyaml API

| sigs.k8s.io/yaml | oneyaml |
|---|---|
| `yaml.Unmarshal(y, &obj)` | `oneyaml.Unmarshal(y, &obj)` |
| `yaml.UnmarshalStrict(y, &obj)` | `oneyaml.Unmarshal(y, &obj, oneyaml.KnownFields())`; `errors.Is(err, oneyaml.ErrUnknownField)` matches an unknown field, and duplicate keys are always an error |
| `yaml.Marshal(obj)` | `oneyaml.Marshal(obj)` |
| `yaml.YAMLToJSON(y)` | `oneyaml.Unmarshal` into `any`, then `json.Marshal` |
| `yaml.JSONToYAML(j)` | `oneyaml.Unmarshal(j, &v)`, then `oneyaml.Marshal(v)` |
| `JSONOpt` such as `UseNumber` | not needed: integers above 2^53 stay exact |

## Behaviour that differs

- **Tags.** sigs.k8s.io/yaml reads and writes `json` tags and ignores `yaml` tags. The `oneyaml` package uses a field's `yaml` tag and falls back to its `json` tag only when the field has no `yaml` tag. A field that has both reads differently after the move. Types written for Kubernetes carry only `json` tags and work as they are:

<!-- example: ExampleUnmarshal_jsonTags -->
```go
var c struct {
	Name  string `json:"name"`
	Image string `json:"image,omitempty"`
}
if err := oneyaml.Unmarshal([]byte("name: web\nimage: nginx:1.25\n"), &c); err != nil {
	log.Fatal(err)
}
fmt.Println(c.Name, c.Image)
// Output: web nginx:1.25
```

- **Field order.** A round trip through JSON sorts struct fields. `Marshal` keeps their declaration order and sorts only map keys.

<!-- example: ExampleMarshal_jsonTags -->
```go
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
```

- **YAML 1.2, not 1.1.** `yes` and `on` are strings and `0755` is the integer 755. A bool field refuses `yes`, and the error names the path and position:

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

- **Every type error is reported.** sigs.k8s.io/yaml binds through `encoding/json`, which skips a value that does not fit and reports only the earliest. The `oneyaml` package also skips it and reads the rest, but its `*TypeError` lists every mismatch in `More` and `All()`; the read stops at the 100th, and fewer are reported when their paths are very long. A syntax error, a limit error or an error from your own hook stops the read at once and is returned alone.
- **Merge keys are off** unless you pass `MergeKeys()`. See [Migrating from yaml.v3](migrate-yaml-v3.md) for the other 1.1 forms.
- **Keys keep their type.** Into `any`, a mapping with a non-string key is a `map[any]any`; sigs.k8s.io/yaml turns every key into a string.
- **JSON methods are used, after the YAML and text ones.** A type with `UnmarshalJSON` or `MarshalJSON` and no YAML or text method is read and written through them, with the value passed as JSON text, so types written for `encoding/json`, such as Kubernetes API types, need no change. A value that JSON cannot hold, such as a mapping with a key that is not a string, or `.nan`, is a `*TypeError` for such a type. Numbers arrive with their digits as written, on their own or inside a mapping or sequence, so a quantity such as `12345678901234567890.5` is not rounded. The order differs from `encoding/json`: a YAML method (`UnmarshalYAML`, `MarshalYAML`) ranks first, and for a scalar `UnmarshalText` and `MarshalText` rank above the JSON methods, where sigs.k8s.io/yaml calls the JSON methods first. `Level` here has only `UnmarshalJSON` and `MarshalJSON`:

<!-- example: ExampleUnmarshal_jsonHook -->
```go
var cfg struct {
	Level Level `yaml:"level"`
}
if err := oneyaml.Unmarshal([]byte("level: high\n"), &cfg); err != nil {
	log.Fatal(err)
}
fmt.Println(cfg.Level)

out, err := oneyaml.Marshal(cfg)
if err != nil {
	log.Fatal(err)
}
fmt.Print(string(out))

// The error from UnmarshalJSON comes back inside a *TypeError.
err = oneyaml.Unmarshal([]byte("level: loud\n"), &cfg)
fmt.Println(err)
// Output:
// 2
// level: high
// oneyaml: line 1, column 8: /level: cannot unmarshal "loud" into oneyaml_test.Level: unknown level "loud"
```

- **JSON is YAML.** A JSON document reads as it is, and integers above 2^53 stay exact:

<!-- example: ExampleUnmarshal_json -->
```go
var v map[string]any
if err := oneyaml.Unmarshal([]byte(`{"id": 12345678901234567890, "ok": true}`), &v); err != nil {
	log.Fatal(err)
}
fmt.Println(v["id"], v["ok"])
// Output: 12345678901234567890 true
```

The supported Go versions are listed under [Stability](../README.md#stability) in the README. For how the libraries compare, see [Comparison](comparison.md).
