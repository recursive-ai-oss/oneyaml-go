# Editing

How `Document` changes a YAML file and what it promises to leave alone. For the API reference see [pkg.go.dev](https://pkg.go.dev/oneyaml.dev/go#Document); for patches see [Patches](patches.md).

`Parse` returns a `Document` that holds the whole input. Until you edit it, `Bytes` returns exactly the bytes you gave it, including comments, blank lines, quoting, indentation and line endings. An edit changes only the value you set, in its own style; comments and formatting elsewhere are kept byte for byte.

You address a value with an [RFC 6901](https://www.rfc-editor.org/rfc/rfc6901) pointer: `/replicas`, `/tags/0`, `/tags/-` for the end of a sequence. A pointer token matches a mapping key by its text, so `/1` finds the key `1`.

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

Setters are exact: whatever you set, `Get` returns.

## When the style cannot hold the value

A value keeps its style when that style can hold the new value. When it cannot, only that value is rewritten, in the nearest style that can. Plainness is judged by the Core schema, because that is how the file will be read: `yes` stays plain, `true` is quoted. A string with a line break that replaces an inline scalar is written double-quoted.

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
```

(`Marshal`, which writes a whole value rather than editing one, is stricter: it quotes `yes` too, because other readers take it for a boolean. See the [package documentation](https://pkg.go.dev/oneyaml.dev/go#hdr-Marshal).)

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

## Comments

Comments outside the edited value are never touched. Comments inside a value you replace or remove go with it, because they described the old value. `Node.HeadComment` and `Node.LineComment` read comments as raw text; there is no API to edit them.

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

## Errors

An edit that cannot be made leaves the document unchanged and returns an `*EditError`. It names the operation and the pointer, and `errors.Is` matches its kind against `ErrNotFound`, `ErrAliasWrite`, `ErrAmbiguousKey`, `ErrAnchorInUse`, `ErrUnsupportedEdit` and the other sentinel errors.

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

## Reading while you edit

A `Node` is a view of the document as it was when you read it. An edit makes every `Node` read before it stale: `Stale` reports `true`, `Decode` and `Pointer` return an error matching `ErrStaleNode`, and the other methods return zero values. Zero values are easy to write back by mistake, so read a node again after an edit rather than keeping it.

A `range` over `Items` or `Entries` may edit the document, as a loop over a Go map may change the map. After an edit, the loop finds its collection again by the pointer it had before the edit (an edit that inserts or removes items of a list on the way to it moves the pointer along), and goes on at the next position with fresh nodes. The loop ends if the collection is removed or replaced, is no longer the same kind, or has no pointer. Entries added at the end during the loop are yielded, so a body that adds one on every pass never ends. Each Document follows at most three loops at once. A further loop, and any loop started after three loop bodies on that Document have panicked and been recovered, still runs, but ends at its first edit. Changing values as you go is the simple case:

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
	if err := doc.Set("/images/"+k.Value(), v.Value()+"-patched"); err != nil {
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

// First collect where each value is, then edit. This works whatever the
// edits do, including adding or removing entries.
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

More in [Migrating from yaml.v3](migrate-yaml-v3.md).
