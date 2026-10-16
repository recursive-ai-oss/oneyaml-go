# Patches

How to apply JSON Patch operations ([RFC 6902](https://www.rfc-editor.org/rfc/rfc6902)) with YAML equality for `test`, and [RFC 7396](https://www.rfc-editor.org/rfc/rfc7396) JSON Merge Patch, to a YAML file. One rule differs from the RFC on purpose: `test` compares values by the YAML data model, not as JSON values, so `1` and `1.0` are different ([the test operation](#the-test-operation)). The edits follow the rules in [Editing](editing.md): only the values a patch touches change, in their own style, and comments elsewhere are kept.

There are three entry points on `Document`:

| Method | Takes |
|---|---|
| `ApplyPatch(rfc6902 []byte)` | an RFC 6902 patch as JSON text, or as a YAML sequence of operations ([details](#patches-written-in-yaml)) |
| `Apply(ops ...PatchOp)` | the same operations as Go values: `PatchAdd`, `PatchRemove`, `PatchReplace`, `PatchMove`, `PatchCopy`, `PatchTest` |
| `ApplyMergePatch(rfc7396 []byte)` | an RFC 7396 merge patch as JSON text, or as YAML ([details](#patches-written-in-yaml)) |

## A patch is atomic

If any operation fails, the document is unchanged, including the operations before the one that failed. The error is an `*EditError` whose `Index` is the position of the failing operation and whose kind you match with `errors.Is`.

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

`add` inserts before an index, as RFC 6902 says; `Document.Set` assigns. `move` removes then adds, except that between two mappings the destination is written first, so a mapping never collapses to `{}` halfway.

## Pointers

Operations address values with [RFC 6901](https://www.rfc-editor.org/rfc/rfc6901) pointers. A token matches a mapping key by the key's text, so `/1` finds the integer key `1` as well as the string key `"1"`:

<!-- example: ExampleDocument_ApplyPatch_pointerText -->
```go
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
```

Inside a token, `/` is written `~1` and `~` is written `~0`. When a key comes from data, build the path with `oneyaml.Pointer`, which escapes each token: `oneyaml.PatchRemove(oneyaml.Pointer("metadata", "labels", key))` removes the label `app.kubernetes.io/name` when `key` holds it ([Editing](editing.md) shows the pointer it builds).

A pointer addresses the keys written in the file. With `MergeKeys()`, `test` and the `from` of `copy` also read a key that a `<<` merge brings in, and `remove`, `replace` and `move` of such a key are refused with `ErrAliasWrite`; [Merge keys](editing.md#merge-keys) has the rules. If two keys have the same text the pointer is ambiguous and the operation fails with `ErrAmbiguousKey`. A key that is itself a collection cannot be addressed by a pointer. A pointer that is not valid fails with `ErrInvalidPointer`; a patch that is not valid fails with `ErrInvalidPatch`. Patch text must be UTF-8.

## Patches written in YAML

`ApplyPatch` also takes a patch kept as a YAML file: a block list of operations with the same members as the JSON form. It applies exactly as the JSON patch with the same operations does:

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

The first significant byte decides how the text is read. Text that starts with `[` or `{` is JSON, and only JSON, so its errors are JSON errors. A flow-style YAML patch such as `[{op: add, path: /a, value: 1}]` is therefore refused, because its keys are not quoted. All other text is read as YAML. The non-specific tag `!` is allowed and makes a scalar a string, so `value: ! 12` writes the string `"12"`.

A YAML patch fails with `ErrInvalidPatch` when it:

- holds more than one document, or is not a list;
- uses an alias, a tag outside the core schema such as `!foo`, or a collection as a key, none of which JSON can express;
- names a key twice in a mapping, judged by value as the decoder judges it, so `{1: a, 0x1: b}` and `{null: 1, ~: 2}` are refused;
- has a scalar that cannot be read, such as a double-quoted escape that is not a Unicode character;
- is not valid YAML. The error gives the line and column.

A YAML patch nested deeper than the depth limit fails with a `*LimitError`.

`ApplyMergePatch` reads YAML the same way: text that starts with `{` or `[`, or is valid JSON, is JSON, and any other text is one YAML document of any kind (a mapping, usually) with the same refusals as a YAML list of operations. The members of a merge patch are named by strings, so a mapping key that is not a string, such as `1: 5`, `true: 5` or an empty key, is refused with `ErrInvalidPatch` at any depth, with its line and column. Quote it (`"1": 5`) to name the member `1`.

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

## Aliases

Reads follow aliases. Writes do not: a patch that would write through an alias is refused with `ErrAliasWrite`, because it would change every place that shares the anchored node. Patch the anchored node instead, and the change shows through every alias.

<!-- example: ExampleDocument_ApplyPatch_aliasRefused -->
```go
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
```

Removing or moving an anchored node that an alias still uses is refused with `ErrAnchorInUse`. When a `copy` or `add` takes a value that holds aliases, the value is written at its destination without its anchor, and an alias that would no longer have its anchor in front of it is written out as a copy. Written-out aliases count against the alias budget described in [Limits](limits.md).

## One document per patch

A patch applies to the document the `Document` addresses, which is the first document of a stream unless you select another with `Doc(i)`.

<!-- example: ExampleDocument_ApplyPatch_stream -->
```go
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
```

## The test operation

`test` compares by the YAML data model under the Core schema, not by JSON text. `1` and `1.0` are different, `0x1F` equals `31`, and a mapping key matches a JSON object key only if it is a string. This is stricter than RFC 6902 section 4.6, which compares JSON values.

<!-- example: ExampleDocument_Apply_testByValue -->
```go
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
```

## Merge patches

A merge patch deletes a member with `null`, merges an object into the mapping it meets and replaces anything else, so an array replaces the whole sequence. An empty object changes nothing. Deletions are applied after the other members of the same mapping. With `MergeKeys()`, a member `null` for a key that only a `<<` merge source has is refused with `ErrAliasWrite` ([Merge keys](editing.md#merge-keys)). Merging an object into an alias is refused:

<!-- example: ExampleDocument_ApplyMergePatch_alias -->
```go
doc, err := oneyaml.Parse([]byte("base: &b {x: 1}\ncopy: *b\n"))
if err != nil {
	log.Fatal(err)
}
err = doc.ApplyMergePatch([]byte(`{"copy": {"x": 2}}`))
fmt.Println(errors.Is(err, oneyaml.ErrAliasWrite))
// Output:
// true
```

A complete merge patch walkthrough, with the comments kept, is in the reference for [`Document.ApplyMergePatch`](https://pkg.go.dev/oneyaml.dev/go#Document.ApplyMergePatch).

## Bounds

A patch can ask for a lot of work: `copy` can double a document at every step, and some edits re-read a large part of the file. Two limits bound a patch, and both apply under the default limits:

- the bytes that copies and written-out aliases may produce are charged to the alias-bytes limit;
- the text that a patch's edits have to read again is bounded by `LimitSet.MaxPatchWorkRatio`.

With a `MaxInputBytes` limit (as `Untrusted()` sets), the patch text itself may be at most an eighth of it (at least 64 KiB, and never more than the limit), because a patch is read into a list of operations that takes far more memory than the same amount of document, and the result may not exceed the input-size limit. The default limits set no input bound. A patch that goes past a bound fails with a `*LimitError` and changes nothing. See [Limits](limits.md).
