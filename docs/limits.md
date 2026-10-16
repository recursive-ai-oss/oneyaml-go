# Limits

Limits stop input from using more time or memory than you intended. By default, nesting depth, alias expansion, the digits of a number and the work of a patch are bounded. Input size, documents per stream and scalar length are bounded only when you select `Untrusted()`; select it, and size it for your data, for any input you do not control. This page lists each limit, the presets, how options combine, and what the limits do not cover: your own hooks and the compat packages among them.

Every call that reads input is limited: `Unmarshal`, `Decoder`, `Parse` and the `Document` methods that follow aliases or apply patches. Aliases are counted wherever they are followed, so reading from a `Document` is bounded in the same way as decoding. `Get`, `Decode` and `Node.Decode` use the limits the `Document` was parsed with, and a limit option given to one of them changes the limits for that read only ([Reading values back](editing.md#reading-values-back)). The options are:

| Option | Effect |
|---|---|
| none | `DefaultLimits()`: generous bounds that real documents do not reach but that stop alias bombs and runaway nesting |
| `Untrusted()` | `UntrustedLimits()`: tighter bounds, and caps on input, scalar and stream size. Use it for input from a source you do not control |
| `Limits(LimitSet)` | changes only the fields you set, on top of the options before it (the defaults when there are none). A field left at zero keeps the bound it had, and `NoBound` (or any negative value) lifts that one bound. `Limits(LimitSet{})` changes nothing |
| `NoLimits()` | no bound at all, including depth when writing. Use it only for input you trust |

Options apply in order. `Untrusted()` and `NoLimits()` replace every bound; a `Limits` after them changes only the fields it sets. So `Untrusted(), Limits(LimitSet{MaxDepth: 50})` keeps every untrusted bound and tightens depth, and `NoLimits(), Limits(LimitSet{MaxInputBytes: 1 << 20})` bounds the input size and nothing else.

The presets side by side, as the code defines them. A field that shows 0 has no bound in that preset:

<!-- example: ExampleDefaultLimits -->
```go
fmt.Printf("%+v\n", oneyaml.DefaultLimits())
fmt.Printf("%+v\n", oneyaml.UntrustedLimits())
// Output:
// {MaxDepth:10000 MaxAliasNodes:1000000 MaxAliasBytes:67108864 MaxInputBytes:0 MaxScalarBytes:0 MaxDocuments:0 MaxNumberDigits:100000 MaxPatchWorkRatio:256}
// {MaxDepth:1000 MaxAliasNodes:100000 MaxAliasBytes:8388608 MaxInputBytes:67108864 MaxScalarBytes:16777216 MaxDocuments:10000 MaxNumberDigits:4300 MaxPatchWorkRatio:32}
```

## Which options apply where

One `Option` type serves every entry point, and an option that does not concern a call is ignored, never an error. The patch methods (`ApplyPatch`, `Apply`, `ApplyMergePatch`) take no options: they use the options the `Document` was parsed with. `Untrusted()` and `NoLimits()` set every `LimitSet` field, so they reach each entry point as those fields do.

| Option | `Unmarshal`, `Decoder` | `Parse` | `Get`, `Decode`, `Node.Decode` | Patches (options of `Parse`) | `Marshal`, `Encoder` |
|---|---|---|---|---|---|
| `KnownFields` | applies | kept for later reads | applies to this read | no effect | ignored |
| `MergeKeys` | applies | kept for reads, pointers and edits | applies to this read | `test` and the `from` of `copy` read merged keys | ignored |
| `YAML11Strings` | ignored | kept for edits | ignored | quotes new strings | ignored (`Marshal` always quotes them) |
| `Indent` | ignored | ignored (edits follow the file) | ignored | ignored | applies |
| `MaxDepth` | applies | applies, and kept | applies to this read | patch text and values | applies |
| `MaxAliasNodes`, `MaxAliasBytes` | applies | kept: parsing expands no alias | applies to this read | copies and written-out aliases | ignored |
| `MaxInputBytes` | applies; a `Decoder` counts the whole stream | applies, and kept for edits | no effect: the text is already parsed | patch text and the patched document | ignored |
| `MaxScalarBytes` | applies | applies, and kept | applies to this read | not checked on the values a patch writes | ignored |
| `MaxDocuments` | `Decoder` only: `Unmarshal` reads one document | applies | no effect: the text is already parsed | no effect | ignored |
| `MaxNumberDigits` | applies | kept: parsing converts no number | applies to this read | not checked on patch values; a later read of them is | numbers from a `MarshalJSON` method |
| `MaxPatchWorkRatio` | ignored | kept for patches | ignored | applies | ignored |

"Kept" means the `Document` remembers the option and uses it where the next columns say. A bound given to a read replaces the document's own for that read, looser as well as tighter; inside an `UnmarshalYAML` hook it can only tighten ([Reading values back](editing.md#reading-values-back)). `Set`, `Rename` and the comment setters follow the options of `Parse` as patches do, for `MaxDepth`, `MaxInputBytes`, `MergeKeys` and `YAML11Strings`.

## Each limit

| `LimitSet` field | What it bounds | If it is exceeded |
|---|---|---|
| `MaxDepth` | nesting of collections, in input and when writing | `*LimitError`, `Limit` is `LimitMaxDepth` |
| `MaxAliasNodes` | nodes produced by expanding aliases; with `MergeKeys()`, also the keys one pointer lookup, `Node.Find` or `Node.Lookup` reads in `<<` merge sources ([Merge keys](editing.md#merge-keys)) | `*LimitError`, also from `Node.Find`; `Node.Lookup` reports it as `false`, like an absent key, so use `Node.Find` to tell the two apart |
| `MaxAliasBytes` | scalar bytes produced by expanding aliases; also the bytes a patch copies | `*LimitError` |
| `MaxInputBytes` | the size of the input, and the size of a document after a patch, `Set`, `Rename` or a comment setter, counted in the encoding the document is written in (a UTF-16 document with its byte order mark, say); the text of one patch or merge patch may be an eighth of it, but never less than 64 KiB and never more than the limit ([Bounds](patches.md#bounds)) | `*LimitError` |
| `MaxScalarBytes` | the length of one scalar | `*LimitError` |
| `MaxDocuments` | documents in one stream | `*LimitError` |
| `MaxNumberDigits` | digits of an integer or float scalar when it is converted to a number, including a plain number used as a key in a part of the input the target ignores, because such keys are compared as numbers to find repeated keys; text kept as text is never limited | `*LimitError` |
| `MaxPatchWorkRatio` | text that one patch's edits may read again, as a multiple of the document plus the patch (and 16 MiB on top) | `*LimitError`, the document is unchanged |

An alias bound left at its default rises with the document: it is at least the figure shown above and at least ten times the document's own nodes or bytes (two times with `Untrusted`), so a large document that reuses its anchors is not refused. An alias bound you set with `Limits` applies exactly as given.

A bound that is hit is a `*LimitError`; an edit or patch returns it inside an `*EditError`, where `errors.As` and `errors.Is` still find it. `Limit` names the bound by its `LimitSet` field (`LimitMaxDepth`, `LimitMaxAliasNodes` and so on), `Max` holds it, and `What` describes it in words for messages. `Option` names the option and field that raise it, in the form `Limits (MaxDepth)`. It is empty for the three bounds that no option changes: the nesting of a patch value (10,000 levels), the nesting of typed Go values (100,000 levels) and the 2 GiB size of a `Document`. The compat packages take no limit options, so their limit errors have an empty `Option` too. `Path` holds the pointer to the value where the bound was hit, such as the alias, scalar or number being read. It is empty where there is no single value: the limits `Parse` reports, the input size, the document count, the nesting that the parser stops at, and the size of the text an edit leaves. `errors.Is` matches `ErrLimit` for any limit and a sentinel for each one, such as `ErrMaxDepth`, so a program need not read the fields to tell limits apart.

<!-- example: ExampleLimits -->
```go
bomb := "a: &a [x, x, x, x]\nb: &b [*a, *a, *a, *a]\nc: [*b, *b, *b, *b]\n"
var v any
err := oneyaml.Unmarshal([]byte(bomb), &v, oneyaml.Limits(oneyaml.LimitSet{MaxAliasNodes: 20}))
var le *oneyaml.LimitError
if errors.As(err, &le) {
	fmt.Println(le.Limit, le.Max)
}
// Output: MaxAliasNodes 20
```

To tell limits apart, match the sentinel or switch on `Limit`:

<!-- example: ExampleLimitError -->
```go
deep := strings.Repeat("[", 100) + strings.Repeat("]", 100)
err := oneyaml.Unmarshal([]byte(deep), new(any), oneyaml.Limits(oneyaml.LimitSet{MaxDepth: 50}))

fmt.Println(errors.Is(err, oneyaml.ErrLimit), errors.Is(err, oneyaml.ErrMaxDepth), errors.Is(err, oneyaml.ErrMaxInputBytes))
var le *oneyaml.LimitError
if errors.As(err, &le) {
	switch le.Limit {
	case oneyaml.LimitMaxDepth:
		fmt.Println("too deep:", le.Max, le.Option)
	}
}
// Output:
// true true false
// too deep: 50 Limits (MaxDepth)
```

## Changing one bound

A `LimitSet` passed to `Limits` changes only the fields you set. Here `MaxInputBytes` adds a bound on input size, and the default depth bound stays in force:

<!-- example: ExampleLimits_addBound -->
```go
opt := oneyaml.Limits(oneyaml.LimitSet{MaxInputBytes: 1 << 20})
large := []byte("a: " + strings.Repeat("x", 2<<20) + "\n")
deep := []byte(strings.Repeat("[", 10001) + strings.Repeat("]", 10001))
var v any
var le *oneyaml.LimitError

err := oneyaml.Unmarshal(large, &v, opt)
if errors.As(err, &le) {
	fmt.Println(le.Limit, le.Max)
}
err = oneyaml.Unmarshal(deep, &v, opt)
if errors.As(err, &le) {
	fmt.Println(le.Limit, le.Max)
}
// Output:
// MaxInputBytes 1048576
// MaxDepth 10000
```

`NoBound` lifts one bound and keeps the rest. Any negative value does the same; `NoBound` names it:

<!-- example: ExampleNoBound -->
```go
deep := []byte(strings.Repeat("[", 10001) + strings.Repeat("]", 10001))
var v any

err := oneyaml.Unmarshal(deep, &v, oneyaml.Limits(oneyaml.LimitSet{MaxDepth: oneyaml.NoBound}))
fmt.Println(err)
// Output: <nil>
```

`Limits` after `Untrusted()` keeps the untrusted bounds and changes only what it names. Here depth is tightened to 50, and the untrusted bound of 4300 digits on a number still applies:

<!-- example: ExampleUntrusted_tighten -->
```go
opts := []oneyaml.Option{oneyaml.Untrusted(), oneyaml.Limits(oneyaml.LimitSet{MaxDepth: 50})}
deep := strings.Repeat("[", 100) + strings.Repeat("]", 100)
long := strings.Repeat("9", 5000)
var v any
var le *oneyaml.LimitError

for _, in := range []string{deep, long} {
	err := oneyaml.Unmarshal([]byte(in), &v, opts...)
	if errors.As(err, &le) {
		fmt.Println(le.Limit, le.Max)
	}
}
// Output:
// MaxDepth 50
// MaxNumberDigits 4300
```

## Untrusted input

`Untrusted()` lowers the depth and alias bounds and adds a cap on input size, scalar size, documents per stream, the digits of a number, and the work and text size of a patch. In the example, 2000 nested sequences pass under the default limits and fail under `Untrusted()`:

<!-- example: ExampleUntrusted -->
```go
deep := strings.Repeat("[", 2000) + strings.Repeat("]", 2000)
var v any

// The default depth limit lets this through; Untrusted does not.
fmt.Println(oneyaml.Unmarshal([]byte(deep), &v))
err := oneyaml.Unmarshal([]byte(deep), &v, oneyaml.Untrusted())
var le *oneyaml.LimitError
if errors.As(err, &le) {
	fmt.Println(le.Limit, le.Max, le.Option)
}
// Output:
// <nil>
// MaxDepth 1000 Limits (MaxDepth)
```

## Loading a configuration file strictly

To load a configuration file and stop on anything unexpected, combine three things:

- `KnownFields()`, so that a misspelt key is an error instead of being skipped;
- `Untrusted()` with a `MaxInputBytes` sized for your files;
- the positions in the errors, which `TypeError.All` lists one per problem.

<!-- example: Example_strictConfig -->
```go
type Config struct {
	Name     string `yaml:"name"`
	Replicas int    `yaml:"replicas"`
}
src := []byte("name: api\nreplica: 3\nreplicas: two\n")

// Decode into a fresh value and use it only when there is no error: a read
// that reports mismatches has still filled the fields it could.
var cfg Config
err := oneyaml.Unmarshal(src, &cfg,
	oneyaml.KnownFields(), // a misspelt key is an error, not skipped
	oneyaml.Untrusted(),   // bounds on size, depth, aliases and numbers
	oneyaml.Limits(oneyaml.LimitSet{MaxInputBytes: 1 << 20}), // sized for a config file
)
var te *oneyaml.TypeError
switch {
case errors.As(err, &te):
	for _, e := range te.All() {
		fmt.Printf("line %d, column %d: %s\n", e.Line, e.Column, e.Msg)
	}
	fmt.Println("unknown field:", errors.Is(err, oneyaml.ErrUnknownField))
case err != nil:
	// A *SyntaxError has a position too, and a *LimitError names the bound.
	log.Fatal(err)
default:
	fmt.Println(cfg.Name, cfg.Replicas)
}
// Output:
// line 2, column 1: field "replica" not found in type oneyaml_test.Config
// line 3, column 11: cannot unmarshal !!str `two` into int
// unknown field: true
```

The compat packages take no options at all, so neither `KnownFields()` nor `Untrusted()` reaches them; `compat/k8syaml` has `UnmarshalStrict` and `compat/yamlv3` has `Decoder.KnownFields` for unknown keys. Bound the input yourself before you hand it over, with a length check or with `io.LimitReader`. Read one byte more than the bound and refuse input that reaches it, because a reader cut off at the bound can leave a shorter document that is still valid YAML:

<!-- example: Example_compatBoundedInput -->
```go
const maxBytes = 1 << 20
r := strings.NewReader("replicas: 3\n") // a file or a request body

data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
if err != nil {
	log.Fatal(err)
}
if len(data) > maxBytes {
	log.Fatal("input is larger than 1 MiB")
}
var cfg struct {
	Replicas int `json:"replicas"`
}
if err := yaml.Unmarshal(data, &cfg); err != nil {
	log.Fatal(err)
}
fmt.Println(cfg.Replicas)
// Output: 3
```

The example imports `yaml "oneyaml.dev/go/compat/k8syaml"`, `io` and `strings`. A length check bounds only the size: depth and alias expansion stay at the default limits, which the compat packages cannot tighten.

`Unmarshal` reads the first document and ignores the rest, and an empty input leaves the value unchanged with a nil error. To require exactly one document, read with `NewDecoder` and check that a second `Decode` returns `io.EOF`. To refuse an empty file, check the value or the input before you use it.

## Trusted input

`NoLimits()` removes every bound. Nesting that the default depth limit refuses is read once it is off:

<!-- example: ExampleNoLimits -->
```go
deep := strings.Repeat("[", 10001) + strings.Repeat("]", 10001)
var v any

err := oneyaml.Unmarshal([]byte(deep), &v)
var le *oneyaml.LimitError
fmt.Println(errors.As(err, &le))
fmt.Println(oneyaml.Unmarshal([]byte(deep), &v, oneyaml.NoLimits()))
// Output:
// true
// <nil>
```

## What limits do not cover

- **Memory held by a `Document`.** A `Document` keeps the text and an index of it. For ordinary files that is a few times the text; for dense input, such as long lists of tiny scalars, it is an order of magnitude more. Without `MaxInputBytes` nothing caps it, so set it (or use `Untrusted()`) for outside input.
- **Documents of 2 GiB or more.** A `Document` addresses its text with 32-bit offsets, so `Parse` refuses input of 2 GiB or more, and an edit or patch that would grow the text past that is refused. Both report `LimitMaxInputBytes` with an empty `Option`, because no option lifts this bound. Keep documents far below that.
- **`Marshal` output.** `Marshal` writes a value that is shared in several places once per place, as `encoding/json` does, so the output can be far larger than the value in memory. A value that contains itself is an error. When writing, `MaxDepth` bounds nesting and `MaxNumberDigits` bounds the numbers a `MarshalJSON` method returns; no other limit applies.
- **The compat packages.** `compat/yamlv3` and `compat/k8syaml` always run with the default limits and have no `Untrusted()` mode or other option to change them. For input from outside, read with the `oneyaml` package and `Untrusted()`.
- **Your own hooks.** An `Unmarshaler` that calls `Node.Decode`, or a func-style `UnmarshalYAML` that calls the function it is given, shares the alias budget of the call that invoked it and keeps its options, such as `KnownFields()` or a bound given to a `Document` read. Options a hook passes to `Node.Decode` can only tighten those bounds: for each one the stricter of the caller's and the hook's applies, so `NoLimits()` inside a hook does not lift them. The code you write in a hook, including `UnmarshalText` and `UnmarshalJSON`, is not limited.

The safety posture and how to report a problem are in [SECURITY.md](../SECURITY.md).
