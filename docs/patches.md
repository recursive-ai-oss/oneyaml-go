# Patches

How to apply [RFC 6902](https://www.rfc-editor.org/rfc/rfc6902) JSON Patch and [RFC 7396](https://www.rfc-editor.org/rfc/rfc7396) JSON Merge Patch to a YAML file. One rule differs from the RFC on purpose: `test` compares values by the YAML data model, not as JSON values, so `1` and `1.0` are different ([the test operation](#the-test-operation)). The edits follow the rules in [Editing](editing.md): only the values a patch touches change, in their own style, and comments elsewhere are kept.

There are three entry points on `Document`:

| Method | Takes |
|---|---|
| `ApplyPatch(rfc6902 []byte)` | an RFC 6902 patch as JSON text |
| `Apply(ops ...PatchOp)` | the same operations as Go values: `PatchAdd`, `PatchRemove`, `PatchReplace`, `PatchMove`, `PatchCopy`, `PatchTest` |
| `ApplyMergePatch(rfc7396 []byte)` | an RFC 7396 merge patch as JSON text |

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

If two keys have the same text the pointer is ambiguous and the operation fails with `ErrAmbiguousKey`. A key that is itself a collection cannot be addressed by a pointer. A pointer that is not valid fails with `ErrInvalidPointer`; a patch that is not valid fails with `ErrInvalidPatch`. Patch text must be UTF-8.

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

A merge patch deletes a member with `null`, merges an object into the mapping it meets and replaces anything else, so an array replaces the whole sequence. An empty object changes nothing. Deletions are applied after the other members of the same mapping. Merging an object into an alias is refused:

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

With `Untrusted()`, the patch text itself is bounded too, and the result may not exceed the input-size limit. A patch that goes past a bound fails with a `*LimitError` and changes nothing. See [Limits](limits.md).
