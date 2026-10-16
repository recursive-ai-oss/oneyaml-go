# oneyaml-go

**Pure-Go YAML 1.2 that edits a file and keeps its comments.**

[![Go Reference](https://pkg.go.dev/badge/oneyaml.dev/go.svg)](https://pkg.go.dev/oneyaml.dev/go)
[![CI](https://github.com/recursive-ai-oss/oneyaml-go/actions/workflows/ci.yml/badge.svg)](https://github.com/recursive-ai-oss/oneyaml-go/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/badge/go-1.24%2B-00ADD8)](#stability)
[![License](https://img.shields.io/badge/license-MIT%20OR%20Apache--2.0-blue)](#license)

Spec-correct YAML 1.2 by default. Edits change only the value you set, in its own style; comments and formatting elsewhere are kept byte for byte. Nesting, alias expansion, number size and patch work are bounded by default.

Benchmark results will come in a later update, measured with the [oneyaml-kit](https://github.com/recursive-ai-oss/oneyaml-kit) harness.

- **Spec-correct YAML 1.2 by default.** Plain scalars follow the Core schema, so `yes` is a string and `0755` is the integer 755. YAML 1.1 forms come only from the [compat packages](#compatibility-adapters-for-yamlv3-and-sigsk8sioyaml). Results against the yaml-test-suite are under [Correctness](#correctness).
- **Minimal-diff edits.** `Document.Set` rewrites one value in its own style and falls back to the nearest style that can hold the new value. Reading it back always gives exactly the value you set. See [Editing](docs/editing.md).
- **Patches.** RFC 6902 JSON Patch, with `test` comparing values by the YAML data model (so `1` and `1.0` [differ](docs/patches.md#the-test-operation)), RFC 6901 pointers and RFC 7396 merge patch apply to a YAML file atomically and keep its layout. See [Patches](docs/patches.md).
- **Bounded by default.** Nesting depth, alias expansion, the digits of a number and the work of a patch are bounded unless you pass `NoLimits()`. Input size, documents per stream and scalar length are bounded only with `Untrusted()`; select it, and size it with `Limits`, for input you do not control. Your own hooks run outside these bounds, and the compat packages have no `Untrusted()` mode. A panic caused by input is treated as a [security bug](SECURITY.md). No `unsafe` and no cgo. See [Limits](docs/limits.md).
- **Compatibility adapters for yaml.v3 and sigs.k8s.io/yaml.** One import change keeps your call sites; the [known differences](#compatibility-adapters-for-yamlv3-and-sigsk8sioyaml) are listed. See [Migrating from yaml.v3](docs/migrate-yaml-v3.md) and [from sigs.k8s.io/yaml](docs/migrate-sigs-k8s-yaml.md).
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

Use it when you need to change a YAML file that people also edit by hand, apply a JSON Patch to one, read YAML 1.2 as the specification says, or read input you do not control with `Untrusted()`.

Think twice when:

- You need to edit comments. A `Node` reads comments, but there is no API to change them yet.
- You need to stream events. The parser is not exposed; `Parse` and `NewDecoder` read a whole document.
- You hold very large files in memory. A `Document` keeps the text and an index of it, which is an order of magnitude larger than the text for dense input. Set `MaxInputBytes`, or use `Untrusted()`, for input from outside.
- Your data uses a collection as a mapping key and you read it into `any`. A Go map cannot hold it; a `Document` can.
- Your program depends on go-yaml quirks. The [compat packages](#compatibility-adapters-for-yamlv3-and-sigsk8sioyaml) reproduce documented behaviour, not bugs, and cover yaml.v3 and sigs.k8s.io/yaml only.

[Comparison](docs/comparison.md) says where other libraries are the better choice.

## Correctness

Conformance is measured with [oneyaml-kit](https://github.com/recursive-ai-oss/oneyaml-kit), the conformance kit shared by the oneyaml libraries, against the yaml-test-suite release `data-2022-01-17`. The rows below are generated. [Conformance](docs/conformance.md) has the whole report, explains what each column means, including the strict column for invalid input, and lists every known deviation.

<!-- generated:conformance-summary -->
Method: kit 0.1.0 (corpus digest `3128b9207c00`, kit revision `4777c7d2370d`), every case including the slow ones, run 2026-10-16, library revision `813058186b9a`, yaml-test-suite `data-2022-01-17` (revision `6e6c296a`) plus 574 kit cases. Generated by `bin/kit run --slow --adapter oneyaml-go` from the kit's result file `oneyaml-go.json`; do not edit.

| Group | Cases | Loose | Strict | Strict, corrected | Unsupported | Strict only | Emulated |
|---|---:|---:|---:|---:|---:|---:|---:|
| yaml-test-suite · events · invalid input | 94 | 94/94 | 0/94 | 0/94 | 0 | 94 | 0 |
| yaml-test-suite · events · valid input | 308 | 308/308 | 308/308 | – | 0 | 0 | 0 |
| yaml-test-suite · values (in.json) · valid input | 279 | 278/279 | 278/279 | 279/279 | 0 | 0 | 0 |
| **yaml-test-suite · events · all 402** | 402 | 402/402 | 308/402 | 308/402 | 0 | 94 | 0 |
<!-- end generated -->

The library is fuzzed with Go's native fuzzing, and its patch and compat packages are checked against their targets by differential tests.

## Compatibility adapters for yaml.v3 and sigs.k8s.io/yaml

The compat packages are adapters with the same names and signatures as their targets, so changing the import path keeps your call sites; the package name stays `yaml`.

```sh
# gopkg.in/yaml.v3  ->  oneyaml.dev/go/compat/yamlv3
# sigs.k8s.io/yaml  ->  oneyaml.dev/go/compat/k8syaml
```

They read YAML 1.1 forms the way their targets do, but they are not identical. Known gaps and differences:

- **yaml.v3:** a repeated key in a mapping that the target ignores (a struct with no such field) is not reported. Some comment layouts, such as comment lines inside a flow collection, are not placed or written back as yaml.v3 does.
- **Numbers:** in `compat/yamlv3`, a plain `3.5` read into an integer field is a type error instead of being truncated. In `compat/k8syaml`, a float read into a string field keeps its full precision, where sigs.k8s.io/yaml rounds it to 32 bits.
- **Valid and invalid input:** both read valid YAML that their targets refuse, `compat/yamlv3` refuses invalid YAML that yaml.v3 accepts, and both return errors where their targets panic.
- **Limits:** both always run with the default limits and have no `Untrusted()` mode.

Each package's documentation lists every difference ([yamlv3](https://pkg.go.dev/oneyaml.dev/go/compat/yamlv3), [k8syaml](https://pkg.go.dev/oneyaml.dev/go/compat/k8syaml)), and the migration guides have a checklist. New code should use the `oneyaml` package.

## Documentation

| You want to | Read |
|---|---|
| Look up a function or type | [pkg.go.dev](https://pkg.go.dev/oneyaml.dev/go) |
| Change a value and keep the file's style | [Editing](docs/editing.md) |
| Apply a JSON Patch or merge patch | [Patches](docs/patches.md) |
| Read input you do not control | [Limits](docs/limits.md), [Security](SECURITY.md) |
| Know what is and is not conformant | [Conformance](docs/conformance.md) |
| Move from yaml.v3 | [Migrating from yaml.v3](docs/migrate-yaml-v3.md) |
| Move from sigs.k8s.io/yaml | [Migrating from sigs.k8s.io/yaml](docs/migrate-sigs-k8s-yaml.md) |
| Compare with other Go libraries | [Comparison](docs/comparison.md) |
| See what changed | [Changelog](CHANGELOG.md) |
| Know what is stable and which Go versions are supported | [Stability](#stability) |
| Contribute | [Contributing](CONTRIBUTING.md) |

## Stability

This is v0, the first series of releases. The API may still change before 1.0, but only in a minor release (0.1 to 0.2, say), never in a patch release. Every change that can break your code is listed in the [changelog](CHANGELOG.md) and marked **Breaking**, with what to write instead, so read it before you move to a new minor version.

The minimum Go version is 1.24 (`go 1.24` in `go.mod`). CI tests Go 1.24, 1.25, 1.26 and 1.27. Raising the minimum takes a minor release and is announced in the changelog.

From 1.0, the module follows Go's [rules for a stable module](https://go.dev/doc/modules/version-numbers). Within a major version:

- No change breaks code that uses the exported API of `oneyaml.dev/go`, `compat/yamlv3` or `compat/k8syaml`. That covers the error types and the sentinel errors you match with `errors.As` and `errors.Is`; the text of error messages is written for people and may change in any release.
- The bytes that `Marshal` writes with default options do not change, and neither do the bytes that an edit leaves untouched, except to fix output that is incorrect. Such a fix is listed in the changelog.

A change that would break these promises means a new major version with its own module path, `oneyaml.dev/go/v2`.

This is the only place this policy is stated; other pages link here.

## License

Licensed under either of [Apache License, Version 2.0](LICENSE-APACHE) or [MIT license](LICENSE-MIT), at your option. Unless you state otherwise, any contribution you submit for inclusion is dual licensed as above, without additional terms or conditions.
