# Editing

How to change a YAML file with `Document` so that only your change shows in the diff. The API reference is on [pkg.go.dev](https://pkg.go.dev/oneyaml.dev/go#Document); patches have [their own page](patches.md).

`Parse` returns a `Document` that holds the whole input, and `Bytes` returns it exactly until you edit it. An edit changes only the value it addresses, in that value's style; comments, blank lines, quoting and indentation elsewhere stay byte for byte. `fmt.Print(doc)` prints the document as it stands.

- **One goroutine at a time.** A `Document` is not safe for concurrent use, reads included. Guard it with a mutex, or give each goroutine its own.
- **`Bytes` is read-only.** The slice belongs to the document and stays valid after later edits; copy it with `bytes.Clone` before you change it. `Parse` copies its input, so you may reuse yours.

You address a value with an [RFC 6901](https://www.rfc-editor.org/rfc/rfc6901) pointer: `/replicas`, `/tags/0`, or `/tags/-` for the end of a sequence. When a key comes from data, build the pointer with `oneyaml.Pointer`, which escapes `/` as `~1` and `~` as `~0`:

<!-- example: ExamplePointer -->
```go
doc, err := oneyaml.Parse([]byte("metadata:\n  labels:\n    app.kubernetes.io/name: web\n"))
if err != nil {
	log.Fatal(err)
}
ptr := oneyaml.Pointer("metadata", "labels", "app.kubernetes.io/name")
fmt.Println(ptr)
if err := doc.Set(ptr, "api"); err != nil {
	log.Fatal(err)
}
fmt.Print(doc)
// Output:
// /metadata/labels/app.kubernetes.io~1name
// metadata:
//   labels:
//     app.kubernetes.io/name: api
```

A pointer token matches a key by its text, so `/1` finds the key `1`.

## Changing a value

`Set` replaces a value and keeps its style. Reading the document back gives exactly the value you set, so a block literal that loses its final line break gets the header `|-`, and nothing else changes:

<!-- example: Example_blockLiteralStrip -->
```go
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
```

## When the style cannot hold the value

When the old style cannot hold the new value, only that value is rewritten, in the nearest style that can. Plainness follows the Core schema: `yes` stays plain, `true` as a string is quoted, and a string with a line break that replaces an inline scalar is double-quoted.

<!-- example: ExampleDocument_Set_styleFallback -->
```go
doc, err := oneyaml.Parse([]byte("a: plain\nb: plain\nc: plain\nd: \"quoted\"\n"))
if err != nil {
	log.Fatal(err)
}
for _, edit := range []struct {
	ptr string
	v   any
}{
	{"/a", "nginx:1.25"},  // Core reads this as a string: it stays plain
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
// a: nginx:1.25
// b: 'true'
// c: "two\nlines"
// d: "now: plain?"
```

If programs that read YAML 1.1 also read the file (yaml.v2, or tooling built on sigs.k8s.io/yaml), parse it with `YAML11Strings()`. New strings from `Set`, patches and `Rename` then stay plain only when YAML 1.1 reads them as the same string, so `yes`, `on`, `0755` and `12:30` are quoted.

## Adding and removing

`Set` on a missing key adds it at the end of its mapping, in the style of its neighbours. `Set` at `/-`, or at the index equal to the length, appends to a sequence. A null already in the file keeps its spelling.

<!-- example: ExampleDocument_Set_newKey -->
```go
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
```

`Remove` deletes an entry with the comment on its line, the comment directly above it and any comment inside its value. The comment above the next entry stays.

<!-- example: ExampleDocument_Remove -->
```go
doc, err := oneyaml.Parse([]byte("a: 1\n# about b\nb: 2 # two\n# about c\nc: 3\n"))
if err != nil {
	log.Fatal(err)
}
if err := doc.Remove("/b"); err != nil {
	log.Fatal(err)
}
fmt.Print(string(doc.Bytes()))
// Output:
// a: 1
// # about c
// c: 3
```

## Renaming a key

`Rename(ptr, newKey)` changes a key where it stands. The value, the entry's place, its comments and an anchor or tag on the key stay. The new key keeps the old key's quoting when it can, and is always a string.

<!-- example: ExampleDocument_Rename -->
```go
doc, err := oneyaml.Parse([]byte(`# chart values
image: nginx
replicas: 2 # scaled by the autoscaler
port: 80
`))
if err != nil {
	log.Fatal(err)
}
if err := doc.Rename("/replicas", "replicaCount"); err != nil {
	log.Fatal(err)
}
fmt.Print(doc)

err = doc.Rename("/port", "image")
fmt.Println(errors.Is(err, oneyaml.ErrKeyExists))
// Output:
// # chart values
// image: nginx
// replicaCount: 2 # scaled by the autoscaler
// port: 80
// true
```

`Rename` refuses, and leaves the document unchanged, when the mapping already has the new key (`ErrKeyExists`), the pointer is the root or a sequence item (`ErrNotAnEntry`), it leads through an alias (`ErrAliasWrite`), the key occurs twice (`ErrAmbiguousKey`), or an alias uses an anchor on the key (`ErrAnchorInUse`). Keys that are empty, collections, aliases, block scalars or `<<` cannot be renamed (`ErrUnsupportedEdit`).

## Comments

`SetHeadComment(ptr, text)` replaces the comment lines above an entry or item, and `SetLineComment(ptr, text)` the comment after a value on its line. An empty text removes the comment, and a line without a leading `#` gets `# `. The root pointer `""` sets the comment above the first content line. `Node.HeadComment` and `Node.LineComment` read comments back, trimmed as yaml.v3 trims them.

<!-- example: ExampleDocument_SetLineComment -->
```go
doc, err := oneyaml.Parse([]byte(`replicas: 2
script: |
  make build
env:
  mode: prod
`))
if err != nil {
	log.Fatal(err)
}
for ptr, text := range map[string]string{
	"/replicas": "scaled by the autoscaler",
	"/script":   "run by CI",
	"/env":      "per environment",
} {
	if err := doc.SetLineComment(ptr, text); err != nil {
		log.Fatal(err)
	}
}
fmt.Print(doc)

replicas, _ := doc.Root().Lookup("replicas")
fmt.Println(replicas.LineComment())
// Output:
// replicas: 2 # scaled by the autoscaler
// script: | # run by CI
//   make build
// env: # per environment
//   mode: prod
// # scaled by the autoscaler
```

The setters refuse places that cannot hold a comment with `ErrUnsupportedEdit`: a head comment inside a flow collection or on an entry that shares its line (the first key after `- `), and a line comment inside a flow collection when anything follows the item on its line, or on a value that something else follows. Text with a control character, invalid UTF-8, or a line break in a line comment is refused with `ErrInvalidComment`.

Comments inside a value you replace or remove go with it.

## Anchors, aliases and tags

An anchor stays on a replaced value, so every alias reads the new value. A tag stays when the new value still reads as that value under it. Replacing an alias node writes the new value in its place.

<!-- example: ExampleDocument_Set_anchorsAndTags -->
```go
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
```

Writing below an alias (`/copy/x` when `copy` is `*b`) is refused with `ErrAliasWrite`, because it would change every place that shares the node; edit the anchored node instead. Removing an anchored node that an alias still uses is refused with `ErrAnchorInUse`.

## Merge keys

With `MergeKeys()`, a pointer also finds the keys a `<<` merge brings in: the mapping's own key wins, then the earlier merge source. `Get`, `Node.Find`, `Node.Lookup`, the patch `test` and the `from` of a patch `copy` read merged keys; `Entries`, `Items` and `Len` show the mapping as written.

A merged key belongs to its source, so:

- `Set` of a merged-only key adds an explicit key that overrides it, and so do a patch `add` and a merge patch member;
- `Remove`, the patch operations `remove`, `replace` and `move`, a merge patch `null`, and any write below such a key or through `<<` are refused with `ErrAliasWrite`.

<!-- example: ExampleMergeKeys_pointer -->
```go
src := "base: &base {image: nginx, port: 80}\nweb:\n  <<: *base\n  port: 8080\n"
doc, err := oneyaml.Parse([]byte(src), oneyaml.MergeKeys())
if err != nil {
	log.Fatal(err)
}
var image string
var port int
if err := doc.Get("/web/image", &image); err != nil { // from the merge
	log.Fatal(err)
}
if err := doc.Get("/web/port", &port); err != nil { // the mapping's own key wins
	log.Fatal(err)
}
fmt.Println(image, port)

// Remove would have to write into the merge source, so it is refused.
err = doc.Remove("/web/image")
fmt.Println(errors.Is(err, oneyaml.ErrAliasWrite))

// Set adds a key of its own to web, which overrides the merged one.
if err := doc.Set("/web/image", "caddy"); err != nil {
	log.Fatal(err)
}
fmt.Print(string(doc.Bytes()))
// Output:
// nginx 8080
// true
// base: &base {image: nginx, port: 80}
// web:
//   <<: *base
//   port: 8080
//   image: caddy
```

The keys a merge search reads count against `MaxAliasNodes` ([Limits](limits.md#each-limit)). A search past it is a `*LimitError` from `Get` and `Node.Find`, and `false` from `Node.Lookup`. Without `MergeKeys()`, `<<` is an ordinary key.

## Reading values back

`Get(ptr, &v)` reads the value at a pointer into any target `Unmarshal` accepts, `Decode(&v)` reads the whole document, and `Node.Decode` the part below a node. Options passed to a read apply to that call only, on top of those given to `Parse`:

<!-- example: ExampleDocument_Get_options -->
```go
src := []byte("name: api\nspec:\n  replicas: 2\n  replica: 3\n")
type Spec struct {
	Replicas int `yaml:"replicas"`
}

// Parsed without options, read strictly in one place.
doc, err := oneyaml.Parse(src)
if err != nil {
	log.Fatal(err)
}
var strict Spec
err = doc.Get("/spec", &strict, oneyaml.KnownFields())
fmt.Println(errors.Is(err, oneyaml.ErrUnknownField))

// Parsed strictly, read loosely in one place.
doc, err = oneyaml.Parse(src, oneyaml.KnownFields())
if err != nil {
	log.Fatal(err)
}
var loose Spec
if err := doc.Get("/spec", &loose, oneyaml.KnownFields(false)); err != nil {
	log.Fatal(err)
}
fmt.Println(loose.Replicas)
// Output:
// true
// 2
```

A limit option on a read replaces the document's bound for that read, looser as well as tighter; inside an `UnmarshalYAML` hook it can only tighten. [Which options apply where](limits.md#which-options-apply-where) has the full table.

A file that repeats a key in a mapping still parses, so you can read and edit its other parts. Reading the part that holds the repeat reports it as `Unmarshal` does, and a pointer through the repeated key is refused with `ErrAmbiguousKey`.

## Reading while you edit

A `Node` is a view of the document as it was when you read it. An edit makes every `Node` read before it stale, so read, edit, then look the node up again. `Node.Find` returns an error that says why a key has no value (`ErrNotFound`, `ErrStaleNode`, `ErrAmbiguousKey`); `Node.Lookup` reports all of them as `false`. `Node.Err` tells a stale node from an empty value.

<!-- example: ExampleNode_Find_afterEdit -->
```go
doc, err := oneyaml.Parse([]byte("spec:\n  replicas: 2 # set by CI\n"))
if err != nil {
	log.Fatal(err)
}
spec, err := doc.Root().Find("spec")
if err != nil {
	log.Fatal(err)
}
replicas, err := spec.Find("replicas")
if err != nil {
	log.Fatal(err)
}
fmt.Println(replicas.Value())

if err := doc.Set("/spec/replicas", 3); err != nil {
	log.Fatal(err)
}

// spec was read before the edit, so it is stale.
_, err = spec.Find("replicas")
fmt.Println(errors.Is(err, oneyaml.ErrStaleNode))

// Look it up again from the root.
if spec, err = doc.Root().Find("spec"); err != nil {
	log.Fatal(err)
}
if replicas, err = spec.Find("replicas"); err != nil {
	log.Fatal(err)
}
fmt.Println(replicas.Value(), replicas.LineComment())
// Output:
// 2
// true
// 3 # set by CI
```

A `range` over `Items` or `Entries` may edit the document, as a loop over a Go map may change the map: after an edit it finds its collection again and goes on with fresh nodes.

<!-- example: ExampleNode_Entries_edit -->
```go
doc, err := oneyaml.Parse([]byte("images:\n  web: nginx:1.25\n  db: postgres:15\n"))
if err != nil {
	log.Fatal(err)
}
imgs, ok := doc.Root().Lookup("images")
if !ok {
	log.Fatal("no images")
}
for k, v := range imgs.Entries() {
	// Each Set makes the nodes read so far stale, but the loop goes on with fresh ones.
	if err := doc.Set(oneyaml.Pointer("images", k.Value()), v.Value()+"-patched"); err != nil {
		log.Fatal(err)
	}
}
fmt.Print(string(doc.Bytes()))
// Output:
// images:
//   web: nginx:1.25-patched
//   db: postgres:15-patched
```

An edit that adds or removes entries may make such a loop skip or repeat some. For those, collect the pointers with `Node.Pointer` first and edit afterwards, removing sequence items from the end so that the earlier indices stay put:

<!-- example: ExampleDocument_Remove_collected -->
```go
doc, err := oneyaml.Parse([]byte("tags:\n  - web\n  - old\n  - api\n  - old\n"))
if err != nil {
	log.Fatal(err)
}
tags, ok := doc.Root().Lookup("tags")
if !ok {
	log.Fatal("no tags")
}
var ptrs []string
for t := range tags.Items() {
	if t.Value() != "old" {
		continue
	}
	p, err := t.Pointer()
	if err != nil {
		log.Fatal(err)
	}
	ptrs = append(ptrs, p)
}
fmt.Println(ptrs)

// Remove from the end: /tags/3, then /tags/1. In the other order,
// removing /tags/1 would move "old" from /tags/3 to /tags/2.
for i := len(ptrs) - 1; i >= 0; i-- {
	if err := doc.Remove(ptrs[i]); err != nil {
		log.Fatal(err)
	}
}
fmt.Print(doc)
// Output:
// [/tags/1 /tags/3]
// tags:
//   - web
//   - api
```

A `Node` is not a decode target; read one with `Document.Get(pointer, &node)`.

## Errors

An edit that cannot be made returns an `*EditError` and leaves the document unchanged. It names the operation and the pointer, and `errors.Is` matches its kind, such as `ErrNotFound` or `ErrAliasWrite`. With a `MaxInputBytes` bound (which `Untrusted()` sets), an edit that would make the text longer than the bound is refused, and `errors.As` finds the `*LimitError` inside.

<!-- example: ExampleEditError -->
```go
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
```

## Streams and encodings

A file with several documents is one `Document`. `Doc(i)` is a view of document *i*, and edits through it change the stream.

<!-- example: ExampleDocument_Doc -->
```go
doc, err := oneyaml.Parse([]byte("a: 1\n---\nb: 2\n"))
if err != nil {
	log.Fatal(err)
}
if err := doc.Doc(1).Set("/c", 3); err != nil {
	log.Fatal(err)
}
fmt.Println(doc.Len())
fmt.Print(string(doc.Bytes()))
// Output:
// 2
// a: 1
// ---
// b: 2
// c: 3
```

UTF-16 and UTF-32 input is read and written back in its own encoding, with its byte order mark.
