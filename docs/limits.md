# Limits

How to bound the time and memory that YAML input can cost you, and which bound does what.

By default, nesting depth, alias expansion, the digits of a number and the work of a patch are bounded. `Untrusted()` tightens those and also bounds input size, scalar length and documents per stream; use it, sized for your data, for any input you do not control.

| Option | Effect |
|---|---|
| none | `DefaultLimits()`: bounds that real documents do not reach but that stop alias bombs and runaway nesting |
| `Untrusted()` | `UntrustedLimits()`: tighter bounds, plus caps on input, scalar and stream size |
| `Limits(LimitSet)` | changes only the fields you set; `NoBound` (any negative value) lifts one bound |
| `NoLimits()` | no bound at all. Only for input you trust |

Options apply in order, so `Untrusted(), Limits(LimitSet{MaxDepth: 50})` keeps every untrusted bound and tightens depth. The presets, where 0 means no bound:

<!-- example: ExampleDefaultLimits -->
```go
fmt.Printf("%+v\n", oneyaml.DefaultLimits())
fmt.Printf("%+v\n", oneyaml.UntrustedLimits())
// Output:
// {MaxDepth:10000 MaxAliasNodes:1000000 MaxAliasBytes:67108864 MaxInputBytes:0 MaxScalarBytes:0 MaxDocuments:0 MaxNumberDigits:100000 MaxPatchWorkRatio:256}
// {MaxDepth:1000 MaxAliasNodes:100000 MaxAliasBytes:8388608 MaxInputBytes:67108864 MaxScalarBytes:16777216 MaxDocuments:10000 MaxNumberDigits:4300 MaxPatchWorkRatio:32}
```

## Untrusted input

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

## Each limit

| `LimitSet` field | What it bounds |
|---|---|
| `MaxDepth` | nesting of collections, in input and when writing |
| `MaxAliasNodes` | nodes produced by expanding aliases; with `MergeKeys()`, also the keys a lookup reads in `<<` merge sources |
| `MaxAliasBytes` | scalar bytes produced by expanding aliases, and the bytes a patch copies |
| `MaxInputBytes` | the size of the input, and of the document after an edit or patch, in the document's own encoding |
| `MaxScalarBytes` | the length of one scalar |
| `MaxDocuments` | documents in one stream |
| `MaxNumberDigits` | digits of a scalar converted to a number, including plain number keys in parts the target ignores |
| `MaxPatchWorkRatio` | text one patch's edits may read again, as a multiple of the document plus the patch ([Bounds](patches.md#bounds)) |

An alias bound left at its default grows with the document: it is never less than ten times the document's own nodes or bytes (two times with `Untrusted()`), so a large document that reuses its anchors is not refused. A bound you set applies exactly.

A bound that is hit is a `*LimitError`, inside an `*EditError` for an edit or patch. `Limit` names the field (`LimitMaxDepth`), `Max` holds the bound, `Option` names the option that raises it, and `Path` points to the value where it was hit, when there is one. `errors.Is` matches `ErrLimit` and a sentinel for each limit:

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

## Which options apply where

An option that does not concern a call is ignored. Patches take no options of their own: they use those given to `Parse`, as `Set`, `Rename` and the comment setters do. A limit given to `Get`, `Decode` or `Node.Decode` applies to that read only ([Reading values back](editing.md#reading-values-back)).

| Option | `Unmarshal`, `Decoder` | `Parse` | `Get`, `Decode`, `Node.Decode` | Patches | `Marshal`, `Encoder` |
|---|---|---|---|---|---|
| `KnownFields` | applies | kept for later reads | applies | no effect | ignored |
| `MergeKeys` | applies | kept for reads, pointers and edits | applies | `test` and `copy` `from` read merged keys | ignored |
| `YAML11Strings` | ignored | kept for edits | ignored | quotes new strings | ignored (`Marshal` always quotes) |
| `Indent` | ignored | ignored (edits follow the file) | ignored | ignored | applies |
| `MaxDepth` | applies | applies, and kept | applies | patch text and values | applies |
| `MaxAliasNodes`, `MaxAliasBytes` | applies | kept | applies | copies and written-out aliases | ignored |
| `MaxInputBytes` | applies; a `Decoder` counts the whole stream | applies, and kept for edits | no effect | patch text and result | ignored |
| `MaxScalarBytes` | applies | applies, and kept | applies | not checked on written values | ignored |
| `MaxDocuments` | `Decoder` only | applies | no effect | no effect | ignored |
| `MaxNumberDigits` | applies | kept | applies | not checked on written values | numbers from `MarshalJSON` |
| `MaxPatchWorkRatio` | ignored | kept for patches | ignored | applies | ignored |

## Loading a configuration file strictly

Combine `KnownFields()`, so a misspelt key is an error, with `Untrusted()` and a `MaxInputBytes` sized for your files. `TypeError.All` lists every problem with its position:

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

`Unmarshal` reads the first document and leaves the value unchanged for empty input. To require exactly one document, read with `NewDecoder` and check that a second `Decode` returns `io.EOF`.

The compat packages take no options and always run with the default limits. Bound their input yourself; read one byte past the bound, because a reader cut off at the bound can leave a shorter document that is still valid YAML:

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

Here `yaml` is `oneyaml.dev/go/compat/k8syaml`.

## What limits do not cover

- **Memory held by a `Document`.** It keeps the text and an index of it, an order of magnitude more than the text for dense input. Set `MaxInputBytes` for outside input.
- **Documents of 2 GiB or more**, which `Parse` refuses and no option allows.
- **`Marshal` output.** A value shared in several places is written once per place, as `encoding/json` does, so the output can be far larger than the value in memory.
- **Your own hooks.** A hook's `Node.Decode` shares the caller's bounds and can only tighten them, but the code you write in a hook is not limited.

[SECURITY.md](../SECURITY.md) has the safety promises and how to report a problem.
