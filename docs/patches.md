# Patches

How to apply a JSON Patch ([RFC 6902](https://www.rfc-editor.org/rfc/rfc6902)) or a JSON Merge Patch ([RFC 7396](https://www.rfc-editor.org/rfc/rfc7396)) to a YAML file. Patches edit by the rules of [Editing](editing.md): only the values they touch change, and comments elsewhere stay.

| Method | Takes |
|---|---|
| `ApplyPatch(rfc6902 []byte)` | an RFC 6902 patch as JSON, or as a YAML list of operations |
| `Apply(ops ...PatchOp)` | the same operations as Go values: `PatchAdd`, `PatchRemove`, `PatchReplace`, `PatchMove`, `PatchCopy`, `PatchTest` |
| `ApplyMergePatch(rfc7396 []byte)` | an RFC 7396 merge patch as JSON or YAML |

## A patch is atomic

If any operation fails, the document is unchanged, the operations before it included. The error is an `*EditError` whose `Index` is the failing operation.

<!-- example: ExampleDocument_ApplyPatch -->
```go
doc, err := oneyaml.Parse([]byte(`# service
name: api
replicas: 2   # raised by the release job
tags: [web]
`))
if err != nil {
	log.Fatal(err)
}
err = doc.ApplyPatch([]byte(`[
	{"op": "test", "path": "/name", "value": "api"},
	{"op": "replace", "path": "/replicas", "value": 3},
	{"op": "add", "path": "/tags/-", "value": "edge"},
	{"op": "copy", "from": "/name", "path": "/title"}
]`))
if err != nil {
	log.Fatal(err)
}
fmt.Print(string(doc.Bytes()))

// A failing test rejects the whole patch.
err = doc.ApplyPatch([]byte(`[
	{"op": "replace", "path": "/replicas", "value": 9},
	{"op": "test", "path": "/name", "value": "web"}
]`))
var ee *oneyaml.EditError
fmt.Println(errors.Is(err, oneyaml.ErrTestFailed), errors.As(err, &ee) && ee.Index == 1)
fmt.Print(string(doc.Bytes()))
// Output:
// # service
// name: api
// replicas: 3   # raised by the release job
// tags: [web, edge]
// title: api
// true true
// # service
// name: api
// replicas: 3   # raised by the release job
// tags: [web, edge]
// title: api
```

The typed form takes the same operations:

<!-- example: ExampleDocument_Apply -->
```go
doc, err := oneyaml.Parse([]byte("a: 1\nb: 2\n"))
if err != nil {
	log.Fatal(err)
}
err = doc.Apply(
	oneyaml.PatchTest("/a", 1),
	oneyaml.PatchMove("/b", "/c"),
	oneyaml.PatchAdd("/d", []string{"x", "y"}),
)
if err != nil {
	log.Fatal(err)
}
fmt.Print(string(doc.Bytes()))
// Output:
// a: 1
// c: 2
// d:
//   - x
//   - y
```

A patch applies to one document: the first of a stream, or the one you select with `Doc(i)`.

## YAML rules at a glance

The RFCs are written for JSON. Applied to YAML:

- **`test` compares YAML values**, see [below](#the-test-operation).
- **A pointer token matches a key by its text**, so `/1` finds the integer key `1` as well as the string key `"1"`. Build paths from data with `oneyaml.Pointer`. Two keys with the same text make the pointer ambiguous (`ErrAmbiguousKey`).
- **A write through an alias is refused** with `ErrAliasWrite`; patch the anchored node instead. Removing or moving an anchored node that an alias uses is refused with `ErrAnchorInUse`. A `copy` of a value that holds aliases writes them out as copies where their anchor would be missing.
- **Text that starts with `[` or `{` is JSON**, so a flow-style YAML patch with unquoted keys is refused; write it as JSON or as a YAML block list.
- **Merge keys:** with `MergeKeys()`, the rules in [Merge keys](editing.md#merge-keys) apply.

<!-- example: ExampleDocument_ApplyPatch_yamlRules -->
```go
doc, err := oneyaml.Parse([]byte("replicas: 1\n"))
if err != nil {
	log.Fatal(err)
}
// 1 is an integer and 1.0 a float, so this test fails.
err = doc.ApplyPatch([]byte(`[{"op": "test", "path": "/replicas", "value": 1.0}]`))
fmt.Println(errors.Is(err, oneyaml.ErrTestFailed))

// Text that starts with [ is read as JSON, and these keys are not quoted.
err = doc.ApplyPatch([]byte(`[{op: test, path: /replicas, value: 1}]`))
fmt.Println(errors.Is(err, oneyaml.ErrInvalidPatch))

// The same operation as a YAML block list.
err = doc.ApplyPatch([]byte("- {op: test, path: /replicas, value: 1}\n"))
fmt.Println(err)
// Output:
// true
// true
// <nil>
```

### The test operation

`test` compares by the YAML data model under the Core schema: `1` and `1.0` differ, `0x1F` equals `31`, and a mapping key matches a JSON object key only if it is a string. RFC 6902 section 4.6 compares JSON values, so it would call `1` and `1.0` equal.

## Patches written in YAML

`ApplyPatch` takes a YAML block list of operations with the same members as the JSON form:

<!-- example: ExampleDocument_ApplyPatch_yaml -->
```go
doc, err := oneyaml.Parse([]byte("replicas: 2\nimage: nginx:1.25\n"))
if err != nil {
	log.Fatal(err)
}
patch := `
- op: replace
  path: /replicas
  value: 5
- op: add
  path: /labels
  value:
    tier: web
`
if err := doc.ApplyPatch([]byte(patch)); err != nil {
	log.Fatal(err)
}
fmt.Print(doc)
// Output:
// replicas: 5
// image: nginx:1.25
// labels:
//   tier: web
```

A YAML patch is refused with `ErrInvalidPatch` when it is not a single list, uses an alias, a tag other than a core one or `!`, or a collection as a key, repeats a key, or is not valid YAML (the error gives the line and column). `!` makes a scalar a string.

`ApplyMergePatch` reads YAML the same way, with one more rule: every key must be a string, so quote `"1": 5` to name the member `1`.

<!-- example: ExampleDocument_ApplyMergePatch_yaml -->
```go
doc, err := oneyaml.Parse([]byte(`name: api
limits:
  cpu: 1   # cores
  mem: 2
`))
if err != nil {
	log.Fatal(err)
}
err = doc.ApplyMergePatch([]byte(`limits:
  cpu: 4
  mem: null
tier: gold
`))
if err != nil {
	log.Fatal(err)
}
fmt.Print(string(doc.Bytes()))
// Output:
// name: api
// limits:
//   cpu: 4   # cores
// tier: gold
```

## Merge patches

A merge patch deletes a member with `null`, merges an object into the mapping it meets, and replaces anything else, so an array replaces the whole sequence. Merging an object into an alias is refused with `ErrAliasWrite`.

## Bounds

A patch can ask for a lot of work, so it runs under limits even by default:

- the bytes that copies and written-out aliases produce count against `MaxAliasBytes`;
- the text a patch's edits read again is bounded by `MaxPatchWorkRatio`.

With `MaxInputBytes` set (as `Untrusted()` sets it), the patch text may be at most an eighth of it (but at least 64 KiB, and never more than the bound itself), and the result may not exceed it. A patch past a bound fails with a `*LimitError` and changes nothing. See [Limits](limits.md).
