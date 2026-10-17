# Migrating from sigs.k8s.io/yaml

How to move from `sigs.k8s.io/yaml` (or `ghodss/yaml`): swap the import to the `compat/k8syaml` adapter and keep your code, or move to the `oneyaml` API, which reads YAML directly instead of converting it to JSON first.

## Swap the import

```sh
# before:  import "sigs.k8s.io/yaml"
# after:   import "oneyaml.dev/go/compat/k8syaml"
go get oneyaml.dev/go
```

The package is named `yaml`. `Marshal`, `Unmarshal`, `UnmarshalStrict`, `YAMLToJSON`, `YAMLToJSONStrict`, `JSONToYAML`, `JSONOpt` and `DisallowUnknownFields` keep their names and signatures. `JSONObjectToYAMLObject` and the `kyaml`, `yamlfmt`, `goyaml.v2` and `goyaml.v3` subpackages are not provided; keep sigs.k8s.io/yaml for those calls. Like the original, it reads YAML 1.1 scalars, expands merge keys, reads the first document only, and binds through `json` tags:

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

It differs where sigs.k8s.io/yaml loses or changes data: a merge key never overrides a key written in the same mapping, a float read into a string field keeps its full precision, and YAML 1.2 syntax that sigs.k8s.io/yaml rejects is read. It returns an error where sigs.k8s.io/yaml panics on a nil target. It runs with the default limits and has no `Untrusted()` mode; for input you do not control, read with the `oneyaml` package. The [package documentation](https://pkg.go.dev/oneyaml.dev/go/compat/k8syaml) lists every difference.

## Move to the oneyaml API

| sigs.k8s.io/yaml | oneyaml |
|---|---|
| `yaml.Unmarshal(y, &obj)` | `oneyaml.Unmarshal(y, &obj)` |
| `yaml.UnmarshalStrict(y, &obj)` | `oneyaml.Unmarshal(y, &obj, oneyaml.KnownFields())`; duplicate keys are always an error |
| `yaml.Marshal(obj)` | `oneyaml.Marshal(obj)` |
| `yaml.YAMLToJSON(y)` | `oneyaml.Unmarshal` into `any`, then `json.Marshal` |
| `yaml.JSONToYAML(j)` | `oneyaml.Unmarshal(j, &v)`, then `oneyaml.Marshal(v)` |
| `JSONOpt` such as `UseNumber` | not needed: integers above 2^53 stay exact |

Types written for Kubernetes, with only `json` tags and `UnmarshalJSON` methods, work as they are:

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

## Behaviour that differs

- **Tags.** A field's `yaml` tag wins over its `json` tag; sigs.k8s.io/yaml ignores `yaml` tags. A field with both reads differently.
- **Field order.** `Marshal` keeps struct fields in declaration order and sorts only map keys; the JSON round trip sorts fields too.
- **YAML 1.2.** `yes` and `on` are strings and `0755` is the integer 755, so a bool field refuses `yes`.
- **Every type error is reported.** A `*TypeError` lists every mismatch in `All()`, where `encoding/json` reports the first.
- **Merge keys are off** unless you pass `MergeKeys()`.
- **Keys keep their type.** Into `any`, a mapping with a non-string key is a `map[any]any`.
- **Hook order.** A YAML method ranks first, then for a scalar `UnmarshalText` and `MarshalText`, then the JSON methods; sigs.k8s.io/yaml calls the JSON methods first. JSON methods receive numbers with their digits as written.

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

[Comparison](comparison.md) says when to stay on sigs.k8s.io/yaml.
