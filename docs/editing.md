# Editing

How `Document` changes a YAML file and what it promises to leave alone. For the API reference see [pkg.go.dev](https://pkg.go.dev/oneyaml.dev/go#Document); for patches see [Patches](patches.md).

`Parse` returns a `Document` that holds the whole input. Until you edit it, `Bytes` returns exactly the bytes you gave it, including comments, blank lines, quoting, indentation and line endings. An edit changes only the value you set, in its own style; comments and formatting elsewhere are kept byte for byte. The same holds for the edits that change a key ([Renaming a key](#renaming-a-key)) or a comment ([Comments](#comments)). `fmt.Print(doc)` prints the document as it stands, because `Document` and `Node` have a `String` method.

You address a value with an [RFC 6901](https://www.rfc-editor.org/rfc/rfc6901) pointer: `/replicas`, `/tags/0`, `/tags/-` for the end of a sequence. A pointer token matches a mapping key by its text, so `/1` finds the key `1`.

In a pointer, `~` is written `~0` and `/` is written `~1`, so the key `app.kubernetes.io/name` is the token `app.kubernetes.io~1name`. When a key comes from data, build the pointer with `oneyaml.Pointer` rather than with `"/" + key`, so that each token is escaped. `Node.Pointer` returns the same form for a node you have read.

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

## Two block-scalar cases

Two cases illustrate the rules. In the first, the new value ends in a line break, so the block literal keeps its style and only its text changes:

<!-- example: Example_blockLiteralKeepsStyle -->
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
```

In the second, the new value has no final line break. Reading the document back must give exactly that value, so the header changes from `|` to `|-` and nothing else does:

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

Setters are exact: whatever you set, `Get` returns, because this package reads the file with the YAML 1.2 Core schema. Programs that read YAML with YAML 1.1 rules, such as Kubernetes tooling built on sigs.k8s.io/yaml and code on yaml.v2, may read a new value differently: to them a plain `yes` is a boolean and `0755` is octal. If such programs also read the file, parse it with `YAML11Strings()` ([below](#when-the-style-cannot-hold-the-value)).

## When the style cannot hold the value

A value keeps its style when that style can hold the new value. When it cannot, only that value is rewritten, in the nearest style that can. Plainness is judged by the Core schema, because that is how this package reads the file: `yes` stays plain, `true` is quoted. A string with a line break that replaces an inline scalar is written double-quoted.

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

A YAML 1.1 reader takes a plain `yes`, `on`, `0755` or `12:30` for a boolean, a number or a time. If the file is also read by such programs (Kubernetes tooling built on sigs.k8s.io/yaml, or code on yaml.v2), pass `YAML11Strings()` to `Parse`. New strings written by `Set`, patches and merge patches, and keys written by `Rename`, then follow the stricter rule that `Marshal` always uses: a string stays plain only when YAML 1.1 readers read the same string too. An existing quoted or block style is kept, and values you do not edit are not touched.

<!-- example: ExampleYAML11Strings -->
```go
src := []byte("debug: false\nname: x\n")

// By default the Core schema decides: "yes" is a string there, so it stays plain.
doc, err := oneyaml.Parse(src)
if err != nil {
	log.Fatal(err)
}
if err := doc.Set("/debug", "yes"); err != nil {
	log.Fatal(err)
}
fmt.Print(doc)

// With YAML11Strings, a string that YAML 1.1 reads as a boolean is quoted.
doc, err = oneyaml.Parse(src, oneyaml.YAML11Strings())
if err != nil {
	log.Fatal(err)
}
if err := doc.Set("/debug", "yes"); err != nil {
	log.Fatal(err)
}
if err := doc.Set("/name", "hello"); err != nil {
	log.Fatal(err)
}
fmt.Print(doc)
// Output:
// debug: yes
// name: x
// debug: 'yes'
// name: hello
```

## Adding and removing

`Set` on a key that does not exist adds it at the end of its mapping, in the style of its neighbours: the same quoting, the same indentation, block or flow. `Set` at `/-`, or at the index equal to the length, appends to a sequence. A null that is already in the file keeps its spelling.

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

`Remove` deletes an entry together with the comment on its line, the comment directly above it and any comment inside its value. A comment above the next entry stays.

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

Removing the root is refused.

## Renaming a key

`Rename(ptr, newKey)` changes the key of the entry at `ptr` where it stands. The value, the entry's place in its mapping, its comments, and an anchor or tag on the key stay as they are. The new key keeps the old key's style (plain, single- or double-quoted) when that style can hold it. Otherwise it gets the nearest style that can: single quotes for a plain key that needs quoting (and, with `YAML11Strings()`, for one that YAML 1.1 readers would take for something else, so renaming to `on` writes `'on'`), double quotes for one with a line break or an unprintable character. The new key is always a string, so renaming the key `1` to `"2"` writes `'2'`.

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

`Rename` refuses, and leaves the document unchanged, when:

- the mapping already has the new key (`ErrKeyExists`);
- the pointer is the root or an item of a sequence (`ErrNotAnEntry`);
- the pointer leads through an alias (`ErrAliasWrite`). Renaming an entry whose value is an alias is allowed;
- the key occurs twice in its mapping (`ErrAmbiguousKey`);
- the key carries an anchor that an alias uses (`ErrAnchorInUse`), because the alias would read the new key too. This is the same rule as for `Remove`. An anchor on the value does not count;
- the key is empty, a collection, an alias or a block scalar, or is the merge key `<<`; the new key is longer than 1000 bytes or is not valid UTF-8; or a tag on the key does not fit the new key (`ErrUnsupportedEdit`).

Like `Set`, a rename parses again only the entries around the key where it can, rather than the whole document; inside a flow collection, for one, it parses the whole document again. To check that the new key is not already there, it uses an index of the mapping's keys, built the first time it is needed and kept up to date by later renames, so a run of renames through one wide mapping does not read every key again for each rename.

## Comments

Comments outside the edited value are never touched. Comments inside a value you replace or remove go with it, because they described the old value.

`Node.HeadComment` returns the comment lines directly above an entry or item, and `Node.LineComment` the comment after a value on its line. Both return trimmed text, as yaml.v3 does: each line starts with its `#`, without indentation or trailing blanks, and the lines are joined by line feeds. The line comment of an entry whose value is a block collection or empty is the one after its colon or dash. `Node.Source` still gives the exact text of a value.

`SetHeadComment(ptr, text)` replaces the comment lines above the entry or item at `ptr`, and an empty text removes them. A line without a leading `#` gets `# ` in front, a blank line inside the text becomes a bare `#`, and the lines are written at the entry's indentation. The root pointer `""` sets the comment above the first content line of the document, below a `---` marker line.

<!-- example: ExampleDocument_SetHeadComment -->
```go
doc, err := oneyaml.Parse([]byte(`service:
  # old note
  port: 80
  host: example.org
`))
if err != nil {
	log.Fatal(err)
}
if err := doc.SetHeadComment("/service/port", "the public port"); err != nil {
	log.Fatal(err)
}
if err := doc.SetHeadComment("/service/host", "first line\nsecond line"); err != nil {
	log.Fatal(err)
}
fmt.Print(doc)

if err := doc.SetHeadComment("/service/host", ""); err != nil {
	log.Fatal(err)
}
fmt.Print(doc)
// Output:
// service:
//   # the public port
//   port: 80
//   # first line
//   # second line
//   host: example.org
// service:
//   # the public port
//   port: 80
//   host: example.org
```

`SetLineComment(ptr, text)` replaces the comment after the value at `ptr`, and an empty text removes it. The comment goes after a scalar, after the closing bracket of a flow collection, after the header of a block scalar, or after the colon or dash of an entry whose value is a block collection or empty:

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

Some places cannot hold a comment, and the setters refuse them with `ErrUnsupportedEdit`:

- a head comment for anything inside a flow collection;
- a line comment inside a flow collection, unless nothing, not even a comma, follows the item on its line, as for the last item of a flow collection written one item per line;
- a head comment for an entry that shares its line with another, such as the first key of a mapping written after `- `;
- a line comment for the root block collection, or for a value that something else follows on its line.

Text that is not valid UTF-8, that holds a control character or a line separator other than the line feed, or, for a line comment, that holds a line break, is refused with `ErrInvalidComment`. In every case the document reads back with the same values, and only the comment lines change.

Like `Set` and `Rename`, the comment setters parse again only the entries around the comment where they can, so a run of comment edits in a large document does not read the whole document for each one. Inside a flow collection, for one, they parse the whole document again.

## Anchors, aliases and tags

An anchor stays on a replaced value, so every alias reads the new value. A tag stays when the new value reads back as exactly that value under it, and is dropped otherwise. Replacing an alias node itself writes the new value in its place.

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

Two edits are refused because they would change a value you did not address:

- Writing below an alias (`/copy/x` when `copy` is `*b`) would change every place that shares the anchored node. Edit the anchored node instead.

<!-- example: ExampleDocument_Set_alias -->
```go
doc, err := oneyaml.Parse([]byte("base: &b\n  x: 1\ncopy: *b\n"))
if err != nil {
	log.Fatal(err)
}
err = doc.Set("/copy/x", 2)
fmt.Println(errors.Is(err, oneyaml.ErrAliasWrite))
err = doc.Set("/base/x", 2)
fmt.Println(err)
fmt.Print(string(doc.Bytes()))
// Output:
// true
// <nil>
// base: &b
//   x: 2
// copy: *b
```

- Removing an anchored node that an alias still uses would leave the alias without a target.

<!-- example: ExampleDocument_Remove_anchorInUse -->
```go
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
```

## Merge keys

With `MergeKeys()`, a pointer also finds the keys that a `<<` merge brings into a mapping, with the precedence decoding gives them: a key the mapping has itself wins, and an earlier merge source wins over a later one. `Get`, the patch `test` operation, the `from` of a patch `copy`, `Node.Find` and `Node.Lookup` read such keys. `Entries`, `Items` and `Len` describe the mapping as written, and `Get` of the merge key itself (`/web/<<`) reads the merge source as written.

A merged key belongs to its merge source, and changing it there would change every mapping that merges the source, as a write through an alias would. So:

- `Set` on a key that only a merge source has adds an explicit key to the mapping, which overrides the merged one;
- `Remove`, the patch operations `remove`, `replace` and `move`, and a merge patch member `null`, of such a key are refused with `ErrAliasWrite`, as is any write below it (`/web/image/tag`) or through the merge key (`/web/<</image`). A patch `add` or a merge patch member that sets such a key adds an explicit key, as `Set` does;
- a patch `copy` reads its `from` as `test` does, so a `copy` from such a key copies the merged value and leaves the merge source unchanged.

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

Merge sources are searched depth-first, each mapping once, so a source that merges itself, directly or through others, does not loop. The keys a search reads count against `MaxAliasNodes` ([Limits](limits.md#each-limit)). A search past it fails with a `*LimitError`, whether it serves `Get`, `Node.Find`, a patch or an edit. `Node.Lookup` reports such a search as `false`, the same as an absent key, so use `Node.Find` when the difference matters, as before you fall back to a default. Adding a new key with `Set` does not search merge sources.

<!-- example: ExampleNode_Find -->
```go
// With MergeKeys, a search through merge sources is bounded by MaxAliasNodes.
// Find tells a refused search from an absent key; Lookup reports both as false.
src := "base: &base {a: 1, b: 2, c: 3, d: 4}\nuse:\n  <<: *base\n  own: 5\n"
limits := oneyaml.DefaultLimits()
limits.MaxAliasNodes = 2
doc, err := oneyaml.Parse([]byte(src), oneyaml.MergeKeys(), oneyaml.Limits(limits))
if err != nil {
	log.Fatal(err)
}
var use oneyaml.Node
if err := doc.Get("/use", &use); err != nil {
	log.Fatal(err)
}
own, err := use.Find("own")
fmt.Println(own.Value(), err)
// Neither "missing" nor "d" is a key of use itself, so both need the merge
// search, which reads more keys than the bound allows. The answer is the
// *LimitError, not ErrNotFound: whether the key exists is unknown.
_, err = use.Find("missing")
fmt.Println(errors.Is(err, oneyaml.ErrNotFound), errors.As(err, new(*oneyaml.LimitError)))
_, err = use.Find("d")
fmt.Println(errors.Is(err, oneyaml.ErrNotFound), errors.As(err, new(*oneyaml.LimitError)))
// Output:
// 5 <nil>
// false true
// false true
```

Without `MergeKeys()`, `<<` is an ordinary key, and pointers address only the keys as written. The edits, patches, `Node.Find` and `Node.Lookup` follow the option given to `Parse`; `Get` and `Decode` can also take `MergeKeys()` or `MergeKeys(false)` for one read ([Reading values back](#reading-values-back)).

## Errors

An edit that cannot be made leaves the document unchanged and returns an `*EditError`. It names the operation and the pointer, and `errors.Is` matches its kind against `ErrNotFound`, `ErrAliasWrite`, `ErrAmbiguousKey`, `ErrAnchorInUse`, `ErrUnsupportedEdit`, `ErrKeyExists`, `ErrNotAnEntry`, `ErrInvalidComment` and the other sentinel errors.

When the document was parsed with a bound on input size (`MaxInputBytes`, which `Untrusted()` sets), `Set`, `Rename`, `SetHeadComment`, `SetLineComment` and patches refuse an edit that would make the text longer than the bound, counted in the encoding the document is written in. `errors.As` finds the `*LimitError` inside the `*EditError` ([Limits](limits.md#each-limit)). An edit that makes the text shorter is always allowed.

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

## Reading values back

`Get(ptr, &v)` reads the value at a pointer into any target `Unmarshal` accepts, `Decode(&v)` reads the whole document, and `Node.Decode` reads the part below a node. Options given to one of these reads apply to that call only, on top of the options the document was parsed with, so one file can be read strictly in one place and loosely in another. `KnownFields`, `MergeKeys`, `Limits`, `Untrusted` and `NoLimits` take effect, also in the decodes an `UnmarshalYAML` hook makes during the read. A bound given to a read replaces the document's own for that read, looser as well as tighter, so `NoLimits()` on a document parsed with `Untrusted()` lifts its bounds; pass such options only for input you trust. The size bounds that concern parsing (`MaxInputBytes`, `MaxDocuments`) do nothing to a document that is already parsed, and options that concern writing are ignored. `KnownFields`, `MergeKeys` and `YAML11Strings` take an optional bool, and the last option wins, so `KnownFields(false)` turns off an earlier `KnownFields()`:

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

A file whose mapping repeats a key still parses, so you can read and edit its other parts. Reading a part that holds the repeated key reports it, as `Unmarshal` does, and a pointer through the repeated key is refused with `ErrAmbiguousKey`, by `Get` and by every edit. `Node.Find` returns an error matching `ErrAmbiguousKey` for such a key and `Node.Lookup` reports `false`, so a lookup agrees with `Get`; `Entries` and `Items` still yield every entry, repeats included.

## Reading while you edit

A `Node` is a view of the document as it was when you read it. An edit makes every `Node` read before it stale: `Stale` reports `true`, `Decode` and `Pointer` return an error matching `ErrStaleNode`, and the other methods return zero values. A zero value looks like an answer (`Value` of a stale node is `""`, the same as an empty scalar), so `Node.Err` is the check: it is `nil` for a node you can read, matches `ErrStaleNode` for a stale one, and matches `ErrNotFound` for the zero `Node`, such as the one a failed `Lookup` returns. To look up a key, use `Node.Find(key)`: it returns the value and a `nil` error, or an error that says why there is no value. The error matches `ErrNotFound` when the key is absent, `ErrStaleNode` when the node was read before an edit, and `ErrAmbiguousKey` when the mapping has the key more than once (the same refusal `Get` gives for a pointer through it) or when a merge brings the key in from two places of one source, and it is a `*LimitError` when a merge search was refused ([Merge keys](#merge-keys)). `Node.Lookup` is the short form for when any miss means "no value": it returns `false` in all of these cases, including a key written twice. `Entries` and `Items` still yield every entry, repeats included. Reading the node again after an edit is still the better habit.

A `Node` can be compared with `==` and used as a map key. A node read before an edit never equals the node read at the same place after it, because the first one is stale; the [`Node` reference](https://pkg.go.dev/oneyaml.dev/go#Node) gives the rule.

<!-- example: ExampleNode_Err -->
```go
doc, err := oneyaml.Parse([]byte("title: ''\nsize: 3\n"))
if err != nil {
	log.Fatal(err)
}
var title oneyaml.Node
if err := doc.Get("/title", &title); err != nil {
	log.Fatal(err)
}
fmt.Printf("%q %v\n", title.Value(), title.Err())

if err := doc.Set("/size", 4); err != nil {
	log.Fatal(err)
}
// The edit made title stale. Value is "" again, as for an empty scalar,
// and only Err says the node can no longer be read.
fmt.Printf("%q %v\n", title.Value(), errors.Is(title.Err(), oneyaml.ErrStaleNode))
// Output:
// "" <nil>
// "" true
```

A `range` over `Items` or `Entries` may edit the document, as a loop over a Go map may change the map. After an edit, the loop finds its collection again by the pointer it had before the edit (an edit that inserts or removes items of a list on the way to it moves the pointer along), and goes on at the next position with fresh nodes. The loop ends if the collection is removed or replaced, is no longer the same kind, or has no pointer. Entries added at the end during the loop are yielded, so a body that adds one on every pass never ends. Any number of loops, over one collection or several, can follow edits at once. Changing values as you go is the simple case:

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

An edit that adds or removes entries or items moves the later ones, so the loop may skip some or see some twice, as Go allows for a map. For those edits, collect the places first and edit afterwards. `Node.Pointer` gives the RFC 6901 pointer of a node, ready for `Set`, `Get` and `Remove`:

<!-- example: ExampleNode_Pointer -->
```go
doc, err := oneyaml.Parse([]byte("images:\n  web: nginx:1.25\n  db: postgres:15\n"))
if err != nil {
	log.Fatal(err)
}
imgs, ok := doc.Root().Lookup("images")
if !ok {
	log.Fatal("no images")
}

// First collect where each value is, then edit. Replacing values moves
// nothing, so every pointer still names the same value.
var ptrs, vals []string
for _, v := range imgs.Entries() {
	p, err := v.Pointer()
	if err != nil {
		log.Fatal(err)
	}
	ptrs = append(ptrs, p)
	vals = append(vals, v.Value())
}
// Then edit by pointer.
for i, p := range ptrs {
	if err := doc.Set(p, vals[i]+"-patched"); err != nil {
		log.Fatal(err)
	}
}
fmt.Print(string(doc.Bytes()))

// Nodes read before the edits are stale now.
_, err = imgs.Pointer()
fmt.Println(imgs.Stale(), errors.Is(err, oneyaml.ErrStaleNode))
// Output:
// images:
//   web: nginx:1.25-patched
//   db: postgres:15-patched
// true true
```

A pointer is an address, not a name for one value. A sequence pointer holds an index, so removing or inserting an item changes what the later indices point to. A rename or a removal higher up changes what every pointer below it addresses. Collected pointers are safe for edits that replace values. To remove several items, remove them from the end of the sequence first, so that each removal leaves the indices before it unchanged:

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

A patch does not change this. Each operation applies to the result of the ones before it, as RFC 6902 says, so a patch that removes items lists them from the end too. What a patch adds is that the removals happen all or nothing.

`Pointer` returns an error matching `ErrNoPointer`, and never a guess, when no pointer leads to the node: a mapping key, anything inside a collection used as a key, or a value whose key occurs twice in its mapping. An alias node has the pointer of the alias itself.

A `Node` is not a decode target, because it is a view of a `Document` rather than a value. `Unmarshal` into a `Node` returns a `*TypeError`; read a node with `Document.Get(pointer, &node)`. `Marshal` writes a `Node` as the value it holds:

<!-- example: ExampleDocument_Get_node -->
```go
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
```

## Streams and encodings

A file with several documents is one `Document`. `Doc(i)` is a view of document *i* that shares the stream, and edits through it change the stream.

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

UTF-16 and UTF-32 input is read, and the document is written back in its own encoding and byte order mark.

## Coming from yaml.v3

yaml.v3 changes a document by walking a `yaml.Node` tree and encoding the whole tree again, so the encoder decides the indentation and layout rather than your file. The equivalent here is one call:

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

Renaming a key and changing a comment work the same way: where yaml.v3 code changes a key node's `Value` or a node's `HeadComment` and encodes the tree again, call [`Rename`](#renaming-a-key) or [`SetHeadComment` and `SetLineComment`](#comments). More in [Migrating from yaml.v3](migrate-yaml-v3.md).
