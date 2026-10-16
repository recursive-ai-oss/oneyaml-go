# Contributing

Thanks for helping. Bug reports, reproductions, documentation fixes and pull requests are all welcome.

## Before you start

- **Found a vulnerability?** Do not open an issue. Follow [SECURITY.md](SECURITY.md).
- **Found a bug?** Open an issue with the smallest YAML or Go program that shows it, the version, and what you expected. A failing test is better still.
- **Want a feature?** Open an issue first and describe the problem you are solving. The library keeps a small API on purpose, so a feature that is not discussed may not be accepted.

By taking part you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Setup

You need a supported Go version (see [Stability](README.md#stability)) and [staticcheck](https://staticcheck.dev/). Clone the repository and run:

```sh
go test ./...
```

The repository has three Go modules: the library at the root, `bench/` (benchmarks and differential tests against other libraries) and `tools/` (repository checks). Each has its own `go.mod`; the library has no dependencies, and a pull request that adds one to the root module will be declined unless there is a strong reason.

## Checks

Before you send a pull request, run the local pre-merge check:

```sh
tools/gate
```

It runs `gofmt`, `go vet`, `staticcheck` and `go test -short` in every module, then a text check over the files you changed against `main` (set `GATE_BASE` to compare with another branch). CI runs these checks too, with the tests on every Go version listed under [Stability](README.md#stability). Its text check also reads a pattern list that CI supplies through `PUBLICTEXT_PATTERNS`; you do not need that list to run the gate.

For a change to the parser, the decoder, edits or patches, also run the whole suite, `go test ./...`, and where it applies the fuzz targets, for example `go test -run '^$' -fuzz FuzzDocument -fuzztime 2m .`.

## What a good pull request has

- **One change.** Keep a pull request to one fix or one feature.
- **A test that fails without it.** Behaviour that could plausibly break gets a test. There is no coverage target.
- **Documentation.** An exported identifier has a doc comment that starts with its name. A new feature has a runnable `Example` test. Go code blocks in `README.md` and `docs/` are copies of `Example` functions: the line before the block names the example, as in `<!-- example: ExampleDocument_Set -->`, and `go test` fails if the two differ. Edit the `Example`, then copy its body.
- **No hand-typed numbers.** Benchmark and conformance figures in the documentation are generated, between `<!-- generated:... -->` markers. Do not edit inside them. The conformance report regions, between `<!-- kit-report:... -->` markers, are the kit's own report pasted in at release. Regenerate them with the oneyaml-kit `bin/bench readme` command, which writes the blocks in `README.md`, `docs/performance.md` and `docs/conformance.md` and the charts in `docs/img` from a benchmark result and a conformance result for `oneyaml-go`: `bin/bench readme --results <results directory> --library oneyaml-go --readme README.md --docs docs --charts docs/img`. A result that is not publishable is refused unless you add `--allow-smoke`, and then every block says it is a smoke result. `go test` checks that the blocks are present and paired, not what they say.
- **A changelog line** under `[Unreleased]` in [CHANGELOG.md](CHANGELOG.md) for anything a user can see. Mark breaking changes.
- **Errors are values.** Examples check every error. Library code must not panic on any input, a panic caused by input is treated as a security bug, and the library never uses `recover` to hide one.
- **No `unsafe`.** Library code does not use it.

## Performance changes

Show before and after with `benchstat` on the benchmarks you touched (`go test -bench . -benchmem -count 10`). A change that slows a benchmark or adds allocations needs a reason.

## Licence

The project is dual licensed under [MIT](LICENSE-MIT) or [Apache-2.0](LICENSE-APACHE). Unless you state otherwise, your contribution is licensed the same way, without additional terms.
