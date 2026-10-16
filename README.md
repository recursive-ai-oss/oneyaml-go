# oneyaml-go

**Pure-Go YAML 1.2 that edits a file and keeps its comments.**

[![Go Reference](https://pkg.go.dev/badge/oneyaml.dev/go.svg)](https://pkg.go.dev/oneyaml.dev/go)
[![CI](https://github.com/recursive-ai-oss/oneyaml-go/actions/workflows/ci.yml/badge.svg)](https://github.com/recursive-ai-oss/oneyaml-go/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/badge/go-1.24%2B-00ADD8)](#stability)
[![License](https://img.shields.io/badge/license-MIT%20OR%20Apache--2.0-blue)](#license)

YAML 1.2 with the Core schema by default; the conformance results and the known deviations are under [Correctness](#correctness). Edits change only the value you set, in its own style; comments and formatting elsewhere are kept byte for byte. Nesting, alias expansion, number size and patch work are bounded by default.

Time per task for oneyaml-go and for the strongest Go competitor whose answer passed the benchmark's check, on the real-world files of each task. Each row says how many times faster oneyaml-go was: the competitor's time divided by ours, so higher is better. The method line under the table names the library commit that was measured, the date of the run and the machine it ran on; [About this run](docs/performance.md#about-this-run) says which release that commit is. The figures are measured, not bounds, and later code may differ.

<!-- generated:bench-hero -->
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/img/oneyaml-go-hero-dark.svg">
  <img alt="Bar chart of the table below: how many times faster oneyaml-go is than the strongest competitor on each task, log scale, higher is better. 7 tasks: 7 better, 0 level, 0 mixed, 0 worse, 0 not comparable." src="docs/img/oneyaml-go-hero-light.svg">
</picture>

| Task | `oneyaml-go` time (geometric mean) | Strongest competitor | Its time (geometric mean) | Speed against it (its time ÷ ours; higher is better) | Verdict |
|---|---|---|---|---|---|
| typed load | 121 µs | `yaml-v2 v2.4.0` | 234 µs | 1.94× faster | ▲ win |
| generic load | 48.1 µs | `yaml-v2 v2.4.0` (6 of 7), `yaml-v3 v3.0.5` (1 of 7) | 135 µs | 2.79× faster | ▲ win |
| dump | 9.5 µs | `yaml-v2 v2.4.0` (5 of 6), `yaml-v4 v4.0.0-rc.6` (1 of 6) | 114 µs | 12.0× faster | ▲ win |
| document parse | 86.3 µs | `yaml-v3 v3.0.5` (2 of 3), `yaml-v4 v4.0.0-rc.6` (1 of 3) | 415 µs | 4.81× faster | ▲ win |
| unchanged emit (write-back of an unedited document; not a rendering speed) | 4.31 ns | `goccy-go-yaml v1.19.2` | 186 µs | 43,100× faster | ▲ win |
| edit script | 13.7 µs | `yaml-v3 v3.0.5` | 284 µs | 20.7× faster | ▲ win |
| patch (RFC 6902) | 15.7 µs † (text API) | `json-patch v5.9.11` † (json-patch: text API, value-only) | 92.1 µs | 5.87× faster | ▲ win |

Not in a row's mean: document parse: no competitor answer: `compose-wordpress`, `gha-workflow`, `openapi-api-with-examples`, `prometheus-config`; unchanged emit (write-back of an unedited document; not a rendering speed): no competitor answer: `compose-wordpress`, `gha-workflow`, `openapi-api-with-examples`, `prometheus-config`; edit script: no competitor answer: `compose-wordpress`, `gha-workflow`, `openapi-api-with-examples`, `prometheus-config`; patch (RFC 6902): no competitor answer: `gha-workflow`.

Each row is the geometric mean, over the real-world workloads the task has, of our time and of the time of the strongest competitor in each workload. Speed is the competitor's time divided by ours: 2× faster means half the time, and a loss is stated as slower. Where several competitors were strongest the column names them all, with how often. A † marks a cell measured differently from the task as specified; the note beside each mark says how. A marked competitor still counts. A workload where our library has no number is listed and the row is marked as partial, never counted as a win. The verdict is never better than the per-workload verdicts allow: a row with a losing workload is marked mixed or loss, and a row with no winning workload is never marked a win. Memory and the count of better, level and worse workloads in each row are on the full page.

Method: **full run** of 2026-10-16T21:48:45Z. Kit revision 0abba1d8b4f2. `oneyaml-go eea67bb6b962`; competitors `yaml-v3 v3.0.5`, `yaml-v4 v4.0.0-rc.6`, `yaml-v2 v2.4.0`, `sigs-yaml v1.6.0`, `goccy-go-yaml v1.19.2`, `palantir-yamlpatch v1.5.0`, `palantir-yamlpatch-goccy v1.5.0`, `json-patch v5.9.11`. Runtime go1.26.0 (target linux/amd64), measured with testing.B: ns/op from testing.Benchmark at benchtime 1s. Memory: B/op and allocs/op from testing.B (ReportAllocs). AMD Ryzen 9 7950X3D 16-Core Processor (125.0 GiB), Linux 7.0.0-34-generic (x86_64), measured on a shared workstation (32 logical CPUs; load 2.67 before, 4.06 after, at most 11.23 during).
<!-- end generated -->

Every workload, the memory figures, what the unchanged-emit row does and does not measure, and how to reproduce the run: [Performance](docs/performance.md).

- **Measured against the other Go YAML libraries.** Typed and generic loads, dumps, document parsing, writing an unedited document, edits and patches are each timed against the strongest competitor whose answer passed the benchmark's check. The table above gives the verdict for each task, and [Performance](docs/performance.md) has every workload with its time and memory. Rows where no competitor answered correctly have no comparison and are [not counted as wins](docs/performance.md#rows-with-no-rival).
- **YAML 1.2 Core schema by default.** Plain scalars follow the Core schema, so plain `yes` is a string, plain `0755` is the integer 755, and quoted `"0755"` is a string. YAML 1.1 forms come only from the [compat packages](#compatibility-adapters-for-yamlv3-and-sigsk8sioyaml). Results against the yaml-test-suite, and the known deviations, are under [Correctness](#correctness).
- **Minimal-diff edits.** `Document.Set` rewrites one value in its own style and falls back to the nearest style that can hold the new value. Reading it back with this package always gives exactly the value you set. Plainness follows the Core schema, so a new plain `yes` is a string here but a boolean to a YAML 1.1 reader; pass [`YAML11Strings()`](docs/editing.md#when-the-style-cannot-hold-the-value) when such programs also read the file. `Rename` changes a key in place, and `SetHeadComment` and `SetLineComment` write comments. The benchmark checks every edit byte for byte [before it times it](docs/performance.md#the-correctness-check-before-timing). See [Editing](docs/editing.md).
- **Patches.** RFC 6902 JSON Patch operations, RFC 6901 pointers and RFC 7396 merge patches, applied to a YAML file with YAML's data model: `test` compares YAML values, so the integer `1` and the float `1.0` [differ](docs/patches.md#yaml-rules-at-a-glance) where RFC 6902 would call them equal, and a write through an alias is refused. A patch applies atomically and keeps the file's layout. See [Patches](docs/patches.md).
- **Bounded by default.** Nesting depth, alias expansion, the digits of a number and the work of a patch are bounded unless you pass `NoLimits()`. Input size, documents per stream and scalar length are bounded only with `Untrusted()`; select it, and size it with `Limits`, for input you do not control. Your own hooks run outside these bounds, and the compat packages have no `Untrusted()` mode. A panic caused by input is treated as a [security bug](SECURITY.md). No `unsafe` and no cgo. See [Limits](docs/limits.md).
- **Compatibility adapters for yaml.v3 and sigs.k8s.io/yaml.** For the common API listed in each migration guide, one import change keeps your call sites; the [known differences](#compatibility-adapters-for-yamlv3-and-sigsk8sioyaml) are listed. See [Migrating from yaml.v3](docs/migrate-yaml-v3.md) and [from sigs.k8s.io/yaml](docs/migrate-sigs-k8s-yaml.md).
- **No dependencies** outside the standard library.

## Install

```sh
go get oneyaml.dev/go@latest
```

## Quick start

Change one value in a file. The comment, the block scalar and every other byte stay as they were. (The examples import `fmt`, `log` and `oneyaml "oneyaml.dev/go"`.)

<!-- example: Example_quickStart -->
```go
src := []byte(`# deploy settings
name: api
replicas: 2   # raised by the release job
script: |
  make build
`)

doc, err := oneyaml.Parse(src)
if err != nil {
	log.Fatal(err)
}
if err := doc.Set("/replicas", 3); err != nil {
	log.Fatal(err)
}
if err := doc.Set("/script", "make build\nmake test\n"); err != nil {
	log.Fatal(err)
}
fmt.Print(string(doc.Bytes()))
// Output:
// # deploy settings
// name: api
// replicas: 3   # raised by the release job
// script: |
//   make build
//   make test
```

Reading into a struct works as it does in `encoding/json`:

<!-- example: ExampleUnmarshal -->
```go
src := []byte(`# deploy settings
name: api
replicas: 2
ports: [80, 443]
labels:
  tier: web
`)
var cfg struct {
	Name     string            `yaml:"name"`
	Replicas int               `yaml:"replicas"`
	Ports    []int             `yaml:"ports"`
	Labels   map[string]string `yaml:"labels"`
}
if err := oneyaml.Unmarshal(src, &cfg); err != nil {
	log.Fatal(err)
}
fmt.Println(cfg.Name, cfg.Replicas, cfg.Ports, cfg.Labels)
// Output: api 2 [80 443] map[tier:web]
```

The package documentation on [pkg.go.dev](https://pkg.go.dev/oneyaml.dev/go) has a runnable example for every feature.

## When to use it, and when not to

Use it when you need to change a YAML file that people also edit by hand, apply a JSON Patch to one, read YAML 1.2 with the Core schema, or read input you do not control with `Untrusted()`.

Think twice when:

- You need comments in any place. The comment setters write a comment above an entry or after a value, and refuse places such as most of the inside of a flow collection ([Comments](docs/editing.md#comments)).
- You need to stream events. The parser is not exposed; `Parse` and `NewDecoder` read a whole document.
- You hold very large files in memory. A `Document` keeps the text and an index of it, which is an order of magnitude larger than the text for dense input. Set `MaxInputBytes`, or use `Untrusted()`, for input from outside.
- Your data uses a collection as a mapping key and you read it into `any`. A Go map cannot hold it; a `Document` can.
- Your program depends on go-yaml quirks. The [compat packages](#compatibility-adapters-for-yamlv3-and-sigsk8sioyaml) reproduce documented behaviour, not bugs, and cover yaml.v3 and sigs.k8s.io/yaml only.

[Comparison](docs/comparison.md) says where other libraries are the better choice.

## Correctness

Conformance is measured with [oneyaml-kit](https://github.com/recursive-ai-oss/oneyaml-kit), the conformance kit shared by the oneyaml libraries, against the yaml-test-suite release `data-2022-01-17`. The rows below are generated. [Conformance](docs/conformance.md) has the whole report, explains what each column means, including the strict column for invalid input, and lists every known deviation.

<!-- kit-report:conformance-summary -->
Method: kit 0.1.0 (corpus digest `b1c5e8ce5ca5`, kit revision `0abba1d8b4f2`), every case including the slow ones, run 2026-10-16, library revision `eea67bb6b962`, yaml-test-suite `data-2022-01-17` (revision `6e6c296a`) plus 605 kit cases. Generated by `bin/kit run --slow --adapter oneyaml-go` from the kit's result file `oneyaml-go.json`; do not edit.

| Group | Cases | Loose | Strict | Strict, corrected | Unsupported | Strict only | Emulated |
|---|---:|---:|---:|---:|---:|---:|---:|
| yaml-test-suite · events · invalid input | 94 | 94/94 | 0/94 | 0/94 | 0 | 94 | 0 |
| yaml-test-suite · events · valid input | 308 | 308/308 | 308/308 | – | 0 | 0 | 0 |
| yaml-test-suite · values (in.json) · valid input | 279 | 278/279 | 278/279 | 279/279 | 0 | 0 | 0 |
| **yaml-test-suite · events · all 402** | 402 | 402/402 | 308/402 | 308/402 | 0 | 94 | 0 |
<!-- end kit-report:conformance-summary -->

The library is fuzzed with Go's native fuzzing. The fuzz targets cover [`Unmarshal`](unmarshal_fuzz_test.go), [`Document` reads](document_decode_fuzz_test.go), [edits](document_prop_test.go) and [patches](patch_prop_test.go), and the compat packages [`compat/yamlv3`](compat/yamlv3/fuzz_test.go) and [`compat/k8syaml`](compat/k8syaml/fuzz_test.go). The patch and compat packages are also checked against their targets by differential tests.

## Compatibility adapters for yaml.v3 and sigs.k8s.io/yaml

The compat packages are adapters for the common API of their targets, listed in each migration guide. If your code uses that API, changing the import path keeps your call sites; the package name stays `yaml`.

```sh
# gopkg.in/yaml.v3  ->  oneyaml.dev/go/compat/yamlv3
# sigs.k8s.io/yaml  ->  oneyaml.dev/go/compat/k8syaml
```

Swapping your import does not change the code of your dependencies. A type from another module whose `UnmarshalYAML` takes gopkg.in/yaml.v3's `*yaml.Node` cannot be called by `compat/yamlv3`, so reading into it returns an error that names the type. (yaml.v3 itself would skip such a method and fill the type field by field, without what the method checks.) Read that part through a wrapper type in your own code whose hook the adapter can call, or decode that part with yaml.v3. [Hooks you do not own](docs/migrate-yaml-v3.md#check-after-the-swap) has the details.

They read YAML 1.1 forms the way their targets do, but they are not identical. Known gaps and differences:

- **yaml.v3 comments:** comments land on the same nodes as in yaml.v3 in the common layouts, and are written back where yaml.v3 writes them, inside flow collections too. One flow-mapping layout puts a comment on a different node, and where yaml.v3 writes text that does not read back, the adapter writes text that does.
- **yaml.v3 hooks:** an `UnmarshalYAML` or `MarshalYAML` method of a form the adapter cannot call is an error, where yaml.v3 skips it. That includes a `MarshalYAML` that returns gopkg.in/yaml.v3's own `*yaml.Node` (or a `yaml.Node` of that module anywhere in the value): the error names the type; build the adapter's `yaml.Node` instead.
- **sigs.k8s.io/yaml:** `JSONObjectToYAMLObject` and the subpackages of sigs.k8s.io/yaml are not provided.
- **Numbers:** in `compat/yamlv3`, a plain `3.5` read into an integer field is a type error instead of being truncated. In `compat/k8syaml`, a float read into a string field keeps its full precision, where sigs.k8s.io/yaml rounds it to 32 bits.
- **Valid and invalid input:** both read valid YAML that their targets refuse, `compat/yamlv3` refuses invalid YAML that yaml.v3 accepts, and both return errors where their targets panic.
- **Limits:** both always run with the default limits and take no options, so there is no `Untrusted()` mode. Bound the input size yourself ([how](docs/limits.md#loading-a-configuration-file-strictly)).

Each package's documentation lists every difference ([yamlv3](https://pkg.go.dev/oneyaml.dev/go/compat/yamlv3), [k8syaml](https://pkg.go.dev/oneyaml.dev/go/compat/k8syaml)), and the migration guides have a checklist. New code should use the `oneyaml` package.

## Documentation

| You want to | Read |
|---|---|
| Look up a function or type | [pkg.go.dev](https://pkg.go.dev/oneyaml.dev/go) |
| Change a value, rename a key or write a comment, and keep the file's style | [Editing](docs/editing.md) |
| Apply a JSON Patch or merge patch | [Patches](docs/patches.md) |
| Read input you do not control | [Limits](docs/limits.md), [Security](SECURITY.md) |
| Load a configuration file strictly | [Loading a configuration file strictly](docs/limits.md#loading-a-configuration-file-strictly) |
| Know what is and is not conformant | [Conformance](docs/conformance.md) |
| Move from yaml.v3 | [Migrating from yaml.v3](docs/migrate-yaml-v3.md) |
| Move from sigs.k8s.io/yaml | [Migrating from sigs.k8s.io/yaml](docs/migrate-sigs-k8s-yaml.md) |
| See speed and memory against other Go libraries | [Performance](docs/performance.md) |
| Compare with other Go libraries | [Comparison](docs/comparison.md) |
| See what changed | [Changelog](CHANGELOG.md) |
| Know what is stable and which Go versions are supported | [Stability](#stability) |
| Contribute | [Contributing](CONTRIBUTING.md) |

## Stability

From 1.0.0 the module follows Go's [rules for a stable module](https://go.dev/doc/modules/version-numbers), in the spirit of the [Go 1 compatibility promise](https://go.dev/doc/go1compat). Within major version 1:

- **Your code keeps compiling and keeps its meaning.** Code that uses the exported API of `oneyaml.dev/go`, `oneyaml.dev/go/compat/yamlv3` or `oneyaml.dev/go/compat/k8syaml` builds with every later 1.x release and behaves as documented. That covers the error types, the sentinel errors you match with `errors.Is` and `errors.As`, and the `Limit` and `Category` constants.
- **Written bytes stay the same.** The bytes that `Marshal` and an `Encoder` write with default options do not change, and neither do the bytes that an edit leaves untouched.

A minor release may still make these changes:

- **Additions:** new names, methods, struct fields and `Limit` constants. A new field breaks a struct literal without field names, so write `LimitSet`, the error types and the other structs of these packages with field names, as in `oneyaml.LimitSet{MaxDepth: 50}`, and give a `switch` on `Limit` a default case.
- **Bug fixes**, including refusing input that the YAML 1.2 specification calls invalid, and writing text that reads back as the value written where the output did not. Each such fix is listed in the [changelog](CHANGELOG.md).
- **Error messages:** their text is written for people and may change. Match errors by type, sentinel and constant.
- **The compat packages:** where `compat/yamlv3` or `compat/k8syaml` places comments or lays out output differently from yaml.v3 or sigs.k8s.io/yaml, a minor release may bring it closer to its target.

Patch releases only fix bugs. Security fixes go into the latest minor release ([security policy](SECURITY.md)).

Any other change that breaks code needs a new major version with its own module path, `oneyaml.dev/go/v2`, so code that imports `oneyaml.dev/go` keeps building against 1.x.

Releases before 1.0 could break code in a minor release. The [changelog](CHANGELOG.md) marks each such change, with what to write instead.

**Go versions.** The minimum is Go 1.24 (`go 1.24` in `go.mod`), and CI tests Go 1.24, 1.25, 1.26 and 1.27. Raising the minimum takes a minor release and a note in the changelog. Go itself supports only its two newest major releases ([release policy](https://go.dev/doc/devel/release#policy)), so the minimum reaches back past the releases that still get fixes from Go.

This is the only place this policy is stated; other pages link here.

## License

Licensed under either of [Apache License, Version 2.0](LICENSE-APACHE) or [MIT license](LICENSE-MIT), at your option. Unless you state otherwise, any contribution you submit for inclusion is dual licensed as above, without additional terms or conditions.
